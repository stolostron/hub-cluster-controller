package cluster

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

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

func CreateMCHManifestwork(namespace string) *workv1.ManifestWork {
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespace + "-" + HOH_HUB_CLUSTER_MCH,
			Namespace: namespace,
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{RawExtension: runtime.RawExtension{
						Raw: []byte(`{
	"apiVersion": "operator.open-cluster-management.io/v1",
	"kind": "MultiClusterHub",
	"metadata": {
		"name": "multiclusterhub",
		"namespace":"open-cluster-management"
	},
	"spec": {
		"disableHubSelfManagement": true
	}
}`),
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
					},
				},
			},
		},
	}
}
