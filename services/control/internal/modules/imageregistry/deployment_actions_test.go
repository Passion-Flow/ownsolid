package imageregistry

import (
	"context"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
)

func TestRegistryActionOwnersRequireBorrowedAuthority(t *testing.T) {
	for _, s := range []*Service{nil, {}} {
		if _, e := s.PrepareActionWithin(context.Background(), nil, identity.Auth{}, nodes.Node{}, deployments.Deployment{}, wire.InferenceCommand{}); e == nil || e.Error() != "REGISTRY_OWNER_REQUIRED" {
			t.Fatal("missing prepare owner accepted", e)
		}
		if e := s.BindActionWithin(context.Background(), nil, identity.Auth{}, nodes.Node{}, nodes.Task{}, wire.InferenceCommand{}, false, "", ""); e == nil || e.Error() != "REGISTRY_OWNER_REQUIRED" {
			t.Fatal("missing binding owner accepted", e)
		}
	}
	if e := RequireEmptyWhenDisabled(context.Background(), nil); e == nil || e.Error() != "REGISTRY_OWNER_REQUIRED" {
		t.Fatal("uninspected disabled state accepted", e)
	}
}
