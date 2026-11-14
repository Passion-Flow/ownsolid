package runtime

import (
	"context"
	"log/slog"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

// renewalInterval is how often a running task renews its lease; tests shorten it.
var renewalInterval = time.Duration(wire.LeaseRenewalSeconds) * time.Second

// renewal renews one task lease.
type renewal func(context.Context) error

// leaseRenewal is the renewal of lease through the task path taskBase.
func leaseRenewal(connection *client.Client, taskBase string, lease wire.Lease) renewal {
	return func(ctx context.Context) error {
		bounded, cancel := context.WithTimeout(ctx, settings.Seconds(connection.Policy().ControlRequestSeconds))
		defer cancel()
		var out wire.RenewedLease
		if e := connection.Post(bounded, taskBase+"/renewals", wire.LeaseRenewal{LeaseToken: lease.LeaseToken}, &out); e != nil {
			return e
		}
		return validRenewal(out)
	}
}

// renewWhileRunning keeps renewing a lease until stop closes or bounded ends. Failing to
// reach control ends nothing: the work goes on and the next tick tries again, for as long
// as it takes (ADR 0032). When control itself refuses the lease, renewWhileRunning revokes
// the task with that refusal as the cause and sends it on failed. done closes once renewal
// has stopped.
func renewWhileRunning(bounded context.Context, revoke context.CancelCauseFunc, renew renewal, task string, stop <-chan struct{}) (done <-chan struct{}, failed <-chan error) {
	finished := make(chan struct{})
	reasons := make(chan error, 1)
	go func() {
		defer close(finished)
		ticker := time.NewTicker(renewalInterval)
		defer ticker.Stop()
		out := false
		for {
			select {
			case <-bounded.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				e := renew(bounded)
				if client.Interrupted(e) {
					if !out && bounded.Err() == nil {
						slog.Warn("agent_lease_renewal_interrupted", "task_id", task, "code", e.Error())
						out = true
					}
					continue
				}
				if e != nil {
					reasons <- e
					revoke(e)
					return
				}
				if out {
					slog.Info("agent_lease_renewal_resumed", "task_id", task)
					out = false
				}
			}
		}
	}()
	return finished, reasons
}

// renewedAttempt runs one training or preparation attempt with its lease renewed, within
// the wall limit. A refusal of the first renewal still runs it, cancelled, so its stop
// receipt proves it never started. refused is the refusal that ended the attempt, if any.
func renewedAttempt[R any](ctx context.Context, wall time.Duration, renew renewal, task string, run func(context.Context) (R, []byte, error)) (receipt R, outcome []byte, executionError, refused error) {
	limited, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	bounded, revoke := context.WithCancelCause(limited)
	defer revoke(nil)
	stop := make(chan struct{})
	finished := make(chan struct{})
	close(finished)
	var done <-chan struct{} = finished
	reasons := make(chan error, 1)
	var failed <-chan error = reasons
	if e := renew(bounded); e != nil && !client.Interrupted(e) {
		reasons <- e
		revoke(e)
	} else {
		done, failed = renewWhileRunning(bounded, revoke, renew, task, stop)
	}
	receipt, outcome, executionError = run(bounded)
	close(stop)
	<-done
	select {
	case refused = <-failed:
	default:
	}
	return receipt, outcome, executionError, refused
}

// deliverResult sends the result of finished work, again whenever control cannot be
// reached, until control answers or limit passes. Without it the work would run a second
// time for the sake of one lost request.
func deliverResult(ctx context.Context, limit time.Duration, post func(context.Context) error) error {
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var retry client.Retry
	for {
		e := post(bounded)
		if !client.Interrupted(e) {
			return e
		}
		delay, _ := retry.Failed(time.Now())
		timer := time.NewTimer(delay)
		select {
		case <-bounded.Done():
			timer.Stop()
			return e
		case <-timer.C:
		}
	}
}
