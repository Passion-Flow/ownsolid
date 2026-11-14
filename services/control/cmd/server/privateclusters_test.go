package main

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestPrivateClusterCompositionOmissionAndInvalidOwner(t *testing.T) {
	c := config.Config{}
	if h, e := openPrivateClusters(context.Background(), c, nil, nil); e != nil || h != nil {
		t.Fatal("omission enabled a module", e)
	}
	for _, path := range []string{"relative", "/private/../changed.json"} {
		c.PrivateClusterConfigFile = path
		if h, e := openPrivateClusters(context.Background(), c, nil, nil); e == nil || h != nil {
			t.Fatal("invalid path admitted")
		}
	}
	c.PrivateClusterConfigFile = "/private/cluster/configuration.json"
	if h, e := openPrivateClusters(context.Background(), c, nil, nil); e == nil || h != nil || e.Error() != "PRIVATE_CLUSTER_OWNER_REQUIRED" {
		t.Fatal("missing owners admitted", e)
	}
	if e := validateUnbootstrappedFeatures(c); e == nil {
		t.Fatal("unbootstrapped private cluster accepted")
	}
}
