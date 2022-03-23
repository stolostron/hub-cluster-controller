package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	workclientv1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	worklisterv1 "open-cluster-management.io/api/client/work/listers/work/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	"github.com/stolostron/hub-cluster-controller/pkg/packagemanifest"
)

const (
	hohHubClusterSubscription = "hoh-hub-cluster-subscription"
	hohHubClusterMCH          = "hoh-hub-cluster-mch"
	workPostponeDeleteAnnoKey = "open-cluster-management/postpone-delete"
)

func createSubManifestwork(namespace string, p *packagemanifest.PackageManifest) *workv1.ManifestWork {
	if p == nil || p.CurrentCSV == "" || p.DefaultChannel == "" {
		return nil
	}
	currentCSV := p.CurrentCSV
	channel := p.DefaultChannel
	source := "redhat-operators"
	// for test develop version
	snapshot, _ := os.LookupEnv("SNAPSHOT")
	if snapshot != "" {
		channel = "release-2.5"
		source = "acm-custom-registry"
		currentCSV = "advanced-cluster-management.v2.5.0"
	}

	manifestwork := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespace + "-" + hohHubClusterSubscription,
			Namespace: namespace,
			Labels: map[string]string{
				"hub-of-hubs.open-cluster-management.io/managed-by": "hoh",
			},
			Annotations: map[string]string{
				// Add the postpone delete annotation for manifestwork so that the observabilityaddon can be
				// cleaned up before the manifestwork is deleted by the managedcluster-import-controller when
				// the corresponding managedcluster is detached.
				// Note the annotation value is currently not taking effect, because managedcluster-import-controller
				// managedcluster-import-controller hard code the value to be 10m
				workPostponeDeleteAnnoKey: "",
			},
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{RawExtension: runtime.RawExtension{
						Raw: []byte(`{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRole",
	"metadata": {
		"name": "open-cluster-management:hub-cluster-controller"
	},
	"rules": [
		{
			"apiGroups": [
				"operators.coreos.com"
			],
			"resources": [
				"operatorgroups",
				"subscriptions"
			],
			"verbs": [
				"create",
				"update"
			]
		}
	]
}`),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: []byte(`{
	"apiVersion": "rbac.authorization.k8s.io/v1",
	"kind": "ClusterRoleBinding",
	"metadata": {
		"name": "open-cluster-management-agent:klusterlet-work-sa"
	},
	"roleRef": {
		"apiGroup": "rbac.authorization.k8s.io",
		"kind": "ClusterRole",
		"name": "open-cluster-management:hub-cluster-controller"
	},
	"subjects": [
		{
			"kind": "ServiceAccount",
			"name": "klusterlet-work-sa",
			"namespace": "open-cluster-management-agent"
		}
	]
}`),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: []byte(`{
	"apiVersion": "v1",
	"kind": "Namespace",
	"metadata": {
		"name": "open-cluster-management"
	}
}`),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: []byte(`{
	"apiVersion": "operators.coreos.com/v1",
	"kind": "OperatorGroup",
	"metadata": {
		"name": "open-cluster-management-group",
		"namespace": "open-cluster-management"
	},
	"spec": {
		"targetNamespaces": [
			"open-cluster-management"
		]
	}
}`),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: []byte(fmt.Sprintf(`{
	"apiVersion": "operators.coreos.com/v1alpha1",
	"kind": "Subscription",
	"metadata": {
		"name": "acm-operator-subscription",
		"namespace": "open-cluster-management"
	},
	"spec": {
		"channel": "%s",
		"installPlanApproval": "Automatic",
		"name": "advanced-cluster-management",
		"source": "%s",
		"sourceNamespace": "openshift-marketplace",
		"startingCSV": "%s"
	}
}`, channel, source, currentCSV)),
					}},
				},
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Group:     "operators.coreos.com",
						Resource:  "subscriptions",
						Name:      "acm-operator-subscription",
						Namespace: "open-cluster-management",
					},
					FeedbackRules: []workv1.FeedbackRule{
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "state",
									Path: ".status.state",
								},
							},
						},
					},
				},
			},
		},
	}

	if snapshot != "" {
		manifestwork.Spec.Workload.Manifests = append(manifestwork.Spec.Workload.Manifests,
			workv1.Manifest{
				RawExtension: runtime.RawExtension{
					Raw: []byte(fmt.Sprintf(`{
"apiVersion": "operators.coreos.com/v1alpha1",
"kind": "CatalogSource",
"metadata": {
	"name": "%s",
	"namespace": "openshift-marketplace"
},
"spec": {
	"displayName": "Advanced Cluster Management",
	"image": "%s",
	"publisher": "Red Hat",
	"sourceType": "grpc",
	"updateStrategy": { 
	  "registryPoll": {
		"interval": "10m"
	  }
	}
}
}`, source, snapshot)),
				}})
	}

	return manifestwork
}

