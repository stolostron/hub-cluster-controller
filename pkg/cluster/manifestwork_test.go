package cluster

import (
	"encoding/json"
	"strings"
	"testing"
)

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
	}`)
	if mch.GetName() != "test-"+HOH_HUB_CLUSTER_MCH {
		t.Fatalf("failed to find the %s manifestwork", "test-"+HOH_HUB_CLUSTER_MCH)
	}
	mchByte, err := json.Marshal(mch)
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !strings.Contains(string(mchByte), "disableHubSelfManagement") {
		t.Fatalf("failed to find disableHubSelfManagement")
	}
}
