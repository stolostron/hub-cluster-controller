package cluster

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"text/template"

	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	yamlserializer "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	workclientv1 "open-cluster-management.io/api/client/work/clientset/versioned/typed/work/v1"
	worklisterv1 "open-cluster-management.io/api/client/work/listers/work/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	"github.com/stolostron/hub-cluster-controller/pkg/packagemanifest"
)

//go:embed manifests/hypershift
var manifestFS embed.FS

const (
	hohHubClusterSubscription     = "hoh-hub-cluster-subscription"
	hohHubClusterMCH              = "hoh-hub-cluster-mch"
	workPostponeDeleteAnnoKey     = "open-cluster-management/postpone-delete"
	defaultInstallNamespace       = "open-cluster-management"
	openshiftMarketPlaceNamespace = "openshift-marketplace"
)

type ACMImageEntry struct {
	CertPolicyController              string
	IAMPolicyController               string
	ConfigPolicyController            string
	GovernancePolicyStatusSync        string
	GovernancePolicySpecSync          string
	GovernancePolicyTemplateSync      string
	GovernancePolicyAddonController   string
	GovernancePolicyPropagator        string
	MulticlusterOperatorsChannel      string
	MulticlusterOperatorsSubscription string
}

type MCEImageEntry struct {
	DefaultImageRegistry           string
	Registration                   string
	RegistrationOperator           string
	ManagedClusterImportController string
	Work                           string
	Placement                      string
}

type HypershiftConfigValues struct {
	HubVersion        string
	HoHAgentVersion   string
	HostedClusterName string
	ImagePullSecret   string
	ChannelClusterIP  string
	ACM               ACMImageEntry
	MCE               MCEImageEntry
}

var defaultHypershiftConfigValues = HypershiftConfigValues{
	ACM: ACMImageEntry{
		CertPolicyController:              "cert-policy-controller",
		IAMPolicyController:               "iam-policy-controller",
		ConfigPolicyController:            "config-policy-controller",
		GovernancePolicyStatusSync:        "governance-policy-status-sync",
		GovernancePolicySpecSync:          "governance-policy-spec-sync",
		GovernancePolicyTemplateSync:      "governance-policy-template-sync",
		GovernancePolicyAddonController:   "governance-policy-addon-controller",
		GovernancePolicyPropagator:        "governance-policy-propagator",
		MulticlusterOperatorsChannel:      "multicluster-operators-channel",
		MulticlusterOperatorsSubscription: "multicluster-operators-subscription",
	},
	MCE: MCEImageEntry{
		Registration:                   "registration",
		RegistrationOperator:           "registration-operator",
		ManagedClusterImportController: "managedcluster-import-controller",
		Work:                           "work",
		Placement:                      "placement",
	},
}

var decUnstructured = yamlserializer.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
var podNamespace, hohVersion, snapshot, mceSnapshot, imagePullSecretName string

func init() {
	// for test develop version
	snapshot, _ = os.LookupEnv("SNAPSHOT")
	mceSnapshot, _ = os.LookupEnv("MCE_SNAPSHOT")
	hohVersion, _ = os.LookupEnv("HUB_OF_HUBS_VERSION")
	if hohVersion == "" {
		hohVersion = "latest"
	}
	podNamespace, _ = os.LookupEnv("POD_NAMESPACE")
	imagePullSecretName, _ = os.LookupEnv("IMAGE_PULL_SECRET")
	if imagePullSecretName == "" {
		imagePullSecretName = "multiclusterhub-operator-pull-secret"
	}
	decUnstructured = yamlserializer.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
}

func getClusterRoleRaw() []byte {
	return []byte(`{
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
				"subscriptions",
				"catalogsources"
			],
			"verbs": [
				"create",
				"update"
			]
		}
	]}`)
}

func getClusterRoleBindingRaw() []byte {
	return []byte(`{
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
	]}`)
}

