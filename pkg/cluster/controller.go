package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	clusterclientv1 "open-cluster-management.io/api/client/cluster/clientset/versioned/typed/cluster/v1"
	clusterinformerv1 "open-cluster-management.io/api/client/cluster/informers/externalversions/cluster/v1"
	clusterlisterv1 "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	workclientv1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	workinformerv1 "open-cluster-management.io/api/client/work/informers/externalversions/work/v1"
	worklisterv1 "open-cluster-management.io/api/client/work/listers/work/v1"

	"github.com/stolostron/hub-cluster-controller/pkg/packagemanifest"
)

// clusterController reconciles instances of ManagedCluster on the hub.
type clusterController struct {
	dynamicClient dynamic.Interface
	kubeClient    *kubernetes.Clientset
	workClient    workclientv1.WorkV1Interface
	clusterClient clusterclientv1.ClusterV1Interface
	clusterLister clusterlisterv1.ManagedClusterLister
	workLister    worklisterv1.ManifestWorkLister
	cache         resourceapply.ResourceCache
	eventRecorder events.Recorder
}

// NewHubClusterController creates a new hub cluster controller
func NewHubClusterController(
	dynamicClient dynamic.Interface,
	kubeClient *kubernetes.Clientset,
	workClient workclientv1.WorkV1Interface,
	clusterClient clusterclientv1.ClusterV1Interface,
	clusterInformer clusterinformerv1.ManagedClusterInformer,
	workInformer workinformerv1.ManifestWorkInformer,
	recorder events.Recorder) factory.Controller {
	c := &clusterController{
		dynamicClient: dynamicClient,
		kubeClient:    kubeClient,
		workClient:    workClient,
		clusterClient: clusterClient,
		clusterLister: clusterInformer.Lister(),
		workLister:    workInformer.Lister(),
		cache:         resourceapply.NewResourceCache(),
		eventRecorder: recorder.WithComponentSuffix("hub-cluster-controller"),
	}
	return factory.New().
		WithFilteredEventsInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				accessor, _ := meta.Accessor(obj)
				return accessor.GetName()
			},
			func(obj interface{}) bool {
				accessor, err := meta.Accessor(obj)
				if err != nil {
					return false
				}
				// enqueue all managed cluster except for local-cluster and hoh=disabled
				if accessor.GetLabels()["vendor"] != "OpenShift" ||
					accessor.GetLabels()["hoh"] == "disabled" || accessor.GetName() == "local-cluster" {
					return false
				} else {
					return true
				}
			}, clusterInformer.Informer()).
		WithFilteredEventsInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				accessor, _ := meta.Accessor(obj)
				if strings.Contains(accessor.GetName(), "-hoh-hub-cluster-management") {
					return strings.ReplaceAll(accessor.GetName(), "-hoh-hub-cluster-management", "")
				} else {
					return accessor.GetNamespace()
				}
			},
			func(obj interface{}) bool {
				accessor, err := meta.Accessor(obj)
				if err != nil {
					return false
				}
				// only enqueue when the hoh=enabled managed cluster is changed
				if accessor.GetName() == accessor.GetNamespace()+"-"+hohHubClusterSubscription ||
					accessor.GetName() == accessor.GetNamespace()+"-"+hohHubClusterMCH ||
					strings.Contains(accessor.GetName(), "-hoh-hub-cluster-management") {
					return true
				}
				return false
			}, workInformer.Informer()).
		WithSync(c.sync).
		ToController("HubClusterController", recorder)
}

