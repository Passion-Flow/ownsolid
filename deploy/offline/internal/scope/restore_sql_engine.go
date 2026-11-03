package scope

import (
	"context"
	"maps"
	"os"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type restoreSQLEngine interface {
	storageEngine
	runtimeEngine
	CreatePostgresBackupTask(context.Context, engine.PostgresBackupTask) (engine.RuntimeContainer, error)
	InspectPostgresBackupTask(context.Context, engine.PostgresBackupTask) (engine.RuntimeContainer, bool, error)
	StartPostgresBackupTask(context.Context, engine.PostgresBackupTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
	StopPostgresBackupTask(context.Context, engine.PostgresBackupTask, string, int) error
	RemovePostgresBackupTask(context.Context, engine.PostgresBackupTask, string) error
	ValidatePostgresRestoreArchive(context.Context, engine.PostgresBackupTask, *os.File) (engine.PostgresBackupArchive, error)
	PutPostgresRestoreArchive(context.Context, engine.PostgresBackupTask, string, *os.File) (engine.PostgresBackupArchive, error)
	InspectPostgresRestoreArchiveInput(context.Context, engine.PostgresBackupTask, string) (engine.PostgresBackupArchive, error)
	InspectPostgresRestoreResult(context.Context, engine.PostgresBackupTask, string) (engine.PostgresRestoreObservation, error)
	RemovePostgresRestoreVolume(context.Context, engine.Ownership) error
}

var _ restoreSQLEngine = (*engine.Client)(nil)

// Only actors independently inspected through their exact fixed SDK profile
// are masked. Unknown candidates and endpoints remain visible to Compose.
type restoreSQLRuntimeView struct {
	runtimeEngine
	ignored                            map[string]bool
	scopeID, networkID, sqlID, sqlName string
}

func (v restoreSQLRuntimeView) RuntimeCandidates(ctx context.Context, scopeID string, networks bool) ([]engine.RuntimeObject, error) {
	objects, e := v.runtimeEngine.RuntimeCandidates(ctx, scopeID, networks)
	if e != nil || networks {
		return objects, e
	}
	if scopeID != v.scopeID {
		return nil, failure("OFFLINE_SCOPE_RESTORE_SQL_VIEW_SCOPE_CHANGED")
	}
	result := []engine.RuntimeObject{}
	for _, object := range objects {
		if !v.ignored[object.ID] {
			result = append(result, object)
		}
	}
	return result, nil
}

func (v restoreSQLRuntimeView) InspectRuntimeNetwork(ctx context.Context, scopeID string) (engine.RuntimeNetwork, bool, error) {
	n, found, e := v.runtimeEngine.InspectRuntimeNetwork(ctx, scopeID)
	if e != nil || !found {
		return n, found, e
	}
	if scopeID != v.scopeID || n.ID != v.networkID {
		return n, false, failure("OFFLINE_SCOPE_RESTORE_SQL_VIEW_NETWORK_CHANGED")
	}
	if endpoint, present := n.Containers[v.sqlID]; v.sqlID != "" && present {
		if endpoint.Name != strings.TrimPrefix(v.sqlName, "/") {
			return n, false, failure("OFFLINE_SCOPE_RESTORE_SQL_VIEW_ENDPOINT_CHANGED")
		}
		n.Containers = maps.Clone(n.Containers)
		delete(n.Containers, v.sqlID)
	}
	return n, found, nil
}

func restoreSQLVolume(record RestoreSQLRecord, role string) (engine.Ownership, bool) {
	switch role {
	case "input":
		return record.Task.InputOwner, true
	case "source":
		return record.Task.Restore.SourceOwner, true
	case "result":
		return record.Task.OutputOwner, true
	}
	return engine.Ownership{}, false
}

func restoreSQLIntentSeen(events []Event, phase, role string) bool {
	for _, e := range events {
		if e.RestoreSQL != nil && e.Action == "restore_sql_effect" && e.RestoreSQL.Phase == phase && e.RestoreSQL.Role == role {
			return true
		}
	}
	return false
}

func restoreSQLStepCompleted(events []Event, phase, role string) bool {
	for _, r := range restoreSQLState(events).completed {
		if r.Phase == phase && r.Role == role {
			return true
		}
	}
	return false
}

func (l *locked) restoreSQLRuntimeView(ctx context.Context, c restoreSQLEngine, record RestoreSQLRecord) (runtimeEngine, error) {
	v := restoreSQLRuntimeView{runtimeEngine: c, ignored: map[string]bool{}, scopeID: l.marker.ScopeID, networkID: record.Task.NetworkID}
	for _, role := range []string{"input", "source", "result"} {
		if !restoreSQLIntentSeen(l.events, "utility-create", role) || restoreSQLStepCompleted(l.events, "utility-remove", role) {
			continue
		}
		owner, _ := restoreSQLVolume(record, role)
		u, found, e := c.InspectUtility(ctx, owner, record.Initializer, record.Task.UID, record.Task.GID)
		if e != nil {
			return nil, e
		}
		if found {
			v.ignored[u.ID] = true
		}
	}
	if restoreSQLIntentSeen(l.events, "task-create", "postgres") && !restoreSQLStepCompleted(l.events, "task-remove", "postgres") {
		task, found, e := c.InspectPostgresBackupTask(ctx, record.Task)
		if e != nil {
			return nil, e
		}
		if found {
			v.ignored[task.ID] = true
			v.sqlID = task.ID
			v.sqlName = task.Name
		}
	}
	return v, nil
}

func restoreSQLTaskID(events []Event) string {
	for _, r := range restoreSQLState(events).completed {
		if r.Phase == "task-create" {
			return r.ObjectID
		}
	}
	return ""
}
