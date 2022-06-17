package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
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
	kubeClient    *kubernetes.Clientset
	workClient    workclientv1.WorkV1Interface
	clusterLister clusterlisterv1.ManagedClusterLister
	workLister    worklisterv1.ManifestWorkLister
	cache         resourceapply.ResourceCache
	eventRecorder events.Recorder
}

// NewHubClusterController creates a new hub cluster controller
func NewHubClusterController(
	kubeClient *kubernetes.Clientset,
	workClient workclientv1.WorkV1Interface,
	clusterInformer clusterinformerv1.ManagedClusterInformer,
	workInformer workinformerv1.ManifestWorkInformer,
	recorder events.Recorder) factory.Controller {
	c := &clusterController{
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
				return accessor.GetNamespace()
			},
			func(obj interface{}) bool {
				accessor, err := meta.Accessor(obj)
				if err != nil {
					return false
				}
				// only enqueue when the hoh=enabled managed cluster is changed
				if accessor.GetName() == accessor.GetNamespace()+"-"+hohHubClusterSubscription ||
					accessor.GetName() == accessor.GetNamespace()+"-"+hohHubClusterMCH {
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
		return removePostponeDeleteAnnotationForSubManifestwork(ctx, c.workClient, c.workLister, managedClusterName)
	}

	if hostingClusterName == "" { // for non-hypershift hosted leaf hub
		subscription, err := ApplySubManifestWorks(ctx, c.kubeClient, c.workClient, c.workLister, managedClusterName)
		if err != nil {
			return err
		}

		if subscription == nil {
			syncCtx.Queue().AddAfter(managedClusterName, 1*time.Second)
			return nil
		}
		// if the csv PHASE is Succeeded, then create mch manifestwork to install Hub
		for _, conditions := range subscription.Status.ResourceStatus.Manifests {
			if conditions.ResourceMeta.Kind == "Subscription" {
				for _, value := range conditions.StatusFeedbacks.Values {
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
		if err := ApplyHubManifestWorks(ctx, c.workClient, c.workLister, managedClusterName, hostingClusterName, hostedClusterName); err != nil {
			return err
		}

		return nil
	}

	return nil
}
