package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

const restoreDatabaseTaskFailed = "OFFLINE_SCOPE_RESTORE_DATABASE_TASK_FAILED"

func (l *locked) executeRestoreDatabaseStep(ctx context.Context, c restoreDatabaseEngine, r RestoreDatabaseRecord, files []engine.ConfigurationFile, reconcile bool) (RestoreDatabaseRecord, error) {
	if ctx.Err() != nil {
		return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	owner := r.BaseTask.VolumeOwner
	uid, gid := r.BaseTask.UID, r.BaseTask.GID
	switch r.Phase {
	case "volume-create":
		if !reconcile {
			if _, e := c.CreateVolume(ctx, owner); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
			return r, failure("OFFLINE_SCOPE_RESTORE_DATABASE_VOLUME_UNRESOLVED")
		}
	case "utility-create":
		if !reconcile {
			if _, e := c.CreateUtility(ctx, owner, r.Initializer, uid, gid); e != nil {
				return r, e
			}
		}
		utility, found, e := c.InspectUtility(ctx, owner, r.Initializer, uid, gid)
		if e != nil || !found {
			return r, failure("OFFLINE_SCOPE_RESTORE_DATABASE_UTILITY_UNRESOLVED")
		}
		r.ObjectID = utility.ID
	case "payload-write":
		if !reconcile {
			if e := c.PutConfiguration(ctx, owner, r.Initializer, uid, gid, files); e != nil {
				return r, e
			}
		}
		observed, e := c.InspectConfiguration(ctx, owner, r.Initializer, uid, gid, files)
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return r, failure("OFFLINE_SCOPE_RESTORE_DATABASE_PAYLOAD_UNRESOLVED")
		}
	case "utility-remove":
		if !reconcile {
			if e := c.RemoveUtility(ctx, owner, r.Initializer, uid, gid); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectUtility(ctx, owner, r.Initializer, uid, gid); e != nil || found {
			return r, failure("OFFLINE_SCOPE_RESTORE_DATABASE_UTILITY_REMOVAL_UNRESOLVED")
		}
	case "task-create", "task-execute", "task-remove":
		objectID, e := l.executeRestoreTask(ctx, c, r.task(r.Kind), r.Phase, restoreDatabaseTaskID(l.events, r.Kind), files, reconcile)
		r.ObjectID = objectID
		return r, e
	case "volume-remove":
		if !reconcile {
			if e := c.RemoveStoreRestoreVolume(ctx, owner); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
			return r, failure("OFFLINE_SCOPE_RESTORE_DATABASE_VOLUME_REMOVAL_UNRESOLVED")
		}
	case "complete":
	default:
		return r, failure("OFFLINE_SCOPE_RESTORE_DATABASE_STEP_INVALID")
	}
	return r, nil
}