func createMCHManifestwork(namespace, userDefinedMCH string) (*workv1.ManifestWork, error) {
	mchJson := `{
		"apiVersion": "operator.open-cluster-management.io/v1",
		"kind": "MultiClusterHub",
		"metadata": {
			"name": "multiclusterhub",
			"namespace":"open-cluster-management"
		},
		"spec": {
			"disableHubSelfManagement": true
		}
	}`
	if userDefinedMCH != "" {
		var mch interface{}
		err := json.Unmarshal([]byte(userDefinedMCH), &mch)
		if err != nil {
			return nil, err
		}
		mch.(map[string]interface{})["spec"].(map[string]interface{})["disableHubSelfManagement"] = true
		mchBytes, err := json.Marshal(mch)
		if err != nil {
			return nil, err
		}
		mchJson = string(mchBytes)
	}
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespace + "-" + hohHubClusterMCH,
			Namespace: namespace,
			Labels: map[string]string{
				"hub-of-hubs.open-cluster-management.io/managed-by": "hoh",
			},
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{RawExtension: runtime.RawExtension{
						Raw: []byte(mchJson),
					}},
				},
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Group:     "operator.open-cluster-management.io",
						Resource:  "multiclusterhubs",
						Name:      "multiclusterhub",
						Namespace: "open-cluster-management",
					},
					FeedbackRules: []workv1.FeedbackRule{
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "state",
									Path: ".status.phase",
								},
							},
						},
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "currentVersion",
									Path: ".status.currentVersion",
								},
							},
						},
						// ideally, the mch status should be in Running state.
						// but due to this bug - https://github.com/stolostron/backlog/issues/20555
						// the mch status can be in Installing for a long time.
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "application-chart-sub-status",
									Path: ".status.components.application-chart-sub.status",
								},
							},
						},
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "cluster-manager-cr-status",
									Path: ".status.components.cluster-manager-cr.status",
								},
							},
						},
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "multicluster-engine-status",
									Path: ".status.components.multicluster-engine.status",
								},
							},
						},
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "grc-sub-status",
									Path: ".status.components.grc-sub.status",
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func EnsureManifestWork(existing, desired *workv1.ManifestWork) (bool, error) {
	// compare the manifests
	existingBytes, err := json.Marshal(existing.Spec)
	if err != nil {
		return false, err
	}
	desiredBytes, err := json.Marshal(desired.Spec)
	if err != nil {
		return false, err
	}
	if string(existingBytes) != string(desiredBytes) {
		return true, nil
	}
	return false, nil
}

