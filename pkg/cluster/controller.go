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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	clusterinformerv1 "open-cluster-management.io/api/client/cluster/informers/externalversions/cluster/v1"
	clusterlisterv1 "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	workclientv1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	workinformerv1 "open-cluster-management.io/api/client/work/informers/externalversions/work/v1"
	worklisterv1 "open-cluster-management.io/api/client/work/listers/work/v1"
)

// clusterController reconciles instances of ManagedCluster on the hub.
type clusterController struct {
	dynamicClient dynamic.Interface
	kubeClient    *kubernetes.Clientset
	workClient    workclientv1.WorkV1Interface
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
	clusterInformer clusterinformerv1.ManagedClusterInformer,
	workInformer workinformerv1.ManifestWorkInformer,
	recorder events.Recorder) factory.Controller {
	c := &clusterController{
		dynamicClient: dynamicClient,
		kubeClient:    kubeClient,
		workClient:    workClient,
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
				// for hypershift hosted cluster, there is no vendor label.
				// TODO: also need to filter the *KS
				if accessor.GetLabels()["hoh"] == "disabled" || accessor.GetName() == "local-cluster" {
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

	hostingClusterName, hostedClusterName := "", ""
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
		hostedClusterName = splits[1]
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
		subscription, err := ApplySubManifestWorks(ctx, c.kubeClient, c.workClient, c.workLister, managedClusterName)
		if err != nil {
			klog.V(2).Infof("failed to apply subscription manifestwork: %v", err)
			return err
		}

		if subscription == nil {
			klog.V(2).Infof("subscription manifestwork is nil, retry after 1 second")
			syncCtx.Queue().AddAfter(managedClusterName, 1*time.Second)
			return nil
		}
		// if the csv PHASE is Succeeded, then create mch manifestwork to install Hub
		klog.V(2).Infof("checking status feedback value from subscription before applying mch manifestwork")
		for _, manifestCondition := range subscription.Status.ResourceStatus.Manifests {
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
		// apply the CRDs into hypershift hosted cluster via helm chart subscription
		if err := ApplyHubHelmSub(ctx, c.dynamicClient, managedClusterName); err != nil {
			return err
		}

		appliedHubManifestwork, err := c.workLister.ManifestWorks(hostingClusterName).Get(managedClusterName + "-hoh-hub-cluster-management")
		if errors.IsNotFound(err) {
			klog.V(2).Infof("hub manifestwork is not found, creating it....")
			return ApplyHubManifestWorks(ctx, c.workClient, c.workLister, managedClusterName, hostingClusterName, hostedClusterName, "")
		}
		if err != nil {
			return err
		}

		klog.V(2).Infof("checking status feedback value from hub manifestwork before applying mch manifestwork")
		for _, manifestCondition := range appliedHubManifestwork.Status.ResourceStatus.Manifests {
			if manifestCondition.ResourceMeta.Kind == "Service" {
				for _, value := range manifestCondition.StatusFeedbacks.Values {
					if value.Name == "clusterIP" && value.Value.String != nil {
						klog.V(2).Infof("Got clusterIP for channel service %s", *value.Value.String)
						channelClusterIP := *value.Value.String
						err := ApplyHubManifestWorks(ctx, c.workClient, c.workLister, managedClusterName, hostingClusterName, hostedClusterName, channelClusterIP)
						if err != nil {
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
