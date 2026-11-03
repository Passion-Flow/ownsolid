package scope

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type restoreTaskEngine interface {
	CreateDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, error)
	InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error)
	StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
	RemoveDatabaseTask(context.Context, engine.DatabaseTask, string) error
}

// Tasks must already be admitted by their fixed SDK profile and durable plan.
// Reconciliation observes the original effect; only a new intent can submit.
func (l *locked) executeRestoreTask(ctx context.Context, c restoreTaskEngine, task engine.DatabaseTask, phase, id string, files []engine.ConfigurationFile, reconcile bool) (string, error) {
	if ctx.Err() != nil {
		return "", failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	switch phase {
	case "task-create":
		if !reconcile {
			if _, e := c.CreateDatabaseTask(ctx, task); e != nil {
				return "", e
			}
		}
		object, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil || !found || object.State.Status != "created" {
			return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_TASK_CREATION_UNRESOLVED")
		}
		return object.ID, nil
	case "task-execute":
		if !utilityIDPattern.MatchString(id) {
			return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_TASK_ID_REQUIRED")
		}
		if !reconcile {
			if _, e := c.StartDatabaseTask(ctx, task, id, files, l.config.Compose.Process.ReservePercent); e != nil {
				return "", e
			}
		}
		deadline := time.NewTimer(time.Duration(l.config.Compose.Process.ObserveSeconds) * time.Second)
		defer deadline.Stop()
		for {
			object, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil || !found || object.ID != id {
				return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_TASK_OBSERVATION_UNRESOLVED")
			}
			if object.State.OOMKilled || object.State.Dead || object.State.Paused || object.State.Restarting {
				return "", failure(restoreDatabaseTaskFailed)
			}
			if object.State.Status == "created" {
				return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_NEVER_STARTED_UNRESOLVED")
			}
			if object.State.Status == "exited" {
				if object.State.ExitCode != 0 {
					return "", failure(restoreDatabaseTaskFailed)
				}
				return "", nil
			}
			poll := time.NewTimer(time.Duration(l.config.Compose.Process.PollMilliseconds) * time.Millisecond)
			select {
			case <-ctx.Done():
				poll.Stop()
				return "", failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
			case <-deadline.C:
				poll.Stop()
				return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_TASK_DEADLINE")
			case <-poll.C:
			}
		}
	case "task-remove":
		if !utilityIDPattern.MatchString(id) {
			return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_TASK_ID_REQUIRED")
		}
		if !reconcile {
			if e := c.RemoveDatabaseTask(ctx, task, id); e != nil {
				return "", e
			}
		}
		if _, found, e := c.InspectDatabaseTask(ctx, task); e != nil || found {
			return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_TASK_REMOVAL_UNRESOLVED")
		}
		return "", nil
	}
	return "", failure("OFFLINE_SCOPE_RESTORE_DATABASE_STEP_INVALID")
}
