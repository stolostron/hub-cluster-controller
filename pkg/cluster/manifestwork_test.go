package cluster

import (
	"testing"
)

func TestCreateSubManifestwork(t *testing.T) {
	sub := CreateSubManifestwork("test")
	if sub.GetName() != "test-"+HOH_HUB_CLUSTER_SUBSCRIPTION {
		t.Fatalf("error")
	}
}
