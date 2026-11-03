package taskdeliveries

import (
	"context"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Run is one explicitly started, joined transport worker. Node machine polling,
// task claim and physical leases continue to belong exclusively to nodes.
func (s *Service) Run(ctx context.Context) (err error) {
	run, done, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	l := s.lifecycle
	l.mu.Lock()
	if l.workerFailure != "" {
		code := l.workerFailure
		l.mu.Unlock()
		return fail(code)
	}
	if l.running {
		l.mu.Unlock()
		return fail("TASK_BUS_WORKER_ALREADY_RUNNING")
	}
	l.running = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		if err != nil && fatalWorkerError(err) {
			// A failed owner cannot be restarted by a stale caller. Recovery
			// requires closing this owner and constructing a newly admitted one.
			l.workerFailure = err.Error()
		}
		l.running = false
		l.mu.Unlock()
	}()
	for run.Err() == nil {
		if err = s.Transport.Verify(run); err != nil {
			if err = s.recoverTransport(run, err); err != nil {
				if run.Err() != nil {
					return cancelledCommitOutcome(err)
				}
				return err
			}
		}
		published, publishErr := s.ProcessNext(run)
		if run.Err() != nil {
			return cancelledCommitOutcome(publishErr)
		}
		// A terminal admission/integrity failure fences the rest of this cycle.
		// In particular, do not pull/ACK a different event or remove retained
		// history after an earlier stage has already required operator recovery.
		if publishErr != nil && fatalWorkerError(publishErr) {
			return safe(publishErr)
		}
		expired, expiryErr := s.ExpireNext(run)
		if run.Err() != nil {
			return cancelledCommitOutcome(publishErr, expiryErr)
		}
		if expiryErr != nil && fatalWorkerError(expiryErr) {
			return safe(expiryErr)
		}
		cleaned, cleanupErr := s.Cleanup(run)
		if run.Err() != nil {
			return cancelledCommitOutcome(publishErr, expiryErr, cleanupErr)
		}
		if cleanupErr != nil && fatalWorkerError(cleanupErr) {
			return safe(cleanupErr)
		}
		received, consumeErr := s.ConsumeNext(run)
		if run.Err() != nil {
			return cancelledCommitOutcome(publishErr, expiryErr, cleanupErr, consumeErr)
		}
		if consumeErr != nil && fatalWorkerError(consumeErr) {
			return safe(consumeErr)
		}
		if run.Err() != nil {
			return cancelledCommitOutcome(publishErr, expiryErr, cleanupErr, consumeErr)
		}
		if publishErr != nil || consumeErr != nil || expiryErr != nil || cleanupErr != nil {
			// SQL errors do not consume the reconnect budget. The next loop first
			// checks real transport readiness before attempting reconstruction.
			if !pause(run, s.Configuration.RetrySeconds) {
				return cancelledCommitOutcome(publishErr, expiryErr, cleanupErr, consumeErr)
			}
		} else if !published && !received && !expired && cleaned == 0 && !pause(run, s.Configuration.WorkerPollSeconds) {
			return nil
		}
	}
	return nil
}

// Cancellation cannot turn a completed transaction's reported outcome into a
// clean worker stop. Plain cancellation still joins normally.
func cancelledCommitOutcome(causes ...error) error {
	if kind, ok := transaction.CommitOutcome(errors.Join(causes...)); ok {
		return fail(string(kind))
	}
	for _, cause := range causes {
		if cause != nil && fatalWorkerError(cause) {
			return safe(cause)
		}
	}
	return nil
}
func fatalWorkerError(err error) bool {
	switch err.Error() {
	case "TASK_BUS_SCHEMA_REJECTED", "TASK_BUS_IDENTITY_CHANGED", "TASK_BUS_PROFILE_REJECTED", "TASK_BUS_NOT_ADMITTED", "TASK_BUS_COMPOSITION_INVALID", "TASK_EVENT_RECEIPT_CONFLICT", "TASK_BUS_READBACK_CONFLICT", "TASK_EVENT_INVALID", "STORAGE_COMMIT_OUTCOME_UNKNOWN", "TASK_BUS_RECOVERY_EXHAUSTED":
		return true
	}
	return false
}
func pause(ctx context.Context, seconds int) bool {
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Service) recoverTransport(ctx context.Context, cause error) error {
	if fatalWorkerError(cause) {
		return safe(cause)
	}
	for attempt := 0; attempt < s.Configuration.MaximumReconnectAttempts; attempt++ {
		if !pause(ctx, s.Configuration.ReconnectSeconds) {
			return safe(ctx.Err())
		}
		err := s.Transport.Reconnect(ctx)
		if err == nil {
			return nil
		}
		if fatalWorkerError(err) {
			return safe(err)
		}
	}
	return fail("TASK_BUS_RECOVERY_EXHAUSTED")
}
