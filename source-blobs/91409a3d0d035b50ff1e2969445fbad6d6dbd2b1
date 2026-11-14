package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	preparationwire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type preparationExecutor interface {
	Begin(string, wire.Lease) (modelpreparations.Journal, error)
	Execute(context.Context, string, wire.Lease, modelpreparations.Transport) (preparationwire.StopReceipt, []byte, error)
	AcceptStop(preparationwire.StopReceipt) error
}

func executePreparation(ctx context.Context, connection *client.Client, base, node string, lease wire.Lease, manager preparationExecutor, outputSeconds int) error {
	if outputSeconds < 1 || outputSeconds > 3600 {
		return errors.New("MODEL_PREPARATION_AGENT_CONFIGURATION_INVALID")
	}
	// Persist the delivered token before the first renewal can fail. This supports truthful never-created cleanup.
	if lease.Preparation == nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if _, e := manager.Begin(node, lease); e != nil {
		return e
	}
	wall := time.Duration(lease.Preparation.Recipe.Resources.WallSeconds) * time.Second
	return supervisePreparation(ctx, connection, base, lease, wall, outputSeconds, manager, func(bounded context.Context) (preparationwire.StopReceipt, []byte, error) {
		return manager.Execute(bounded, node, lease, connection)
	})
}

// resumePreparation adopts and follows the attempt an earlier process left running, if any.
func resumePreparation(ctx context.Context, connection *client.Client, node string, manager *modelpreparations.Manager, outputSeconds int) error {
	if outputSeconds < 1 || outputSeconds > 3600 {
		return errors.New("MODEL_PREPARATION_AGENT_CONFIGURATION_INVALID")
	}
	lease, wall, ok, e := manager.Resumable(node)
	if e != nil || !ok {
		return e
	}
	slog.Info("model_preparation_attempt_adopted", "task_id", lease.TaskID)
	base := "/agent/v1/nodes/" + url.PathEscape(node)
	return supervisePreparation(ctx, connection, base, lease, wall, outputSeconds, manager, func(bounded context.Context) (preparationwire.StopReceipt, []byte, error) {
		return manager.Resume(bounded, connection)
	})
}

func supervisePreparation(ctx context.Context, connection *client.Client, base string, lease wire.Lease, wall time.Duration, outputSeconds int, manager interface {
	AcceptStop(preparationwire.StopReceipt) error
}, run func(context.Context) (preparationwire.StopReceipt, []byte, error)) error {
	taskBase := base + "/tasks/" + lease.TaskID
	receipt, outcome, executionError, refused := renewedAttempt(ctx, wall, leaseRenewal(connection, taskBase, lease), lease.TaskID, run)
	if receipt.ReceiptID == "" {
		if executionError != nil {
			return executionError
		}
		return errors.New("MODEL_PREPARATION_STOP_UNCERTAIN")
	}
	// Cleanup submission is deliberately separate from execution/output authority and survives local shutdown.
	cleanup, stop := context.WithTimeout(context.Background(), settings.Seconds(outputSeconds))
	defer stop()
	if _, e := connection.PreparationStop(cleanup, taskBase, lease.LeaseToken, *lease.Preparation, receipt, outcome); e != nil {
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
