package scope

import (
	"context"
	"os"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type restoreStoresEngine interface {
	restoreDatabaseEngine
	PutStoreRestoreArchive(context.Context, engine.DatabaseTask, string, *os.File) (engine.StoreRestoreProfile, error)
	InspectStoreRestoreArchiveInput(context.Context, engine.DatabaseTask, string) (engine.StoreRestoreProfile, error)
}

var _ restoreStoresEngine = (*engine.Client)(nil)

func restoreStoresIntentSeen(events []Event, phase, role, kind string) bool {
	for _, e := range events {
		if e.RestoreStores != nil && e.Action == "restore_stores_effect" && e.RestoreStores.Phase == phase && e.RestoreStores.Role == role && e.RestoreStores.Kind == kind {
			return true
		}
	}
	return false
}
func restoreStoresStepCompleted(events []Event, phase, role, kind string) bool {
	for _, r := range restoreStoresState(events).completed {
		if r.Phase == phase && r.Role == role && r.Kind == kind {
			return true
		}
	}
	return false
}
func restoreStoresTaskID(events []Event, role, kind string) string {
	for _, r := range restoreStoresState(events).completed {
		if r.Phase == "task-create" && r.Role == role && r.Kind == kind {
			return r.ObjectID
		}
	}
	return ""
}
func restoreStoresVolume(p RestoreStorePlan, role string) (engine.Ownership, bool) {
	if len(p.Jobs) == 0 {
		return engine.Ownership{}, false
	}
	if role == "configuration" {
		return p.Jobs[0].Import.VolumeOwner, true
	}
	for _, job := range p.Jobs {
		if job.Role == role && job.Import.SourceOwner != nil {
			return *job.Import.SourceOwner, true
		}
	}
	return engine.Ownership{}, false
}
func restoreStoresTask(p RestoreStorePlan, role, kind string) (engine.DatabaseTask, bool) {
	for _, job := range p.Jobs {
		if job.Role != role {
			continue
		}
		if kind == job.Import.Kind {
			return job.Import, true
		}
		if kind == job.Verify.Kind {
			return job.Verify, true
		}
	}
	return engine.DatabaseTask{}, false
}
func (l *locked) restoreStoresRuntimeView(ctx context.Context, c restoreStoresEngine, p RestoreStorePlan) (runtimeEngine, error) {
	var view runtimeEngine = c
	uid, gid := p.Jobs[0].Import.UID, p.Jobs[0].Import.GID
	roles := []string{"configuration"}
	for _, job := range p.Jobs {
		roles = append(roles, job.Role)
	}
	// Each exact utility is masked separately; every unknown actor remains visible.
	for _, role := range roles {
		if !restoreStoresIntentSeen(l.events, "utility-create", role, "") || restoreStoresStepCompleted(l.events, "utility-remove", role, "") {
			continue
		}
		owner, _ := restoreStoresVolume(p, role)
		utility, found, e := c.InspectUtility(ctx, owner, p.Initializer, uid, gid)
		if e != nil {
			return nil, e
		}
		if found {
			view = restoredInputRuntimeView{runtimeEngine: view, scopeID: l.marker.ScopeID, utilityID: utility.ID}
		}
	}
	for _, job := range p.Jobs {
		for _, task := range []engine.DatabaseTask{job.Import, job.Verify} {
			if !restoreStoresIntentSeen(l.events, "task-create", job.Role, task.Kind) || restoreStoresStepCompleted(l.events, "task-remove", job.Role, task.Kind) {
				continue
			}
			object, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil {
				return nil, e
			}
			if found {
				view = databaseTaskRuntimeView{runtimeEngine: view, task: task, object: object}
			}
		}
	}
	return view, nil
}
