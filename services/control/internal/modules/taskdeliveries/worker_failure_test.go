package taskdeliveries

import (
	"context"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestTaskDeliveryFailedWorkerCannotResume(t *testing.T) {
	for _, code := range []string{"TASK_EVENT_INVALID", "TASK_BUS_IDENTITY_CHANGED", "TASK_EVENT_RECEIPT_CONFLICT", "TASK_BUS_READBACK_CONFLICT", "STORAGE_COMMIT_OUTCOME_UNKNOWN", "TASK_BUS_RECOVERY_EXHAUSTED"} {
		t.Run(code, func(t *testing.T) {
			n := &nodes.Service{}
			s := &Service{Nodes: n}
			n.TaskEvents = s
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.lifecycle = &lifecycle{ctx: ctx, cancel: cancel, nodes: n, configuration: configurationDigest(s.Configuration), workerFailure: code}
			// No transport is attached: any resumed broker operation would panic.
			// Concurrent stale callers must all see the original safe failure.
			var callers sync.WaitGroup
			for range 8 {
				callers.Go(func() {
					if err := s.Run(context.Background()); err == nil || err.Error() != code {
						t.Error("failed worker restarted", err)
					}
				})
			}
			callers.Wait()
			if s.lifecycle.running {
				t.Fatal("failed worker became running")
			}
			s.lifecycle.active.Wait()
		})
	}
}

func TestTaskDeliveryUnknownCommitIsTerminalButKnownRollbackIsRetryable(t *testing.T) {
	if !fatalWorkerError(fail(string(transaction.CommitUnknown))) || fatalWorkerError(fail(string(transaction.CommitRolledBack))) {
		t.Fatal("worker lost unknown versus known rollback distinction")
	}
}

func TestTaskDeliveryCancellationRetainsTerminalWorkerFailure(t *testing.T) {
	for _, code := range []string{"TASK_EVENT_INVALID", "TASK_BUS_IDENTITY_CHANGED", "TASK_EVENT_RECEIPT_CONFLICT", "TASK_BUS_READBACK_CONFLICT"} {
		t.Run(code, func(t *testing.T) {
			if err := cancelledCommitOutcome(context.Canceled, fail(code)); err == nil || err.Error() != code {
				t.Fatal("cancellation concealed terminal worker failure", err)
			}
			unknown := &transaction.Error{Kind: transaction.CommitUnknown}
			if err := cancelledCommitOutcome(fail(code), context.Canceled, unknown); err == nil || err.Error() != string(transaction.CommitUnknown) {
				t.Fatal("terminal failure concealed commit uncertainty", err)
			}
		})
	}
}
