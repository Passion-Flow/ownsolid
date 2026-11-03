package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type restoreDatabaseEngine interface {
	storageEngine
	runtimeEngine
	ValidateConfigurationFiles([]engine.ConfigurationFile, int, int) error
	CreateDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, error)
	InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error)
	StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
	StopDatabaseTask(context.Context, engine.DatabaseTask, string, int) error
	RemoveDatabaseTask(context.Context, engine.DatabaseTask, string) error
	RemoveStoreRestoreVolume(context.Context, engine.Ownership) error
}

var _ restoreDatabaseEngine = (*engine.Client)(nil)

type restoredInputRuntimeView struct {
	runtimeEngine
	scopeID, utilityID string
}

func (v restoredInputRuntimeView) RuntimeCandidates(ctx context.Context, scopeID string, networks bool) ([]engine.RuntimeObject, error) {
	objects, e := v.runtimeEngine.RuntimeCandidates(ctx, scopeID, networks)
	if e != nil || networks {
		return objects, e
	}
	if scopeID != v.scopeID {
		return nil, failure("OFFLINE_SCOPE_RESTORE_DATABASE_VIEW_SCOPE_CHANGED")
	}
	result := []engine.RuntimeObject{}
	seen := false
	for _, object := range objects {
		if object.ID == v.utilityID {
			if seen {
				return nil, failure("OFFLINE_SCOPE_RESTORE_DATABASE_VIEW_DUPLICATE")
			}
			seen = true
			continue
		}
		result = append(result, object)
	}
	return result, nil
}
func restoreDatabaseIntentSeen(events []Event, phase, kind string) bool {
	for _, e := range events {
		if e.RestoreDatabase != nil && e.Action == "restore_database_effect" && e.RestoreDatabase.Phase == phase && e.RestoreDatabase.Kind == kind {
			return true
		}
	}
	return false
}
func restoreDatabaseStepCompleted(events []Event, phase, kind string) bool {
	for _, r := range restoreDatabaseState(events).completed {
		if r.Phase == phase && r.Kind == kind {
			return true
		}
	}
	return false
}
func restoreDatabaseTaskID(events []Event, kind string) string {
	for _, r := range restoreDatabaseState(events).completed {
		if r.Phase == "task-create" && r.Kind == kind {
			return r.ObjectID
		}
	}
	return ""
}
func (l *locked) restoreDatabaseRuntimeView(ctx context.Context, c restoreDatabaseEngine, r RestoreDatabaseRecord) (runtimeEngine, error) {
	var view runtimeEngine = c
	if restoreDatabaseIntentSeen(l.events, "utility-create", "") && !restoreDatabaseStepCompleted(l.events, "utility-remove", "") {
		utility, found, e := c.InspectUtility(ctx, r.BaseTask.VolumeOwner, r.Initializer, r.BaseTask.UID, r.BaseTask.GID)
		if e != nil {
			return nil, e
		}
		if found {
			view = restoredInputRuntimeView{runtimeEngine: view, scopeID: l.marker.ScopeID, utilityID: utility.ID}
		}
	}
	for _, step := range restoreDatabaseSteps() {
		if step.phase != "task-create" || !restoreDatabaseIntentSeen(l.events, "task-create", step.kind) || restoreDatabaseStepCompleted(l.events, "task-remove", step.kind) {
			continue
		}
		task := r.task(step.kind)
		object, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil {
			return nil, e
		}
		if found {
			view = databaseTaskRuntimeView{runtimeEngine: view, task: task, object: object}
		}
	}
	return view, nil
}
