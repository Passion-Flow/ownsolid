package workflows

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"sync"
	"testing"
	"time"
)

// This is a process-lifecycle unit fixture, not an admitted workflow/provider
// execution. A controlled worker proves Stop waits for actual owned cleanup.
func TestWorkflowConcurrentStopWaitersJoinOwnedCleanupAndReportTimeout(t *testing.T) {
	lifecycle, cancel := context.WithCancelCause(context.Background())
	s := &Service{Identity: &identity.Service{}, started: true, lifecycleContext: lifecycle, lifecycleCancel: cancel, running: map[string]context.CancelCauseFunc{}}
	s.Identity.Config.Workflows.Enabled = true
	cleanup := make(chan struct{})
	left := make(chan struct{})
	s.workers.Add(1)
	go func() { defer s.workers.Done(); defer close(left); <-lifecycle.Done(); <-cleanup }()
	bounded, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if e := s.Stop(bounded); e == nil || e.Error() != "WORKFLOW_SHUTDOWN_TIMEOUT" {
		t.Fatal("unjoined cleanup was reported stopped", e)
	}
	stop()
	if e := s.VerifyStored(context.Background()); e == nil {
		t.Fatal("closing service remained ready")
	}
	const waiters = 3
	result := make(chan error, waiters)
	var entered sync.WaitGroup
	for i := 0; i < waiters; i++ {
		entered.Add(1)
		go func() {
			entered.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result <- s.Stop(ctx)
		}()
	}
	entered.Wait()
	select {
	case e := <-result:
		t.Fatal("Stop returned before cleanup joined", e)
	default:
	}
	close(cleanup)
	for i := 0; i < waiters; i++ {
		if e := <-result; e != nil {
			t.Fatal("shared shutdown waiter", e)
		}
	}
	select {
	case <-left:
	default:
		t.Fatal("owned worker not joined")
	}
	if e := s.Stop(context.Background()); e != nil {
		t.Fatal("stopped service was not idempotent", e)
	}
}
func TestWorkflowUnavailableAdmissionNeverTouchesStorageOrReturnsAccepted(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	s.Identity.Config.Workflows.Enabled = true
	out, e := s.Debug(context.Background(), identity.Auth{}, "", "", DebugRequest{Question: "Synthetic question", ExpectedRevision: 1, Reason: "Synthetic unavailable executor"}, "", "")
	if e == nil || e.Error() != "WORKFLOW_UNAVAILABLE" || out.ID != "" {
		t.Fatal("unavailable process admitted work", e, out.ID)
	}
	state, code := runFailure(context.Background(), errors.New("synthetic private provider body"))
	if state != "failed" || code != "STORAGE_UNAVAILABLE" {
		t.Fatal("private diagnostics persisted")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(failure("WORKFLOW_RUN_CANCELLED"))
	state, code = runFailure(ctx, errors.New("transport interrupted"))
	if state != "cancelled" || code != "WORKFLOW_RUN_CANCELLED" {
		t.Fatal("explicit cancellation cause lost")
	}
}
