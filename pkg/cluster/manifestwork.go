package cluster

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	workv1 "open-cluster-management.io/api/work/v1"
)

const (
	HOH_HUB_CLUSTER_SUBSCRIPTION = "hoh-hub-cluster-subscription"
	HOH_HUB_CLUSTER_MCH          = "hoh-hub-cluster-mch"
)

func CreateSubManifestwork(namespace string) *workv1.ManifestWork {
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespace + "-" + HOH_HUB_CLUSTER_SUBSCRIPTION,
			Namespace: namespace,
			Labels: map[string]string{
				"hub-of-hubs.open-cluster-management.io/managed-by": "hoh",
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
						Raw: []byte(`{
	"apiVersion": "operators.coreos.com/v1alpha1",
	"kind": "Subscription",
	"metadata": {
		"name": "acm-operator-subscription",
		"namespace": "open-cluster-management"
	},
	"spec": {
		"channel": "release-2.4",
		"installPlanApproval": "Automatic",
		"name": "advanced-cluster-management",
		"source": "redhat-operators",
		"sourceNamespace": "openshift-marketplace",
		"startingCSV": "advanced-cluster-management.v2.4.1"
	}
}`),
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
}

func CreateMCHManifestwork(namespace, userDefinedMCH string) (*workv1.ManifestWork, error) {
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
			Name:      namespace + "-" + HOH_HUB_CLUSTER_MCH,
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
		klog.V(2).Infof("the existing manifestwork is %s", string(existingBytes))
		klog.V(2).Infof("the desired manifestwork is %s", string(desiredBytes))
		return true, nil
	}
	return false, nil
}
