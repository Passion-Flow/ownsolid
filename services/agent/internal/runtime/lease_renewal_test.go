package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

// fastRenewal shortens the renewal interval for one test.
func fastRenewal(t *testing.T) {
	t.Helper()
	previous := renewalInterval
	renewalInterval = 10 * time.Millisecond
	t.Cleanup(func() { renewalInterval = previous })
}

var refusal = &client.Problem{Code: "AGENT_LEASE_STALE", Status: 409}

func TestAFailedRenewalNeverEndsTheTask(t *testing.T) {
	fastRenewal(t)
	for _, failure := range []error{
		errors.New("CONTROL_CONNECTION_FAILED"),
		errors.New("CONTROL_RESPONSE_INVALID"),
		errors.New("CONTROL_RESPONSE_REJECTED"),
		&client.Problem{Code: "STORAGE_UNAVAILABLE", Status: 503},
		&client.Problem{Code: "AGENT_OVERLOADED", Status: 503, RetryAfter: 2 * time.Second},
		&client.Problem{Code: "RATE_LIMITED", Status: 429},
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			var calls atomic.Int32
			renew := func(context.Context) error {
				calls.Add(1)
				return failure
			}
			bounded, revoke := context.WithCancelCause(t.Context())
			defer revoke(nil)
			stop := make(chan struct{})
			done, failed := renewWhileRunning(bounded, revoke, renew, "task", stop)
			for calls.Load() < 8 {
				time.Sleep(5 * time.Millisecond)
			}
			close(stop)
			<-done
			select {
			case e := <-failed:
				t.Fatalf("the failure ended the task: %v", e)
			default:
			}
			if bounded.Err() != nil {
				t.Fatal("the task was cancelled")
			}
		})
	}
}

func TestRenewalResumesAfterAnOutageAndARefusalStillEndsTheTask(t *testing.T) {
	fastRenewal(t)
	var calls atomic.Int32
	renew := func(context.Context) error {
		switch n := calls.Add(1); {
		case n <= 3:
			return errors.New("CONTROL_CONNECTION_FAILED")
		case n <= 6:
			return nil
		}
		return refusal
	}
	bounded, revoke := context.WithCancelCause(t.Context())
	defer revoke(nil)
	done, failed := renewWhileRunning(bounded, revoke, renew, "task", make(chan struct{}))
	<-done
	if e := <-failed; !errors.Is(e, refusal) {
		t.Fatalf("refusal %v", e)
	}
	if !errors.Is(context.Cause(bounded), refusal) || calls.Load() != 7 {
		t.Fatalf("cause %v after %d renewals", context.Cause(bounded), calls.Load())
	}
}

func attemptRun(t *testing.T, started chan<- bool) func(context.Context) (string, []byte, error) {
	t.Helper()
	return func(ctx context.Context) (string, []byte, error) {
		started <- ctx.Err() == nil
		<-ctx.Done()
		return "receipt", nil, ctx.Err()
	}
}

func TestAFirstRenewalThatFailsStillStartsTheAttempt(t *testing.T) {
	fastRenewal(t)
	var calls atomic.Int32
	renew := func(context.Context) error {
		if calls.Add(1) < 5 {
			return errors.New("CONTROL_CONNECTION_FAILED")
		}
		return refusal
	}
	started := make(chan bool, 1)
	receipt, _, executionError, refused := renewedAttempt(t.Context(), time.Minute, renew, "task", attemptRun(t, started))
	if !<-started {
		t.Fatal("the outage cancelled the attempt before it started")
	}
	if receipt != "receipt" || !errors.Is(refused, refusal) || !errors.Is(executionError, context.Canceled) {
		t.Fatalf("receipt %q, refused %v, execution %v", receipt, refused, executionError)
	}
}

func TestARefusedFirstRenewalRunsTheAttemptCancelled(t *testing.T) {
	renew := func(context.Context) error { return refusal }
	started := make(chan bool, 1)
	_, _, executionError, refused := renewedAttempt(t.Context(), time.Minute, renew, "task", func(ctx context.Context) (string, []byte, error) {
		started <- ctx.Err() == nil
		if !errors.Is(context.Cause(ctx), refusal) {
			t.Errorf("cause %v", context.Cause(ctx))
		}
		return "receipt", nil, ctx.Err()
	})
	if <-started || !errors.Is(refused, refusal) || !errors.Is(executionError, context.Canceled) {
		t.Fatalf("refused %v, execution %v", refused, executionError)
	}
}

func TestTheWallLimitAndShutdownEndAnAttemptWithoutARefusal(t *testing.T) {
	fastRenewal(t)
	down := func(context.Context) error { return errors.New("CONTROL_CONNECTION_FAILED") }
	started := make(chan bool, 1)
	_, _, executionError, refused := renewedAttempt(t.Context(), 50*time.Millisecond, down, "task", attemptRun(t, started))
	if !errors.Is(executionError, context.DeadlineExceeded) || refused != nil {
		t.Fatalf("wall limit: %v, %v", executionError, refused)
	}
	<-started
	ctx, shutdown := context.WithCancel(t.Context())
	time.AfterFunc(30*time.Millisecond, shutdown)
	_, _, executionError, refused = renewedAttempt(ctx, time.Minute, down, "task", func(bounded context.Context) (string, []byte, error) {
		<-bounded.Done()
		if context.Cause(bounded) != context.Canceled {
			t.Errorf("shutdown cause %v", context.Cause(bounded))
		}
		return "", nil, bounded.Err()
	})
	if !errors.Is(executionError, context.Canceled) || refused != nil {
		t.Fatalf("shutdown: %v, %v", executionError, refused)
	}
}

func TestAResultIsDeliveredAfterAnOutage(t *testing.T) {
	var calls atomic.Int32
	e := deliverResult(t.Context(), time.Minute, func(context.Context) error {
		if calls.Add(1) < 3 {
			return errors.New("CONTROL_CONNECTION_FAILED")
		}
		return nil
	})
	if e != nil || calls.Load() != 3 {
		t.Fatalf("%v after %d calls", e, calls.Load())
	}
}

func TestAResultStopsAtARefusalOrItsLimit(t *testing.T) {
	var calls atomic.Int32
	e := deliverResult(t.Context(), time.Minute, func(context.Context) error {
		calls.Add(1)
		return refusal
	})
	if !errors.Is(e, refusal) || calls.Load() != 1 {
		t.Fatalf("refused result %v after %d calls", e, calls.Load())
	}
	started := time.Now()
	e = deliverResult(t.Context(), 100*time.Millisecond, func(context.Context) error { return errors.New("CONTROL_CONNECTION_FAILED") })
	if e == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("limit %v after %v", e, time.Since(started))
	}
}
