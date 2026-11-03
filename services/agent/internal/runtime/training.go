package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type trainingExecutor interface {
	Begin(string, wire.Lease) (training.Journal, error)
	Execute(context.Context, string, wire.Lease, training.Transport) (trainingwire.StopReceipt, []byte, error)
	AcceptStop(trainingwire.StopReceipt) error
}

func executeTraining(ctx context.Context, connection *client.Client, base, node string, lease wire.Lease, manager trainingExecutor) error {
	// Persist the delivered token before the first renewal can fail. This supports truthful never-created cleanup.
	if lease.Training == nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if _, e := manager.Begin(node, lease); e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(lease.Training.Recipe.Resources.WallSeconds)*time.Second)
	defer cancel()
	taskBase := base + "/tasks/" + lease.TaskID
	done := make(chan struct{})
	finish := make(chan struct{})
	renewed := make(chan error, 1)
	renew := func(ctx context.Context) (time.Time, error) {
		started := time.Now()
		var out wire.RenewedLease
		if e := connection.Post(ctx, taskBase+"/renewals", wire.LeaseRenewal{LeaseToken: lease.LeaseToken}, &out); e != nil {
			return time.Time{}, e
		}
		return renewalDeadline(started, out)
	}
	expiry, e := renew(bounded)
	if e != nil {
		renewed <- e
		cancel()
		close(done)
	} else {
		go func() {
			defer close(done)
			ticker := time.NewTicker(time.Duration(wire.LeaseRenewalSeconds) * time.Second)
			defer ticker.Stop()
			watchdog := time.NewTimer(time.Until(expiry))
			defer watchdog.Stop()
			fail := func(e error) { renewed <- e; cancel() }
			for {
				select {
				case <-bounded.Done():
					return
				case <-finish:
					return
				case <-watchdog.C:
					fail(errors.New("AGENT_LEASE_STALE"))
					return
				case <-ticker.C:
					renewal, stop := context.WithDeadline(bounded, expiry)
					next, e := renew(renewal)
					stop()
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
	}
	receipt, outcome, executionError := manager.Execute(bounded, node, lease, connection)
	close(finish)
	<-done
	cancel()
	if receipt.ReceiptID == "" {
		if executionError != nil {
			return executionError
		}
		return errors.New("TRAINING_STOP_UNCERTAIN")
	}
	// Cleanup submission is deliberately separate from execution/output authority and survives local shutdown.
	cleanup, stop := context.WithTimeout(context.Background(), settings.Seconds(connection.Policy().ControlRequestSeconds))
	defer stop()
	if _, e = connection.TrainingStop(cleanup, taskBase, lease.LeaseToken, *lease.Training, receipt, outcome); e != nil {
		return e
	}
	if e = manager.AcceptStop(receipt); e != nil {
		return e
	}
	select {
	case e := <-renewed:
		return e
	default:
	}
	return executionError
}