func getNamespaceRaw() []byte {
	return []byte(fmt.Sprintf(`{
	"apiVersion": "v1",
	"kind": "Namespace",
	"metadata": {
		"name": "%s"
	}}`, defaultInstallNamespace))
}

func getACMOperatorGroupRaw() []byte {
	return []byte(fmt.Sprintf(`{
		"apiVersion": "operators.coreos.com/v1",
		"kind": "OperatorGroup",
		"metadata": {
			"name": "open-cluster-management-group",
			"namespace": "%s"
		},
		"spec": {
			"targetNamespaces": [
				"%s"
			]
		}
	}`, defaultInstallNamespace, defaultInstallNamespace))
}

func getACMSubscription(channel, source, currentCSV string) []byte {
	return []byte(fmt.Sprintf(`{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind": "Subscription",
		"metadata": {
			"name": "acm-operator-subscription",
			"namespace": "%s"
		},
		"spec": {
			"channel": "%s",
			"installPlanApproval": "Automatic",
			"name": "advanced-cluster-management",
			"source": "%s",
			"sourceNamespace": "%s",
			"startingCSV": "%s"
		}
	}`, defaultInstallNamespace, channel, source, openshiftMarketPlaceNamespace, currentCSV))
}

func getACMCatalogSource(source, snapshot, imagePullSecretName string) []byte {
	return []byte(fmt.Sprintf(`{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind": "CatalogSource",
		"metadata": {
			"name": "%s",
			"namespace": "%s"
		},
		"spec": {
			"displayName": "Advanced Cluster Management",
			"image": "quay.io/stolostron/acm-custom-registry:%s",
			"secrets": [
			  "%s"
			],
			"publisher": "Red Hat",
			"sourceType": "grpc",
			"updateStrategy": {
			  "registryPoll": {
				"interval": "10m"
			  }
			}
		}
		}`, source, openshiftMarketPlaceNamespace, snapshot, imagePullSecretName))
}

func getMCECatalogSource(name, snapshot string) []byte {
	return []byte(fmt.Sprintf(`{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind": "CatalogSource",
		"metadata": {
			"name": "%s",
			"namespace": "%s"
		},
		"spec": {
			"displayName": "MultiCluster Engine",
			"image": "quay.io/stolostron/cmb-custom-registry:%s",
			"publisher": "Red Hat",
			"sourceType": "grpc",
			"updateStrategy": {
			  "registryPoll": {
				"interval": "10m"
			  }
			}
		}
		}`, "multiclusterengine-catalog", openshiftMarketPlaceNamespace, snapshot))
}

func createSubManifestwork(namespace string, p *packagemanifest.PackageManifestInfo, imagePullSecret *corev1.Secret) *workv1.ManifestWork {
	if p == nil || p.ACMCurrentCSV == "" || p.ACMDefaultChannel == "" {
		return nil
	}
	currentCSV := p.ACMCurrentCSV
	channel := p.ACMDefaultChannel
	source := "redhat-operators"
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
						Raw: getClusterRoleRaw(),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: getClusterRoleBindingRaw(),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: getNamespaceRaw(),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: getACMOperatorGroupRaw(),
					}},
					{RawExtension: runtime.RawExtension{
						Raw: getACMSubscription(channel, source, currentCSV),
					}},
				},
			},
			DeleteOption: &workv1.DeleteOption{
				PropagationPolicy: workv1.DeletePropagationPolicyTypeOrphan,
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Group:     "operators.coreos.com",
						Resource:  "subscriptions",
						Name:      "acm-operator-subscription",
						Namespace: defaultInstallNamespace,
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
			[]workv1.Manifest{
				{RawExtension: runtime.RawExtension{
					Raw: getACMCatalogSource(source, snapshot, imagePullSecretName),
				}},
				{RawExtension: runtime.RawExtension{
					Raw: getMCECatalogSource("multiclusterengine-catalog", snapshot),
				}},
			}...)
	}

	if imagePullSecret != nil {
		manifestwork.Spec.Workload.Manifests = append(manifestwork.Spec.Workload.Manifests,
			workv1.Manifest{
				RawExtension: runtime.RawExtension{
					Object: imagePullSecret,
				},
			})
	}

	return manifestwork
}

