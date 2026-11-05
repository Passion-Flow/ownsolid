package runtime

import (
	"context"
	"errors"
	"log/slog"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

// renewalInterval is how often a running task renews its lease; tests shorten it.
var renewalInterval = time.Duration(wire.LeaseRenewalSeconds) * time.Second

// renewal renews one task lease and returns when the renewed lease expires.
type renewal func(context.Context) (time.Time, error)

// leaseRenewal is the renewal of lease through the task path taskBase.
func leaseRenewal(connection *client.Client, taskBase string, lease wire.Lease) renewal {
	return func(ctx context.Context) (time.Time, error) {
		started := time.Now()
		var out wire.RenewedLease
		if e := connection.Post(ctx, taskBase+"/renewals", wire.LeaseRenewal{LeaseToken: lease.LeaseToken}, &out); e != nil {
			return time.Time{}, e
		}
		return renewalDeadline(started, out)
	}
}

// renewWhileRunning keeps a lease that holds until expiry renewed until stop closes or
// bounded ends. When the lease is lost it cancels the task and sends the reason on
// failed. done closes once renewal has stopped.
//
// A renewal control refuses with a retry hint was shed under load (ADR 0017). The lease
// still holds, so the next tick tries again, and the task ends only when the lease
// expires without a renewal getting through.
func renewWhileRunning(bounded context.Context, cancel context.CancelFunc, renew renewal, task string, expiry time.Time, stop <-chan struct{}) (done <-chan struct{}, failed <-chan error) {
	finished := make(chan struct{})
	reasons := make(chan error, 1)
	go func() {
		defer close(finished)
		ticker := time.NewTicker(renewalInterval)
		defer ticker.Stop()
		watchdog := time.NewTimer(time.Until(expiry))
		defer watchdog.Stop()
		fail := func(e error) { reasons <- e; cancel() }
		for {
			select {
			case <-bounded.Done():
				return
			case <-stop:
				return
			case <-watchdog.C:
				fail(errors.New("AGENT_LEASE_STALE"))
				return
			case <-ticker.C:
				attempt, release := context.WithDeadline(bounded, expiry)
				next, e := renew(attempt)
				release()
				if _, shed := client.RetryHint(e); shed {
					slog.Warn("agent_lease_renewal_deferred", "task_id", task, "code", e.Error())
					continue
				}
				if e != nil {
					fail(e)
					return
				}
				expiry = next
				if !watchdog.Stop() {
					select {
					case <-watchdog.C:
					default:
					}
				}
				watchdog.Reset(time.Until(expiry))
			}
		}
	}()
	return finished, reasons
}

// lostBeforeStart reports a lease whose first renewal failed, in the shape of
// renewWhileRunning: renewal is already over and failed holds the reason.
func lostBeforeStart(reason error) (done <-chan struct{}, failed <-chan error) {
	finished := make(chan struct{})
	close(finished)
	reasons := make(chan error, 1)
	reasons <- reason
	return finished, reasons
}
