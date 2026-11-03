package nodes

import (
	"context"
	"errors"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestSelectedTaskRuntimeSafeFailureCodes(t *testing.T) {
	for _, code := range []string{"NODE_BUSY", "AGENT_LEASE_STALE", "INFERENCE_RESOURCE_BUSY", "NODE_POSTGRESQL_CALLBACK_REQUIRED", string(transaction.CommitUnknown), string(transaction.CommitRolledBack)} {
		if e := taskRuntimeError(errors.New(code)); e == nil || e.Error() != code {
			t.Fatal("safe code lost", code, e)
		}
	}
	if e := taskRuntimeError(errors.New("sql private-credential query failed")); e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("vendor detail leaked", e)
	}
	if e := taskRuntimeError(context.Canceled); e.Error() != string(transaction.Cancelled) {
		t.Fatal("cancellation lost", e)
	}
}
func TestSelectedMachineInvalidInputsPublishNoProjection(t *testing.T) {
	s := new(Service)
	if out, e := s.Heartbeat(context.Background(), "invalid", "secret", wire.Handshake{}); e == nil || out.Status != "" || !out.ServerTime.IsZero() {
		t.Fatal("invalid heartbeat projected", out, e)
	}
	if out, e := s.Complete(context.Background(), "invalid", "secret", "invalid", wire.Result{}, "invalid"); e == nil || out != "" {
		t.Fatal("invalid result projected", out, e)
	}
	if e := s.WithLeaseExecutor(context.Background(), "invalid", "secret", "invalid", "secret", nil); e == nil {
		t.Fatal("nil owning callback accepted")
	}
	if out, e := s.CreateInferenceTaskBoundWithin(context.Background(), nil, identity.Auth{}, "invalid", "invalid", "invalid", "invalid", "invalid", nil, nil, nil); e == nil || out.ID != "" {
		t.Fatal("invalid borrowed task projected", out, e)
	}
}
