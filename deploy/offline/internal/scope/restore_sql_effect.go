package scope

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const restoreSQLTaskFailed = "OFFLINE_SCOPE_RESTORE_SQL_TASK_FAILED"

func (l *locked) executeRestoreSQLStep(ctx context.Context, c restoreSQLEngine, r RestoreSQLRecord, files []engine.ConfigurationFile, request configuration.RestoreRequest, reconcile bool) (RestoreSQLRecord, error) {
	if ctx.Err() != nil {
		return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if owner, ok := restoreSQLVolume(r, r.Role); ok {
		switch r.Phase {
		case "volume-create":
			if !reconcile {
				if _, e := c.CreateVolume(ctx, owner); e != nil {
					return r, e
				}
			}
			if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_VOLUME_UNRESOLVED")
			}
		case "utility-create":
			if !reconcile {
				if _, e := c.CreateUtility(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID); e != nil {
					return r, e
				}
			}
			u, found, e := c.InspectUtility(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID)
			if e != nil || !found {
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_UTILITY_UNRESOLVED")
			}
			r.ObjectID = u.ID
		case "payload-write":
			if r.Role == "source" {
				if !reconcile {
					f, e := os.OpenFile(filepath.Join(request.BackupRoot, "postgres.tar"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
					if e != nil {
						return r, failure("OFFLINE_SCOPE_RESTORE_SQL_ARCHIVE_UNAVAILABLE")
					}
					_, e = c.PutPostgresRestoreArchive(ctx, r.Task, r.Initializer, f)
					closeError := f.Close()
					if e != nil {
						return r, e
					}
					if closeError != nil {
						return r, failure("OFFLINE_SCOPE_RESTORE_SQL_ARCHIVE_CLOSE_FAILED")
					}
					return r, nil
				}
				if _, e := c.InspectPostgresRestoreArchiveInput(ctx, r.Task, r.Initializer); e != nil {
					return r, e
				}
			} else {
				payload := files
				if r.Role == "result" {
					payload = nil
				}
				if !reconcile {
					if e := c.PutConfiguration(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID, payload); e != nil {
						return r, e
					}
				}
				observed, e := c.InspectConfiguration(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID, payload)
				if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
					return r, failure("OFFLINE_SCOPE_RESTORE_SQL_PRIVATE_PAYLOAD_UNRESOLVED")
				}
			}
		case "utility-remove":
			if !reconcile {
				if e := c.RemoveUtility(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID); e != nil {
					return r, e
				}
			}
			if _, found, e := c.InspectUtility(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID); e != nil || found {
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_UTILITY_REMOVAL_UNRESOLVED")
			}
		case "volume-remove":
			if !reconcile {
				if e := c.RemovePostgresRestoreVolume(ctx, owner); e != nil {
					return r, e
				}
			}
			if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_VOLUME_REMOVAL_UNRESOLVED")
			}
		default:
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_STEP_INVALID")
		}
		return r, nil
	}
	id := restoreSQLTaskID(l.events)
	switch r.Phase {
	case "task-create":
		if !reconcile {
			if _, e := c.CreatePostgresBackupTask(ctx, r.Task); e != nil {
				return r, e
			}
		}
		task, found, e := c.InspectPostgresBackupTask(ctx, r.Task)
		if e != nil || !found || task.State.Status != "created" {
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_CREATION_UNRESOLVED")
		}
		r.ObjectID = task.ID
	case "task-execute":
		if !utilityIDPattern.MatchString(id) {
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_ID_REQUIRED")
		}
		if !reconcile {
			if _, e := c.StartPostgresBackupTask(ctx, r.Task, id, files, l.config.Compose.Process.ReservePercent); e != nil {
				return r, e
			}
		}
		deadline := time.NewTimer(time.Duration(l.config.Compose.Process.ObserveSeconds) * time.Second)
		defer deadline.Stop()
		for {
			task, found, e := c.InspectPostgresBackupTask(ctx, r.Task)
			if e != nil || !found || task.ID != id {
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_OBSERVATION_UNRESOLVED")
			}
			if task.State.Status == "created" {
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_NEVER_STARTED_OUTCOME_UNRESOLVED")
			}
			if task.State.Status == "exited" {
				if task.State.ExitCode != 0 || task.State.OOMKilled {
					return r, failure(restoreSQLTaskFailed)
				}
				return r, nil
			}
			poll := time.NewTimer(time.Duration(l.config.Compose.Process.PollMilliseconds) * time.Millisecond)
			select {
			case <-ctx.Done():
				poll.Stop()
				return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
			case <-deadline.C:
				poll.Stop()
				return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_DEADLINE")
			case <-poll.C:
			}
		}
	case "result-verify":
		if !utilityIDPattern.MatchString(id) {
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_ID_REQUIRED")
		}
		observed, e := c.InspectPostgresRestoreResult(ctx, r.Task, id)
		if e != nil {
			return r, e
		}
		r.ResultSHA256 = wire.Digest(encode(observed))
	case "task-remove":
		if !utilityIDPattern.MatchString(id) {
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_ID_REQUIRED")
		}
		if !reconcile {
			if e := c.RemovePostgresBackupTask(ctx, r.Task, id); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectPostgresBackupTask(ctx, r.Task); e != nil || found {
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_TASK_REMOVAL_UNRESOLVED")
		}
	case "complete":
		for _, completed := range restoreSQLState(l.events).completed {
			if completed.Phase == "result-verify" {
				r.ResultSHA256 = completed.ResultSHA256
			}
		}
		if !wire.ValidSHA256(r.ResultSHA256) {
			return r, failure("OFFLINE_SCOPE_RESTORE_SQL_RESULT_PROOF_REQUIRED")
		}
	default:
		return r, failure("OFFLINE_SCOPE_RESTORE_SQL_STEP_INVALID")
	}
	return r, nil
}
