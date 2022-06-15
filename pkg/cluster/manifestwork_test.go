package cluster

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	workv1 "open-cluster-management.io/api/work/v1"
)

var testConfigMapStr = `{
	"apiVersion": "v1",
	"kind": "ConfigMap",
	"metadata": {
	  "name": "foo",
	  "namespace": "test"
	},
	"data": {
	  "key1": "value1",
	  "key2": "value2"
	}
  }`

var testConfigMapObj = &corev1.ConfigMap{
	TypeMeta: metav1.TypeMeta{
		Kind:       "ConfigMap",
		APIVersion: "v1",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name:      "foo",
		Namespace: "test",
	},
	Data: map[string]string{"key1": "value1", "key2": "value2"},
}

func TestCreateMCHManifestwork(t *testing.T) {
	mch, _ := createMCHManifestwork("test", `{
		"apiVersion": "operator.open-cluster-management.io/v1",
		"kind": "MultiClusterHub",
		"metadata": {
			"name": "multiclusterhub",
			"namespace":"open-cluster-management"
		},
		"spec": {
			"imagePullSecret": "multiclusterhub-operator-pull-secret"
		}
	}`, nil)
	if mch.GetName() != "test-"+hohHubClusterMCH {
		t.Fatalf("failed to find the %s manifestwork", "test-"+hohHubClusterMCH)
	}
	mchByte, err := json.Marshal(mch)
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !strings.Contains(string(mchByte), "disableHubSelfManagement") {
		t.Fatalf("failed to find disableHubSelfManagement")
	}
}

func TestEnsureManifestWork(t *testing.T) {
	tests := []struct {
		desc                 string
		existingManifestwork *workv1.ManifestWork
		desiredManifestwork  *workv1.ManifestWork
		wantChanged          bool
		wantErr              string
	}{
		{
			desc: "DifferentDeletePropagationPolicy",
			existingManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					DeleteOption: &workv1.DeleteOption{
						PropagationPolicy: workv1.DeletePropagationPolicyTypeOrphan,
					},
				},
			},
			desiredManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					DeleteOption: &workv1.DeleteOption{
						PropagationPolicy: workv1.DeletePropagationPolicyTypeForeground,
					},
				},
			},
			wantChanged: true,
			wantErr:     "",
		},
		{
			desc: "DifferentManifestConfigs",
			existingManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					ManifestConfigs: []workv1.ManifestConfigOption{
						{
							ResourceIdentifier: workv1.ResourceIdentifier{
								Group:     "",
								Resource:  "services",
								Name:      "foo",
								Namespace: "test",
							},
							FeedbackRules: []workv1.FeedbackRule{
								{
									Type: workv1.JSONPathsType,
									JsonPaths: []workv1.JsonPath{
										{
											Name: "clusterIP",
											Path: ".spec.clusterIP",
										},
									},
								},
							},
						},
					},
				},
			},
			desiredManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					ManifestConfigs: []workv1.ManifestConfigOption{
						{
							ResourceIdentifier: workv1.ResourceIdentifier{
								Group:     "",
								Resource:  "services",
								Name:      "bar",
								Namespace: "test",
							},
							FeedbackRules: []workv1.FeedbackRule{
								{
									Type: workv1.JSONPathsType,
									JsonPaths: []workv1.JsonPath{
										{
											Name: "clusterIP",
											Path: ".spec.clusterIP",
										},
									},
								},
							},
						},
					},
				},
			},
			wantChanged: true,
			wantErr:     "",
		},
		{
			desc: "DifferentManifestsNumber",
			existingManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Raw: []byte(testConfigMapStr),
							}},
						},
					},
				},
			},
			desiredManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Raw: []byte(testConfigMapStr),
							}},
							{RawExtension: runtime.RawExtension{
								Raw: []byte(testConfigMapStr),
							}},
						},
					},
				},
			},
			wantChanged: true,
			wantErr:     "",
		},
		{
			desc: "SameManifestwork",
			existingManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Raw: []byte(testConfigMapStr),
							}},
						},
					},
				},
			},
			desiredManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Raw: []byte(testConfigMapStr),
							}},
						},
					},
				},
			},
			wantChanged: false,
			wantErr:     "",
		},
		{
			desc: "SameManifestworkWithDifferentManifestFormat",
			existingManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Raw: []byte(testConfigMapStr),
							}},
						},
					},
				},
			},
			desiredManifestwork: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{
								Object: testConfigMapObj,
							}},
						},
					},
				},
			},
			wantChanged: false,
			wantErr:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotChanged, gotErr := EnsureManifestWork(tt.existingManifestwork, tt.desiredManifestwork)
			if gotErr != nil {
				if gotErr.Error() != tt.wantErr {
					t.Fatalf("EnsureManifestWork(%s): gotErr:%v, wanrErr:%v", tt.desc, gotErr, tt.wantErr)
				}
			} else {
				if tt.wantErr != "" {
					t.Fatalf("EnsureManifestWork(%s): gotErr:%v, wanrErr:%v", tt.desc, gotErr, tt.wantErr)
				}
			}
			if gotChanged != tt.wantChanged {
				t.Fatalf("EnsureManifestWork(%s): gotChanged:%v, wantChanged:%v", tt.desc, gotChanged, tt.wantChanged)
			}
		})
	}
}