func getDefaultMCH() string {
	if snapshot != "" {
		return fmt.Sprintf(`{
			"apiVersion": "operator.open-cluster-management.io/v1",
			"kind": "MultiClusterHub",
			"metadata": {
				"name": "multiclusterhub",
				"namespace": "%s",
				"annotations": {
					"installer.open-cluster-management.io/mce-subscription-spec": "{\"channel\": \"stable-2.0\",\"installPlanApproval\": \"Automatic\",\"name\": \"multicluster-engine\",\"source\": \"multiclusterengine-catalog\",\"sourceNamespace\": \"openshift-marketplace\"}"
				}
			},
			"spec": {
				"disableHubSelfManagement": true,
				"imagePullSecret": "%s"
			}
		}`, defaultInstallNamespace, imagePullSecretName)
	}
	return fmt.Sprintf(`{
		"apiVersion": "operator.open-cluster-management.io/v1",
		"kind": "MultiClusterHub",
		"metadata": {
			"name": "multiclusterhub",
			"namespace": "%s"
		},
		"spec": {
			"disableHubSelfManagement": true
		}
	}`, defaultInstallNamespace)
}

func createMCHManifestwork(namespace, userDefinedMCH string, imagePullSecret *corev1.Secret) (*workv1.ManifestWork, error) {

	mchJson := getDefaultMCH()
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
	manifestwork := &workv1.ManifestWork{
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
			DeleteOption: &workv1.DeleteOption{
				PropagationPolicy: workv1.DeletePropagationPolicyTypeOrphan,
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Group:     "operator.open-cluster-management.io",
						Resource:  "multiclusterhubs",
						Name:      "multiclusterhub",
						Namespace: defaultInstallNamespace,
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
	}

	if imagePullSecret != nil {
		manifestwork.Spec.Workload.Manifests = append(manifestwork.Spec.Workload.Manifests,
			workv1.Manifest{
				RawExtension: runtime.RawExtension{
					Object: imagePullSecret,
				},
			})
	}

	return manifestwork, nil
}

func EnsureManifestWork(existing, desired *workv1.ManifestWork) (bool, error) {
	if !equality.Semantic.DeepDerivative(desired.Spec.DeleteOption, existing.Spec.DeleteOption) {
		return true, nil
	}

	if !equality.Semantic.DeepDerivative(desired.Spec.ManifestConfigs, existing.Spec.ManifestConfigs) {
		return true, nil
	}

	if len(existing.Spec.Workload.Manifests) != len(desired.Spec.Workload.Manifests) {
		return true, nil
	}

	for i, m := range existing.Spec.Workload.Manifests {
		var existingObj, desiredObj interface{}
		if len(m.RawExtension.Raw) > 0 {
			if err := json.Unmarshal(m.RawExtension.Raw, &existingObj); err != nil {
				return false, err
			}
		} else {
			existingObj = m.RawExtension.Object
		}

		if len(desired.Spec.Workload.Manifests[i].RawExtension.Raw) > 0 {
			if err := json.Unmarshal(desired.Spec.Workload.Manifests[i].RawExtension.Raw, &desiredObj); err != nil {
				return false, err
			}
		} else {
			desiredObjBytes, err := json.Marshal(desired.Spec.Workload.Manifests[i].RawExtension.Object)
			if err != nil {
				return false, err
			}
			if err := json.Unmarshal(desiredObjBytes, &desiredObj); err != nil {
				return false, err
			}
		}

		metadata := existingObj.(map[string]interface{})["metadata"].(map[string]interface{})
		metadata["creationTimestamp"] = nil
		if !equality.Semantic.DeepDerivative(desiredObj, existingObj) {
			klog.V(2).Infof("existing manifest object %d is not equal to the desired manifest object", i)
			return true, nil
		}
	}

	return false, nil
}

// generatePullSecret generates the image pull secret for mco
func generatePullSecret(kubeClient *kubernetes.Clientset, namespace string) (*corev1.Secret, error) {
	if imagePullSecretName != "" {
		imagePullSecret, err := kubeClient.CoreV1().Secrets(podNamespace).Get(context.TODO(),
			imagePullSecretName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Secret",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      imagePullSecret.Name,
				Namespace: namespace,
			},
			Data: map[string][]byte{
				".dockerconfigjson": imagePullSecret.Data[".dockerconfigjson"],
			},
			Type: corev1.SecretTypeDockerConfigJson,
		}, nil
	}
	return nil, nil
}

