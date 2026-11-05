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
	finish := make(chan struct{})
	renew := leaseRenewal(connection, taskBase, lease)
	var done <-chan struct{}
	var renewed <-chan error
	expiry, e := renew(bounded)
	if e != nil {
		// Execute still runs, cancelled, so the stop receipt proves nothing started.
		cancel()
		done, renewed = lostBeforeStart(e)
	} else {
		done, renewed = renewWhileRunning(bounded, cancel, renew, lease.TaskID, expiry, finish)
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
