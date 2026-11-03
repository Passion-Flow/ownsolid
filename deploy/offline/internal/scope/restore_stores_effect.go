package scope

import (
	"context"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

const restoreStoresTaskFailed = "OFFLINE_SCOPE_RESTORE_STORES_TASK_FAILED"

func (l *locked) executeRestoreStoresStep(ctx context.Context, c restoreStoresEngine, p RestoreStorePlan, r RestoreStoresRecord, files []engine.ConfigurationFile, sourceRoot string, reconcile bool) (RestoreStoresRecord, error) {
	if ctx.Err() != nil {
		return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if r.Phase == "task-create" || r.Phase == "task-execute" || r.Phase == "task-remove" {
		task, found := restoreStoresTask(p, r.Role, r.Kind)
		if !found {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_TASK_PROFILE_REQUIRED")
		}
		objectID, e := l.executeRestoreTask(ctx, c, task, r.Phase, restoreStoresTaskID(l.events, r.Role, r.Kind), files, reconcile)
		r.ObjectID = objectID
		if e != nil && strings.HasPrefix(e.Error(), "OFFLINE_SCOPE_RESTORE_DATABASE_") {
			e = failure(strings.Replace(e.Error(), "OFFLINE_SCOPE_RESTORE_DATABASE_", "OFFLINE_SCOPE_RESTORE_STORES_", 1))
		}
		return r, e
	}
	if r.Phase == "complete" {
		return r, nil
	}
	owner, found := restoreStoresVolume(p, r.Role)
	if !found {
		return r, failure("OFFLINE_SCOPE_RESTORE_STORES_VOLUME_PROFILE_REQUIRED")
	}
	uid, gid := p.Jobs[0].Import.UID, p.Jobs[0].Import.GID
	switch r.Phase {
	case "volume-create":
		if !reconcile {
			if _, e := c.CreateVolume(ctx, owner); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_VOLUME_UNRESOLVED")
		}
	case "utility-create":
		if !reconcile {
			if _, e := c.CreateUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
				return r, e
			}
		}
		utility, found, e := c.InspectUtility(ctx, owner, p.Initializer, uid, gid)
		if e != nil || !found {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_UTILITY_UNRESOLVED")
		}
		r.ObjectID = utility.ID
	case "payload-write":
		if r.Role != "configuration" {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_CONFIGURATION_ROLE_REQUIRED")
		}
		if !reconcile {
			if e := c.PutConfiguration(ctx, owner, p.Initializer, uid, gid, files); e != nil {
				return r, e
			}
		}
		observed, e := c.InspectConfiguration(ctx, owner, p.Initializer, uid, gid, files)
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_PAYLOAD_UNRESOLVED")
		}
	case "archive-write":
		var task engine.DatabaseTask
		for _, job := range p.Jobs {
			if job.Role == r.Role {
				task = job.Import
			}
		}
		if task.StoreRestore == nil {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_PROFILE_REQUIRED")
		}
		if reconcile {
			if _, e := c.InspectStoreRestoreArchiveInput(ctx, task, p.Initializer); e != nil {
				return r, e
			}
			return r, nil
		}
		root, e := openBackupDirectory(sourceRoot)
		if e != nil {
			return r, e
		}
		defer root.Close()
		f, e := openRestoreStoreArchive(root, r.Role)
		if e != nil {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_UNAVAILABLE")
		}
		_, e = c.PutStoreRestoreArchive(ctx, task, p.Initializer, f)
		closeError := f.Close()
		if e != nil {
			return r, e
		}
		if closeError != nil {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_CLOSE_FAILED")
		}
	case "utility-remove":
		if !reconcile {
			if e := c.RemoveUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, uid, gid); e != nil || found {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_UTILITY_REMOVAL_UNRESOLVED")
		}
	case "volume-remove":
		if !reconcile {
			if e := c.RemoveStoreRestoreVolume(ctx, owner); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
			return r, failure("OFFLINE_SCOPE_RESTORE_STORES_VOLUME_REMOVAL_UNRESOLVED")
		}
	default:
		return r, failure("OFFLINE_SCOPE_RESTORE_STORES_STEP_INVALID")
	}
	return r, nil
}
