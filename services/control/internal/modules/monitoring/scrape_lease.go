package monitoring

import (
	"context"
	"errors"
	"time"
)

// RunNodeScrapeLease permits a workspace's collector to run only during a
// current inventory lease. Refresh must reestablish authority and durably
// publish the workspace's discovery file on every call. Collect must stop
// and join the owned process when its context ends. The caller owns process
// containment if the supervisor itself exits unexpectedly.
func RunNodeScrapeLease(ctx context.Context, refreshEvery, maximumAge time.Duration, refresh, collect func(context.Context) error) error {
	if ctx == nil || refreshEvery <= 0 || maximumAge <= refreshEvery || refresh == nil || collect == nil {
		return errors.New("MONITORING_SCRAPE_LEASE_INVALID")
	}
	expired := errors.New("MONITORING_SCRAPE_LEASE_EXPIRED")
	started := time.Now()
	deadline := started.Add(maximumAge)
	initial, end := context.WithDeadline(ctx, deadline)
	err := refresh(initial)
	end()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !time.Now().Before(deadline) {
		return expired
	}
	if err != nil {
		return err
	}

	run, stop := context.WithCancel(ctx)
	// Cancellation must reach the collector even when publication is blocked
	// inside filesystem I/O. Renew only while this timer can still be stopped.
	expiry := time.AfterFunc(time.Until(deadline), stop)
	defer expiry.Stop()
	done := make(chan struct{})
	var collectError error
	go func() {
		defer close(done)
		collectError = collect(run)
	}()
	defer func() {
		stop()
		<-done
	}()

	next := time.NewTimer(time.Until(started.Add(refreshEvery)))
	defer next.Stop()
	for {
		select {
		case <-run.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return expired
		case <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !time.Now().Before(deadline) {
				return expired
			}
			if collectError != nil {
				return collectError
			}
			return errors.New("MONITORING_SCRAPE_COLLECTOR_EXITED")
		case <-next.C:
			started = time.Now()
			if run.Err() != nil || !started.Before(deadline) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return expired
			}
			err := refresh(run)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !expiry.Stop() || !time.Now().Before(deadline) {
				return expired
			}
			if err != nil {
				return err
			}
			deadline = started.Add(maximumAge)
			expiry.Reset(time.Until(deadline))
			next.Reset(time.Until(started.Add(refreshEvery)))
		}
	}
}
