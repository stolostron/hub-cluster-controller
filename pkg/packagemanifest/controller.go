package packagemanifest

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// packageManifestController reconciles packagemanifests.packages.operators.coreos.com.
type packageManifestController struct {
	cache         resourceapply.ResourceCache
	dynamicClient dynamic.Interface
	eventRecorder events.Recorder
}

type packageManifest struct {
}

// NewPackageManifestController creates a new package controller
func NewPackageManifestController(
	dynamicClient dynamic.Interface,
	packageGenericInformer informers.GenericInformer,
	recorder events.Recorder) factory.Controller {
	c := &packageManifestController{
		dynamicClient: dynamicClient,
		cache:         resourceapply.NewResourceCache(),
		eventRecorder: recorder.WithComponentSuffix("package-manifest-controller"),
	}
	return factory.New().
		WithFilteredEventsInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				key, _ := cache.MetaNamespaceKeyFunc(obj)
				return key
			},
			func(obj interface{}) bool {
				key, err := cache.MetaNamespaceKeyFunc(obj)
				if err != nil {
					return false
				}
				_, name, err := cache.SplitMetaNamespaceKey(key)
				if err != nil {
					// ignore addon whose key is not in format: namespace/name
					return false
				}
				// only enqueue when the name is advanced-cluster-management
				if name == "advanced-cluster-management" || name == "multicluster-engine" {
					return true
				}
				return false
			}, packageGenericInformer.Informer()).
		WithSync(c.sync).
		ToController("PackageManifestController", recorder)
}

func (c *packageManifestController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	key := syncCtx.QueueKey()
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		// ignore addon whose key is not in format: namespace/name
		return err
	}
	klog.V(2).Infof("Reconciling for packagemanifest %s in namespace %s", name, namespace)

	obj, err := c.dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "packages.operators.coreos.com",
		Version:  "v1",
		Resource: "packagemanifests"}).
		Namespace(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	statusObj := obj.Object["status"].(map[string]interface{})

	if statusObj["catalogSource"].(string) != "redhat-operators" {
		return nil
	}

	if name == "advanced-cluster-management" {
		defaultChannel := statusObj["defaultChannel"].(string)
		klog.V(2).Infof("ACM defaultChannel is %s", defaultChannel)

		currentCSV := ""
		channels := statusObj["channels"].([]interface{})

		ACMImages := map[string]string{}
		for _, channel := range channels {
			if channel.(map[string]interface{})["name"].(string) == defaultChannel {
				currentCSV = channel.(map[string]interface{})["currentCSV"].(string)
				// retrieve the acm related images
				acmRelatedImages := channel.(map[string]interface{})["currentCSVDesc"].(map[string]interface{})["relatedImages"].([]interface{})
				for _, img := range acmRelatedImages {
					imgStr := img.(string)
					var imgStrs []string
					if strings.Contains(imgStr, "@") {
						imgStrs = strings.Split(imgStr, "@")
					} else if strings.Contains(imgStr, ":") {
						imgStrs = strings.Split(imgStr, ":")
					} else {
						return fmt.Errorf("invalid image format: %s in packagemanifest", imgStr)
					}
					if len(imgStrs) != 2 {
						return fmt.Errorf("invalid image format: %s in packagemanifest", imgStr)
					}
					imgNameStrs := strings.Split(imgStrs[0], "/")
					imageName := imgNameStrs[len(imgNameStrs)-1]
					// words := strings.Split(imageName, "-")
					// for i, w := range words {
					// 	words[i] = strings.Title(w)
					// }
					// imageKey := strings.Join(words, "")
					imageKey := strings.TrimSuffix(imageName, "-rhel8")
					ACMImages[imageKey] = imgStr
				}
				break
			}
		}
		klog.V(2).Infof("ACM currentCSV is %s", currentCSV)
		//klog.V(2).Infof("ACM images %v", ACMImages)

		acmPackageManifestInfo.ACMDefaultChannel = defaultChannel
		acmPackageManifestInfo.ACMCurrentCSV = currentCSV
		acmPackageManifestInfo.ACMImages = ACMImages
	}

	if name == "multicluster-engine" {
		defaultChannel := statusObj["defaultChannel"].(string)
		klog.V(2).Infof("MCE defaultChannel is %s", defaultChannel)

		currentCSV := ""
		channels := statusObj["channels"].([]interface{})

		MCEImages := map[string]string{}
		for _, channel := range channels {
			if channel.(map[string]interface{})["name"].(string) == defaultChannel {
				currentCSV = channel.(map[string]interface{})["currentCSV"].(string)
				// retrieve the mce related images
				mceRelatedImages := channel.(map[string]interface{})["currentCSVDesc"].(map[string]interface{})["relatedImages"].([]interface{})
				for _, img := range mceRelatedImages {
					imgStr := img.(string)
					var imgStrs []string
					if strings.Contains(imgStr, "@") {
						imgStrs = strings.Split(imgStr, "@")
					} else if strings.Contains(imgStr, ":") {
						imgStrs = strings.Split(imgStr, ":")
					} else {
						return fmt.Errorf("invalid image format: %s in packagemanifest", imgStr)
					}
					if len(imgStrs) != 2 {
						return fmt.Errorf("invalid image format: %s in packagemanifest", imgStr)
					}
					imgNameStrs := strings.Split(imgStrs[0], "/")
					imageName := imgNameStrs[len(imgNameStrs)-1]
					// words := strings.Split(imageName, "-")
					// for i, w := range words {
					// 	words[i] = strings.Title(w)
					// }
					// imageKey := strings.Join(words, "")
					imageKey := strings.TrimSuffix(imageName, "-rhel8")
					MCEImages[imageKey] = imgStr
				}
				break
			}
		}
		klog.V(2).Infof("MCE currentCSV is %s", currentCSV)
		//klog.V(2).Infof("MCE images %v", MCEImages)

		acmPackageManifestInfo.MCEDefaultChannel = defaultChannel
		acmPackageManifestInfo.MCECurrentCSV = currentCSV
		acmPackageManifestInfo.MCEImages = MCEImages
	}

	return nil
}
