package cluster

import (
	"context"
	"encoding/json"

	appv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

func createChannelManagedClusterView(namespace string) *unstructured.Unstructured {

	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "view.open-cluster-management.io/v1beta1",
		"kind":       "ManagedClusterView",
		"name":       "getdeployment",
		"namespace":  namespace,
		"spec": map[string]interface{}{
			"scope": map[string]interface{}{
				"name":      "multicluster-operators-channel",
				"namespace": "open-cluster-management",
				"resource":  "deployments",
			},
		},
	}}
}

func ApplyChannelManagedClusterView(ctx context.Context,
	dynamicClient dynamic.Interface, managedClusterName string) (*unstructured.Unstructured, error) {

	crResource := schema.GroupVersionResource{
		Group:    "view.open-cluster-management.io",
		Version:  "v1beta1",
		Resource: "managedclusterviews"}

	existingChannel, err := dynamicClient.Resource(crResource).
		Namespace(managedClusterName).
		Get(ctx, MULTICLUSTER_CHANNEL_NAME, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	desiredChannel := createChannelManagedClusterView(managedClusterName)
	if errors.IsNotFound(err) {
		klog.V(2).Infof("creating multicluster-operators-channel managedclusterviews in %s namespace", managedClusterName)
		_, err := dynamicClient.Resource(crResource).
			Create(ctx, desiredChannel, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}

	updated, err := ensureManagedClusterView(existingChannel, desiredChannel)
	if err != nil {
		return nil, err
	}
	if updated {
		existingChannel, err = dynamicClient.Resource(crResource).
			Update(ctx, desiredChannel, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
	}

	return existingChannel, nil
}

func ensureManagedClusterView(existing, desired *unstructured.Unstructured) (bool, error) {
	// compare the ManagedClusterView
	existingBytes, err := json.Marshal(existing.Object["spec"])
	if err != nil {
		return false, err
	}
	desiredBytes, err := json.Marshal(desired.Object["spec"])
	if err != nil {
		return false, err
	}
	if string(existingBytes) != string(desiredBytes) {
		return true, nil
	}
	return false, nil
}

func isChannelReady(channel *unstructured.Unstructured) bool {
	if channel == nil {
		return false
	}
	statusObj := channel.Object["status"]
	if statusObj == nil {
		return false
	}
	resultObj := statusObj.(map[string]interface{})["result"]
	if resultObj == nil {
		return false
	}

	deploy := resultObj.(*appv1.Deployment)
	if deploy.Status.ReadyReplicas == deploy.Status.Replicas {
		return true
	}
	return false
}