func ApplySubManifestWorks(ctx context.Context, workclient workclientv1.WorkV1Interface,
	workLister worklisterv1.ManifestWorkLister, managedClusterName string) (*workv1.ManifestWork, error) {

	desiredSubscription := createSubManifestwork(managedClusterName, packagemanifest.GetPackageManifest())
	if desiredSubscription == nil {
		return nil, nil
	}

	subscription, err := workLister.ManifestWorks(managedClusterName).Get(managedClusterName + "-" + hohHubClusterSubscription)
	if errors.IsNotFound(err) {
		klog.V(2).Infof("creating subscription manifestwork in %s namespace", managedClusterName)
		_, err := workclient.ManifestWorks(managedClusterName).
			Create(ctx, desiredSubscription, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}

	// Do not need to update if the packagemanifest is changed
	// for example: the existing DefaultChannel is release-2.4, once the new release is delivered.
	// the DefaultChannel will be release-2.5. but we do not need to update the manifestwork.
	p, err := getExistingPackageManifestInfo(subscription)
	if err != nil {
		return nil, err
	}
	klog.V(2).Infof("the existing packagemanifest is %+v for managedcluster %s", p, managedClusterName)
	desiredSubscription = createSubManifestwork(managedClusterName, p)

	updated, err := EnsureManifestWork(subscription, desiredSubscription)
	if err != nil {
		return nil, err
	}
	if updated {
		desiredSubscription.ObjectMeta.ResourceVersion = subscription.ObjectMeta.ResourceVersion
		subscription, err = workclient.ManifestWorks(managedClusterName).
			Update(ctx, desiredSubscription, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
	}
	return subscription, nil
}

func getExistingPackageManifestInfo(subManifest *workv1.ManifestWork) (*packagemanifest.PackageManifest, error) {
	for _, manifest := range subManifest.Spec.Workload.Manifests {
		if strings.Contains(string(manifest.RawExtension.Raw), `"kind":"Subscription"`) {
			sub := operatorv1alpha1.Subscription{}
			err := json.Unmarshal(manifest.RawExtension.Raw, &sub)
			if err != nil {
				return nil, err
			}
			return &packagemanifest.PackageManifest{
				DefaultChannel: sub.Spec.Channel,
				CurrentCSV:     sub.Spec.StartingCSV,
			}, nil
		}
	}
	return nil, nil
}

func ApplyMCHManifestWorks(ctx context.Context, workclient workclientv1.WorkV1Interface,
	workLister worklisterv1.ManifestWorkLister, managedClusterName string) error {
	userDefinedMCH := ""
	// Do not need to support customization so far
	// if managedCluster.Annotations != nil {
	// 	userDefinedMCH = managedCluster.Annotations["mch"]
	// }

	desiredMCH, err := createMCHManifestwork(managedClusterName, userDefinedMCH)
	if err != nil {
		return err
	}
	mch, err := workLister.ManifestWorks(managedClusterName).Get(managedClusterName + "-" + hohHubClusterMCH)
	if errors.IsNotFound(err) {
		klog.V(2).Infof("creating mch manifestwork in %s namespace", managedClusterName)
		_, err := workclient.ManifestWorks(managedClusterName).
			Create(ctx, desiredMCH, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}

	updated, err := EnsureManifestWork(mch, desiredMCH)
	if err != nil {
		return err
	}
	if updated {
		desiredMCH.ObjectMeta.ResourceVersion = mch.ObjectMeta.ResourceVersion
		_, err := workclient.ManifestWorks(managedClusterName).
			Update(ctx, desiredMCH, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// removePostponeDeleteAnnotationForManifestwork removes the postpone delete annotation for manifestwork so that
// the workagent can delete the manifestwork normally
func removePostponeDeleteAnnotationForSubManifestwork(ctx context.Context, workclient workclientv1.WorkV1Interface,
	workLister worklisterv1.ManifestWorkLister, managedClusterName string) error {
	_, err := workLister.ManifestWorks(managedClusterName).Get(managedClusterName + "-" + hohHubClusterMCH)
	if errors.IsNotFound(err) {
		subscription, err := workLister.ManifestWorks(managedClusterName).Get(managedClusterName + "-" + hohHubClusterSubscription)
		if err != nil {
			return err
		}
		if subscription.GetAnnotations() != nil {
			delete(subscription.GetAnnotations(), workPostponeDeleteAnnoKey)
			_, err = workclient.ManifestWorks(managedClusterName).
				Update(ctx, subscription, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