func (c *clusterController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	managedClusterName := syncCtx.QueueKey()
	klog.V(2).Infof("Reconciling for %s", managedClusterName)
	managedCluster, err := c.clusterLister.Get(managedClusterName)
	if err != nil {
		return err
	}

	hostingClusterName, hostedClusterName, hypershiftDeploymentNamespace := "", "", ""
	annotations := managedCluster.GetAnnotations()
	if val, ok := annotations["import.open-cluster-management.io/klusterlet-deploy-mode"]; ok && val == "Hosted" {
		hostingClusterName, ok = annotations["import.open-cluster-management.io/hosting-cluster-name"]
		if !ok || hostingClusterName == "" {
			return fmt.Errorf("missing hosting-cluster-name in managed cluster.")
		}
		hypershiftdeploymentName, ok := annotations["cluster.open-cluster-management.io/hypershiftdeployment"]
		if !ok || hypershiftdeploymentName == "" {
			return fmt.Errorf("missing hypershiftdeployment name in managed cluster.")
		}
		splits := strings.Split(hypershiftdeploymentName, "/")
		if len(splits) != 2 || splits[1] == "" {
			return fmt.Errorf("bad hypershiftdeployment name in managed cluster.")
		}
		hypershiftDeploymentNamespace = splits[0]
		hostedClusterName = splits[1]

		// for managedcluster that is hypershift hosted cluster, add new annotation
		if val, ok := annotations["hub-of-hubs.open-cluster-management.io/managed-by-hoh"]; !ok || val != "true" {
			annotations["hub-of-hubs.open-cluster-management.io/managed-by-hoh"] = "true"
			managedCluster.SetAnnotations(annotations)
			if _, err := c.clusterClient.ManagedClusters().Update(ctx, managedCluster, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}

	if !managedCluster.DeletionTimestamp.IsZero() {
		// the managed cluster is deleting, we should not re-apply the manifestwork
		// wait for managedcluster-import-controller to clean up the manifestwork
		if hostingClusterName == "" { // for non-hypershift hosted leaf hub
			return removePostponeDeleteAnnotationForSubManifestwork(ctx, c.workClient, c.workLister, managedClusterName)
		} else { // for hypershift hosted leaf hub, remove the corresponding manifestwork from hypershift management cluster
			return removeHubManifestworkFromHyperMgtCluster(ctx, c.workClient, managedClusterName, hostingClusterName)
		}
	}

	if hostingClusterName == "" { // for non-hypershift hosted leaf hub
		subscriptionManifestwork, err := ApplySubManifestWorks(ctx, c.kubeClient, c.workClient, c.workLister, managedClusterName)
		if err != nil {
			klog.V(2).Infof("failed to apply subscription manifestwork: %v", err)
			return err
		}

		if subscriptionManifestwork == nil {
			klog.V(2).Infof("subscription manifestwork is nil, retry after 1 second")
			syncCtx.Queue().AddAfter(managedClusterName, 1*time.Second)
			return nil
		}
		// if the csv PHASE is Succeeded, then create mch manifestwork to install Hub
		klog.V(2).Infof("checking status feedback value from subscription before applying mch manifestwork")
		for _, manifestCondition := range subscriptionManifestwork.Status.ResourceStatus.Manifests {
			if manifestCondition.ResourceMeta.Kind == "Subscription" {
				for _, value := range manifestCondition.StatusFeedbacks.Values {
					if value.Name == "state" && *value.Value.String == "AtLatestKnown" {
						//fetch user defined mch from annotation
						err := ApplyMCHManifestWorks(ctx, c.kubeClient, c.workClient, c.workLister, managedClusterName)
						if err != nil {
							return err
						}
						return nil
					}
				}
			}
		}
	} else { // for hypershift hosted leaf hub
		p := packagemanifest.GetPackageManifest()
		if p == nil || p.ACMCurrentCSV == "" || p.ACMDefaultChannel == "" {
			klog.V(2).Infof("package manifest is not ready, retry after 1 second")
			syncCtx.Queue().AddAfter(managedClusterName, 1*time.Second)
			return nil
		}

		hypershiftDeploymentGVR := schema.GroupVersionResource{
			Group:    "cluster.open-cluster-management.io",
			Version:  "v1alpha1",
			Resource: "hypershiftdeployments",
		}
		hypershiftDeploymentCR, err := c.dynamicClient.Resource(hypershiftDeploymentGVR).Namespace(hypershiftDeploymentNamespace).
			Get(ctx, hostedClusterName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		hypershiftDeploymentSpec := hypershiftDeploymentCR.Object["spec"].(map[string]interface{})
		hostingNamespace := hypershiftDeploymentSpec["hostingNamespace"].(string)

		hubMgtManifestwork, err := c.workLister.ManifestWorks(hostingClusterName).Get(managedClusterName + "-hoh-hub-cluster-management")
		if errors.IsNotFound(err) {
			klog.V(2).Infof("creating hub manifestwork for managedcluster %s", managedClusterName)
			if err := ApplyHubManifestWorks(ctx, c.kubeClient, c.workClient, c.workLister, managedClusterName, hostingClusterName, hostingNamespace, hostedClusterName, ""); err != nil {
				klog.V(2).Infof("failed to apply hub manifestwork: %v", err)
				return err
			}
			return nil
		}

		klog.V(2).Infof("checking status feedback value from hub manifestwork before applying channel service in manifestwork")
		for _, manifestCondition := range hubMgtManifestwork.Status.ResourceStatus.Manifests {
			if manifestCondition.ResourceMeta.Kind == "Service" {
				for _, value := range manifestCondition.StatusFeedbacks.Values {
					if value.Name == "clusterIP" && value.Value.String != nil {
						klog.V(2).Infof("Got clusterIP for channel service %s", *value.Value.String)
						channelClusterIP := *value.Value.String
						if err := ApplyHubManifestWorks(ctx, c.kubeClient, c.workClient, c.workLister, managedClusterName, hostingClusterName, hostingNamespace, hostedClusterName, channelClusterIP); err != nil {
							klog.V(2).Infof("failed to apply hub manifestwork: %v", err)
							return err
						}
						return nil
					}
				}
			}
		}

		return nil
	}

	return nil
}