func ApplySubManifestWorks(ctx context.Context, kubeClient *kubernetes.Clientset, workclient workclientv1.WorkV1Interface,
	workLister worklisterv1.ManifestWorkLister, managedClusterName string) (*workv1.ManifestWork, error) {

	// Get image pull secret for pre-release testing
	// search components and catalogsource image need have pull secret
	imagePullSecret, err := generatePullSecret(kubeClient, openshiftMarketPlaceNamespace)
	if err != nil {
		return nil, err
	}

	desiredSubscription := createSubManifestwork(managedClusterName, packagemanifest.GetPackageManifest(), imagePullSecret)
	if desiredSubscription == nil {
		return nil, nil
	}

	subscription, err := workLister.ManifestWorks(managedClusterName).Get(managedClusterName + "-" + hohHubClusterSubscription)
	if errors.IsNotFound(err) {
		klog.V(2).Infof("creating subscription manifestwork in %s namespace", managedClusterName)
		createdSubscription, err := workclient.ManifestWorks(managedClusterName).
			Create(ctx, desiredSubscription, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
		return createdSubscription, nil
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
	desiredSubscription = createSubManifestwork(managedClusterName, p, imagePullSecret)

	updated, err := EnsureManifestWork(subscription, desiredSubscription)
	if err != nil {
		return nil, err
	}
	if updated {
		klog.V(2).Infof("updating the manifestwork: %s/%s because it's changed", subscription.GetNamespace(), subscription.GetName())
		desiredSubscription.ObjectMeta.ResourceVersion = subscription.ObjectMeta.ResourceVersion
		subscription, err = workclient.ManifestWorks(managedClusterName).
			Update(ctx, desiredSubscription, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
	}

	return subscription, nil
}

func getExistingPackageManifestInfo(subManifest *workv1.ManifestWork) (*packagemanifest.PackageManifestInfo, error) {
	for _, manifest := range subManifest.Spec.Workload.Manifests {
		if strings.Contains(string(manifest.RawExtension.Raw), `"kind":"Subscription"`) {
			sub := operatorv1alpha1.Subscription{}
			err := json.Unmarshal(manifest.RawExtension.Raw, &sub)
			if err != nil {
				return nil, err
			}
			return &packagemanifest.PackageManifestInfo{
				ACMDefaultChannel: sub.Spec.Channel,
				ACMCurrentCSV:     sub.Spec.StartingCSV,
			}, nil
		}
	}
	return nil, nil
}

func ApplyMCHManifestWorks(ctx context.Context, kubeClient *kubernetes.Clientset, workclient workclientv1.WorkV1Interface,
	workLister worklisterv1.ManifestWorkLister, managedClusterName string) error {
	userDefinedMCH := ""
	// Do not need to support customization so far
	// if managedCluster.Annotations != nil {
	// 	userDefinedMCH = managedCluster.Annotations["mch"]
	// }
	// Get image pull secret for pre-release testing
	// search components and catalogsource image need have pull secret
	// Get image pull secret for pre-release testing
	// search components and catalogsource image need have pull secret
	imagePullSecret, err := generatePullSecret(kubeClient, defaultInstallNamespace)
	if err != nil {
		return err
	}

	desiredMCH, err := createMCHManifestwork(managedClusterName, userDefinedMCH, imagePullSecret)
	if err != nil {
		return err
	}

	_, err = applyManifestWork(ctx, workclient, workLister, desiredMCH)
	return err
}

// applyManifestWork creates or updates a single manifestwork resource
func applyManifestWork(ctx context.Context, workclient workclientv1.WorkV1Interface, workLister worklisterv1.ManifestWorkLister,
	manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	existingManifestWork, err := workLister.ManifestWorks(manifestWork.GetNamespace()).Get(manifestWork.GetName())
	if errors.IsNotFound(err) {
		klog.V(2).Infof("creating manifestwork %s in namespace %s", manifestWork.GetName(), manifestWork.GetNamespace())
		createdManifestwork, err := workclient.ManifestWorks(manifestWork.GetNamespace()).
			Create(ctx, manifestWork, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
		return createdManifestwork, nil
	}
	if err != nil {
		return nil, err
	}

	updated, err := EnsureManifestWork(existingManifestWork, manifestWork)
	if err != nil {
		return nil, err
	}

	if updated {
		klog.V(2).Infof("updating manifestwork: %s/%s because it's changed", manifestWork.GetNamespace(), manifestWork.GetName())
		manifestWork.ObjectMeta.ResourceVersion = existingManifestWork.ObjectMeta.ResourceVersion
		updatedManifestwork, err := workclient.ManifestWorks(manifestWork.GetNamespace()).
			Update(ctx, manifestWork, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
		return updatedManifestwork, nil
	}

	return existingManifestWork, nil
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

func ApplyHubManifestWorks(ctx context.Context, kubeClient *kubernetes.Clientset, workclient workclientv1.WorkV1Interface, workLister worklisterv1.ManifestWorkLister,
	managedClusterName, hostingClusterName, hostingNamespace, hostedClusterName, channelClusterIP string) (*workv1.ManifestWork, error) {
	p := packagemanifest.GetPackageManifest()
	if p == nil || len(p.ACMImages) == 0 || len(p.MCEImages) == 0 || p.ACMCurrentCSV == "" || p.ACMDefaultChannel == "" {
		return nil, nil
	}

	acmImages := p.ACMImages
	mceImages := p.MCEImages

	acmDefaultImageRegistry := "quay.io/stolostron"
	mceDefaultImageRegistry := "quay.io/stolostron"
	if snapshot == "" {
		acmDefaultImageRegistry = "registry.redhat.io/rhacm2"
		// handle special case for governance-policy-addon-controller image
		defaultHypershiftConfigValues.ACM.GovernancePolicyAddonController = "acm-governance-policy-addon-controller"
	}
	if mceSnapshot == "" {
		mceDefaultImageRegistry = "registry.redhat.io/multicluster-engine"
	}

	tpl, err := parseTemplates(manifestFS, snapshot, mceSnapshot, acmDefaultImageRegistry, mceDefaultImageRegistry, acmImages, mceImages)
	if err != nil {
		return nil, err
	}

	hypershiftHostedClusterName := hostingNamespace + "-" + hostedClusterName
	latestACMVersion := strings.TrimPrefix(p.ACMCurrentCSV, "advanced-cluster-management.v")
	latestACMVersionParts := strings.Split(latestACMVersion, ".")
	if len(latestACMVersionParts) < 2 {
		return nil, fmt.Errorf("invalid ACM version :%s", latestACMVersion)
	}
	latestACMVersionM := strings.Join(latestACMVersionParts[:2], ".")
	defaultHypershiftConfigValues.HubVersion = latestACMVersionM
	defaultHypershiftConfigValues.HoHAgentVersion = hohVersion
	defaultHypershiftConfigValues.HostedClusterName = hypershiftHostedClusterName
	defaultHypershiftConfigValues.ImagePullSecret = imagePullSecretName
	defaultHypershiftConfigValues.MCE.DefaultImageRegistry = mceDefaultImageRegistry

	if channelClusterIP != "" {
		defaultHypershiftConfigValues.ChannelClusterIP = channelClusterIP
	}

	// apply manifestwork on hypershift hosted cluster
	var buf bytes.Buffer
	tpl.ExecuteTemplate(&buf, "manifests/hypershift/hosted", defaultHypershiftConfigValues)
	// klog.V(2).Infof("templates for objects on hosted cluster: %s", buf.String())

	hostedManifests := []workv1.Manifest{}
	yamlReader := yaml.NewYAMLReader(bufio.NewReader(&buf))
	for {
		b, err := yamlReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(b) != 0 {
			rawJSON, err := yaml.ToJSON(b)
			if err != nil {
				return nil, err
			}
			if string(rawJSON) != "null" {
				// klog.V(2).Infof("raw JSON for object on hosted cluster:\n%s\n", rawJSON)
				hostedManifests = append(hostedManifests, workv1.Manifest{RawExtension: runtime.RawExtension{Raw: rawJSON}})
			}
		}
	}

	imagePullSecret, err := generatePullSecret(kubeClient, hypershiftHostedClusterName)
	if err != nil {
		return nil, err
	}
	if imagePullSecret != nil {
		hostedManifests = append(hostedManifests, workv1.Manifest{RawExtension: runtime.RawExtension{Object: imagePullSecret}})
	}

	hostedManifestwork := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedClusterName + "-hoh-hub-cluster-hosted",
			Namespace: managedClusterName,
			Labels: map[string]string{
				"hub-of-hubs.open-cluster-management.io/managed-by": "hoh",
			},
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: hostedManifests,
			},
			DeleteOption: &workv1.DeleteOption{
				PropagationPolicy: workv1.DeletePropagationPolicyTypeOrphan,
			},
		},
	}

	// klog.V(2).Infof("manifestwork on hosted cluster: %+v", hostedManifests)
	if _, err := applyManifestWork(ctx, workclient, workLister, hostedManifestwork); err != nil {
		return nil, err
	}

	// manifestwork on hypershift management cluster
	buf.Reset()
	tpl.ExecuteTemplate(&buf, "manifests/hypershift/management", defaultHypershiftConfigValues)
	// klog.V(2).Infof("templates for objects on management cluster: %s", buf.String())

	managementManifests := []workv1.Manifest{}
	// yamlReader := yaml.NewYAMLReader(bufio.NewReader(&buf))
	for {
		b, err := yamlReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(b) != 0 {
			rawJSON, err := yaml.ToJSON(b)
			if err != nil {
				return nil, err
			}
			if string(rawJSON) != "null" {
				// klog.V(2).Infof("raw JSON for object on management cluster:\n%s\n", rawJSON)
				managementManifests = append(managementManifests, workv1.Manifest{RawExtension: runtime.RawExtension{Raw: rawJSON}})
			}
		}
	}

	if imagePullSecret != nil {
		managementManifests = append(managementManifests, workv1.Manifest{RawExtension: runtime.RawExtension{Object: imagePullSecret}})
	}

	managementManifestwork := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedClusterName + "-hoh-hub-cluster-management",
			Namespace: hostingClusterName,
			Labels: map[string]string{
				"hub-of-hubs.open-cluster-management.io/managed-by": "hoh",
			},
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: managementManifests,
			},
			DeleteOption: &workv1.DeleteOption{
				PropagationPolicy: workv1.DeletePropagationPolicyTypeOrphan,
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Group:     "",
						Resource:  "services",
						Name:      "channels-apps-open-cluster-management-webhook-svc",
						Namespace: hypershiftHostedClusterName,
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
	}

	return applyManifestWork(ctx, workclient, workLister, managementManifestwork)
}

func removeHubManifestworkFromHyperMgtCluster(ctx context.Context, workclient workclientv1.WorkV1Interface,
	managedClusterName, hostingClusterName string) error {
	return workclient.ManifestWorks(hostingClusterName).Delete(ctx, managedClusterName+"-hoh-hub-cluster-management", metav1.DeleteOptions{})
}

func parseTemplates(manifestFS embed.FS, snapshot, mceSnapshot, acmDefaultImageRegistry, mceDefaultImageRegistry string, acmImages, mceImages map[string]string) (*template.Template, error) {
	tf := template.FuncMap{
		"getACMImage": func(imageKey string) string {
			if snapshot != "" {
				return acmDefaultImageRegistry + "/" + imageKey + ":" + snapshot
			} else {
				return acmImages[imageKey]
			}
		},
		"getMCEImage": func(imageKey string) string {
			if mceSnapshot != "" {
				return mceDefaultImageRegistry + "/" + imageKey + ":" + mceSnapshot
			} else {
				return mceImages[imageKey]
			}
		},
	}

	tpl := template.New("")
	err := fs.WalkDir(manifestFS, ".", func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasSuffix(file, "hosted") || strings.HasSuffix(file, "management")) {
			manifests, err := readFileInDir(manifestFS, file)
			if err != nil {
				return err
			}

			t := tpl.New(file).Funcs(tf)
			_, err = t.Parse(manifests)
			if err != nil {
				return err
			}
		}

		return nil
	})

	return tpl, err
}

