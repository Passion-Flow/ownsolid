package nodes

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestRegistryActionReadsRequireOwningExecutor(t *testing.T) {
	s := &Service{}
	if found, e := s.RegistryIntentRetainedWithin(context.Background(), nil, Node{}, "invalid"); e == nil || found {
		t.Fatal("history bypassed owner", e)
	}
	if _, task, _, e := s.RetainedInferenceTaskForActorWithin(context.Background(), nil, identity.Auth{}, "invalid", "invalid"); e == nil || task.ID != "" {
		t.Fatal("replay bypassed owner", e)
	}
}
