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