func readFileInDir(manifestFS embed.FS, dir string) (string, error) {
	var res string
	err := fs.WalkDir(manifestFS, dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			b, err := manifestFS.ReadFile(file)
			if err != nil {
				return err
			}
			res += string(b) + "\n---\n"
		}
		return nil
	})

	return res, err
}

func getHohRepoHost(ctx context.Context, dynamicClient dynamic.Interface) (string, error) {
	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}
	hoHRepoRoute, err := dynamicClient.Resource(routeGVR).Namespace(podNamespace).
		Get(ctx, "hub-of-hubs-repo", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	routeStatus := hoHRepoRoute.Object["status"].(map[string]interface{})
	routeIngress := routeStatus["ingress"].([]interface{})
	serverHost := routeIngress[0].(map[string]interface{})["host"].(string)
	klog.V(2).Infof("hub-of-hubs-repo server is %s", serverHost)
	return serverHost, nil
}

func applyDynamicResource(ctx context.Context, dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, desired *unstructured.Unstructured) error {
	existingObj, err := dynamicClient.Resource(gvr).Namespace(desired.GetNamespace()).Get(ctx, desired.GetName(), metav1.GetOptions{})
	if errors.IsNotFound(err) {
		klog.V(2).Infof("creating unstructured object %s in namespace %s", desired.GetName(), desired.GetNamespace())
		_, err := dynamicClient.Resource(gvr).Namespace(desired.GetNamespace()).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}

	toUpdate, modified, err := ensureGenericSpec(existingObj, desired)
	if err != nil {
		return err
	}

	if !modified {
		return nil
	}

	klog.V(2).Infof("desired unstructured object %s/%s is changed, updating it...", desired.GetNamespace(), desired.GetName())
	_, err = dynamicClient.Resource(gvr).Namespace(toUpdate.GetNamespace()).Update(ctx, toUpdate, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func ensureGenericSpec(existing, desired *unstructured.Unstructured) (*unstructured.Unstructured, bool, error) {
	desiredCopy := desired.DeepCopy()
	desiredSpec, _, err := unstructured.NestedMap(desiredCopy.UnstructuredContent(), "spec")
	if err != nil {
		return nil, false, err
	}
	existingSpec, _, err := unstructured.NestedMap(existing.UnstructuredContent(), "spec")
	if err != nil {
		return nil, false, err
	}

	if equality.Semantic.DeepEqual(existingSpec, desiredSpec) {
		return existing, false, nil
	}

	existingCopy := existing.DeepCopy()
	if err := unstructured.SetNestedMap(existingCopy.UnstructuredContent(), desiredSpec, "spec"); err != nil {
		return nil, true, err
	}

	return existingCopy, true, nil
}
