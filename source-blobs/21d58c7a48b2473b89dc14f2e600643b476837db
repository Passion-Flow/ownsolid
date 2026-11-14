package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
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
	wall := time.Duration(lease.Training.Recipe.Resources.WallSeconds) * time.Second
	return superviseTraining(ctx, connection, base, lease, wall, manager, func(bounded context.Context) (trainingwire.StopReceipt, []byte, error) {
		return manager.Execute(bounded, node, lease, connection)
	})
}

// resumeTraining adopts and follows the attempt an earlier process left running, if any.
func resumeTraining(ctx context.Context, connection *client.Client, node string, manager *training.Manager) error {
	lease, wall, ok, e := manager.Resumable(node)
	if e != nil || !ok {
		return e
	}
	slog.Info("training_attempt_adopted", "task_id", lease.TaskID)
	base := "/agent/v1/nodes/" + url.PathEscape(node)
	return superviseTraining(ctx, connection, base, lease, wall, manager, func(bounded context.Context) (trainingwire.StopReceipt, []byte, error) {
		return manager.Resume(bounded, connection)
	})
}

func superviseTraining(ctx context.Context, connection *client.Client, base string, lease wire.Lease, wall time.Duration, manager interface {
	AcceptStop(trainingwire.StopReceipt) error
}, run func(context.Context) (trainingwire.StopReceipt, []byte, error)) error {
	taskBase := base + "/tasks/" + lease.TaskID
	receipt, outcome, executionError, refused := renewedAttempt(ctx, wall, leaseRenewal(connection, taskBase, lease), lease.TaskID, run)
	if receipt.ReceiptID == "" {
		if executionError != nil {
			return executionError
		}
		return errors.New("TRAINING_STOP_UNCERTAIN")
	}
	// Cleanup submission is deliberately separate from execution/output authority and survives local shutdown.
	cleanup, stop := context.WithTimeout(context.Background(), settings.Seconds(connection.Policy().ControlRequestSeconds))
	defer stop()
	if _, e := connection.TrainingStop(cleanup, taskBase, lease.LeaseToken, *lease.Training, receipt, outcome); e != nil {
		return e
	}
	if e := manager.AcceptStop(receipt); e != nil {
		return e
	}
	if refused != nil {
		return refused
	}
	return executionError
}
