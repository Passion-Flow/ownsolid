package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const storeRequestFormatVersion = 1

type StoreRequest struct {
	FormatVersion int    `json:"format_version"`
	Reason        string `json:"reason"`
}

var orderedStoreRoles = []string{"datasets", "training", "preparations"}

func storeKind(kind string) bool { _, _, ok := engine.StoreTaskContext(kind); return ok }
func storeCheckKind(role string) string {
	switch role {
	case "datasets":
		return engine.DatasetStoreCheck
	case "training":
		return engine.TrainingStoreCheck
	case "preparations":
		return engine.PreparationStoreCheck
	}
	return ""
}
func storeNamespace(events []Event, role string) *engine.StoreTaskObservation {
	creations := map[string]string{}
	for _, e := range events {
		if e.DatabaseTask != nil && e.Outcome == "completed" {
			r, _, ok := engine.StoreTaskContext(e.DatabaseTask.Task.Kind)
			if ok {
				creations[wire.Digest(encode(*e.DatabaseTask))] = r
			}
		}
		if e.DatabaseTaskAction != nil && e.DatabaseTaskAction.Target == "execute" && e.Outcome != "started" && e.DatabaseTaskAction.StoreNamespace != nil && creations[e.DatabaseTaskAction.CreationSHA256] == role {
			return e.DatabaseTaskAction.StoreNamespace
		}
	}
	return nil
}
func storeQualified(events []Event, role string) bool {
	_, ok := completedDatabaseTaskProof(events, storeCheckKind(role))
	return ok && bootstrapQualified(events) && storeNamespace(events, role) != nil
}
func nextDeclaredStore(events []Event, previous string) string {
	after := previous == ""
	roles := volumeState(events).roles
	for _, role := range orderedStoreRoles {
		if !after {
			if role == previous {
				after = true
			}
			continue
		}
		if _, declared := roles[role]; declared {
			return role
		}
	}
	return ""
}
func storeTransition(events []Event, next string) bool {
	role, mode, ok := engine.StoreTaskContext(next)
	if !ok || !bootstrapQualified(events) {
		return false
	}
	if _, declared := volumeState(events).roles[role]; !declared {
		return false
	}
	s, a := databaseTaskState(events), databaseTaskActionState(events)
	if s.record == nil || s.pending != "" || !a.removed || !a.everExecuted || !a.succeeded && !a.failed {
		return false
	}
	if s.record.Task.Kind == engine.DatabaseBootstrapReconcile {
		return a.succeeded && mode == "initialize" && role == nextDeclaredStore(events, "")
	}
	previousRole, previousMode, ok := engine.StoreTaskContext(s.record.Task.Kind)
	if !ok {
		return false
	}
	if previousMode == "initialize" || previousMode == "record" {
		return role == previousRole && mode == "check"
	}
	if a.failed {
		return role == previousRole && mode == "record"
	}
	return a.succeeded && storeQualified(events, previousRole) && mode == "initialize" && role == nextDeclaredStore(events, previousRole)
}
func storePredecessorProof(events []Event, next string) (string, bool) {
	if !storeTransition(events, next) {
		return "", false
	}
	return databaseTaskTerminalProof(events, databaseTaskState(events).record.Task.Kind, false)
}
func storeRequest(files []engine.ConfigurationFile) (StoreRequest, error) {
	var result StoreRequest
	found := false
	for _, f := range files {
		if f.Name == "store-request.json" {
			if found || wire.StrictJSON(f.Contents, &result) != nil {
				return result, failure("OFFLINE_SCOPE_STORE_REQUEST_INVALID")
			}
			found = true
		}
	}
	if !found || result.FormatVersion != storeRequestFormatVersion || !engine.ValidStoreTaskReason(result.Reason) {
		return result, failure("OFFLINE_SCOPE_STORE_REQUEST_INVALID")
	}
	return result, nil
}
func CreateStoreTask(ctx context.Context, c configuration.Config, kind string, reconcile bool) (DatabaseTaskResult, error) {
	if !storeKind(kind) {
		return DatabaseTaskResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	return createDatabaseTaskClient(ctx, c, kind, reconcile)
}
func StoreTaskAction(ctx context.Context, c configuration.Config, kind, target string, reconcile bool) (DatabaseTaskActionResult, error) {
	if !storeKind(kind) {
		return DatabaseTaskActionResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	return databaseTaskActionClient(ctx, c, target, reconcile, kind)
}

func verifyTaskOutputConsumers(ctx context.Context, client databaseTaskActionEngine, t engine.DatabaseTask) error {
	allowed := []string{}
	if storeKind(t.Kind) {
		allowed = append(allowed, t.Peers["control"])
	}
	return client.VerifyVolumeConsumers(ctx, *t.OutputOwner, allowed)
}

type StoreStatus struct {
	Qualified bool                         `json:"qualified"`
	Namespace *engine.StoreTaskObservation `json:"namespace,omitempty"`
}

func storeStatuses(events []Event) map[string]StoreStatus {
	result := map[string]StoreStatus{}
	roles := volumeState(events).roles
	for _, role := range orderedStoreRoles {
		if _, ok := roles[role]; ok {
			result[role] = StoreStatus{Qualified: storeQualified(events, role), Namespace: storeNamespace(events, role)}
		}
	}
	return result
}
