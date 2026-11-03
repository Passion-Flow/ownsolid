package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// This is a runtime-ordering double; it makes no physical service claim.
type componentRecoveryDouble struct {
	recover func(context.Context) error
	observe func(context.Context) wire.ComponentObservation
}

func (m *componentRecoveryDouble) Recover(ctx context.Context) error { return m.recover(ctx) }
func (m *componentRecoveryDouble) Observe(ctx context.Context) wire.ComponentObservation {
	if m.observe == nil {
		panic("observation before completed recovery")
	}
	return m.observe(ctx)
}
func (*componentRecoveryDouble) Execute(context.Context, wire.ComponentCommand, func(context.Context, string) error) (wire.ComponentObservation, error) {
	panic("unexpected component dispatch")
}
func (*componentRecoveryDouble) ExecuteMaintenance(context.Context, string, wire.ComponentCommand, func(context.Context, string) error) (wire.ComponentObservation, *wire.ComponentLogs, error) {
	panic("unexpected component maintenance dispatch")
}

func TestComponentRecoveryFailureBlocksCycleBeforeDiscoveryAndNetwork(t *testing.T) {
	failure := errors.New("COMPONENT_ROLLBACK_FAILED")
	calls := 0
	m := &componentRecoveryDouble{recover: func(context.Context) error { calls++; return failure }}
	for range 2 {
		// Nil client and invalid discovery path ensure no subsequent work can run.
		if e := cycle(t.Context(), Config{DiskPath: "/not-used"}, nil, state.Identity{}, m, nil, nil, nil, nil); !errors.Is(e, failure) {
			t.Fatal("recovery failure did not fence the next cycle", e)
		}
	}
	if calls != 2 {
		t.Fatal("same-process recovery was not retried on the next bounded cycle")
	}
}

func TestComponentRecoveryUsesOwnedBudgetAndCallerCancellation(t *testing.T) {
	policy := settings.Default()
	policy.ComponentTaskSeconds = 37
	c := Config{Runtime: &policy}
	var observed context.Context
	m := &componentRecoveryDouble{recover: func(ctx context.Context) error { observed = ctx; return nil }}
	before := time.Now()
	if e := recoverComponent(t.Context(), c, m); e != nil {
		t.Fatal(e)
	}
	deadline, ok := observed.Deadline()
	if !ok || deadline.Before(before.Add(36*time.Second)) || deadline.After(before.Add(38*time.Second)) || observed.Err() != context.Canceled {
		t.Fatal("recovery budget was absent or its context was left active")
	}
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	expected, _ := ctx.Deadline()
	if e := recoverComponent(ctx, c, m); e != nil {
		t.Fatal(e)
	}
	if deadline, _ = observed.Deadline(); deadline != expected {
		t.Fatal("recovery extended the caller deadline")
	}
	stop()
	m.recover = func(context.Context) error { t.Fatal("cancelled recovery started effects"); return nil }
	if e := recoverComponent(ctx, c, m); !errors.Is(e, context.Canceled) {
		t.Fatal("caller cancellation was lost", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.recover = func(context.Context) error { cancel(); return nil }
	if e := recoverComponent(ctx, c, m); !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation during recovery was published as success", e)
	}
}

func TestReadOnlyProfileHasNoComponentRecoveryOwner(t *testing.T) {
	m, e := manager(Config{ExecutionProfile: wire.ReadOnlyProfile})
	if e != nil || m != nil || recoverComponent(t.Context(), Config{}, m) != nil {
		t.Fatal("read-only profile acquired component effects", e)
	}
}
