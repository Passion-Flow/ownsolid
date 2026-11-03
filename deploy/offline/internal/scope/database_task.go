package scope

import (
	"context"
	"maps"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type databaseTaskEngine interface {
	runtimeEngine
	CreateDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, error)
	InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error)
	InspectDatabaseTaskInput(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
}
type DatabaseTaskResult struct {
	ScopeID       string `json:"scope_id"`
	ID            string `json:"id,omitempty"`
	Status        string `json:"status"`
	Pending       bool   `json:"pending"`
	Qualification string `json:"qualification"`
}

// This view is constructed only after independent exact task validation. It is
// never exposed as an arbitrary ignored-ID API to Compose or its callers.
type databaseTaskRuntimeView struct {
	runtimeEngine
	task   engine.DatabaseTask
	object engine.RuntimeContainer
}

func (v databaseTaskRuntimeView) InspectRuntimeNetwork(ctx context.Context, scopeID string) (engine.RuntimeNetwork, bool, error) {
	n, found, e := v.runtimeEngine.InspectRuntimeNetwork(ctx, scopeID)
	if e != nil || !found {
		return n, found, e
	}
	if scopeID != v.task.Owner.ScopeID || n.ID != v.task.NetworkID {
		return n, false, failure("OFFLINE_SCOPE_DATABASE_TASK_VIEW_NETWORK_CHANGED")
	}
	n.Containers = maps.Clone(n.Containers)
	if endpoint, ok := n.Containers[v.object.ID]; ok {
		if endpoint.Name != strings.TrimPrefix(v.object.Name, "/") {
			return n, false, failure("OFFLINE_SCOPE_DATABASE_TASK_VIEW_ENDPOINT_CHANGED")
		}
		delete(n.Containers, v.object.ID)
	}
	return n, found, nil
}
func (v databaseTaskRuntimeView) RuntimeCandidates(ctx context.Context, scopeID string, networks bool) ([]engine.RuntimeObject, error) {
	objects, e := v.runtimeEngine.RuntimeCandidates(ctx, scopeID, networks)
	if e != nil || networks {
		return objects, e
	}
	if scopeID != v.task.Owner.ScopeID {
		return nil, failure("OFFLINE_SCOPE_DATABASE_TASK_VIEW_SCOPE_CHANGED")
	}
	result := []engine.RuntimeObject{}
	seen := false
	for _, o := range objects {
		if o.ID == v.object.ID {
			if seen {
				return nil, failure("OFFLINE_SCOPE_DATABASE_TASK_VIEW_DUPLICATE")
			}
			seen = true
			continue
		}
		result = append(result, o)
	}
	return result, nil
}
func (v databaseTaskRuntimeView) VerifyVolumeConsumers(ctx context.Context, owner engine.Ownership, allowed []string) error {
	if owner == v.task.VolumeOwner || v.task.OutputOwner != nil && owner == *v.task.OutputOwner || v.task.LocalizationOwner != nil && owner == *v.task.LocalizationOwner {
		allowed = append(append([]string{}, allowed...), v.object.ID)
	}
	return v.runtimeEngine.VerifyVolumeConsumers(ctx, owner, allowed)
}

func CreateDatabaseMigrationTask(ctx context.Context, c configuration.Config, reconcile bool) (DatabaseTaskResult, error) {
	return createDatabaseTaskClient(ctx, c, engine.DatabaseMigrate, reconcile)
}
func CreateDatabaseSchemaTask(ctx context.Context, c configuration.Config, reconcile bool) (DatabaseTaskResult, error) {
	return createDatabaseTaskClient(ctx, c, engine.DatabaseSchemaCheck, reconcile)
}
func CreateDatabaseAccessTask(ctx context.Context, c configuration.Config, kind string, reconcile bool) (DatabaseTaskResult, error) {
	if kind != engine.DatabaseRuntimePrepare && kind != engine.DatabaseRuntimeCheck {
		return DatabaseTaskResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	return createDatabaseTaskClient(ctx, c, kind, reconcile)
}
func createDatabaseTaskClient(ctx context.Context, c configuration.Config, kind string, reconcile bool) (DatabaseTaskResult, error) {
	worker, e := compose.New(c)
	if e != nil {
		return DatabaseTaskResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return DatabaseTaskResult{}, e
	}
	defer client.Close()
	return createDatabaseTask(ctx, c, client, worker, kind, reconcile)
}
func (l *locked) databaseMigrationContext(ctx context.Context, client databaseTaskEngine, worker runtimeOperator, operationID string, knownID string) (DatabaseTaskRecord, engine.RuntimeContainer, bool, error) {
	return l.databaseTaskContext(ctx, client, worker, engine.DatabaseMigrate, operationID, knownID, true, true)
}
func (l *locked) databaseTaskContext(ctx context.Context, client databaseTaskEngine, worker runtimeOperator, kind string, operationID string, knownID string, createdOnly, requireRunningDatabase bool) (DatabaseTaskRecord, engine.RuntimeContainer, bool, error) {
	var result DatabaseTaskRecord
	input, outcome, _ := inputsState(l.events)
	runtime := runtimeState(l.events)
	volumes := volumeState(l.events)
	process := processState(l.events)
	volume, exists := volumes.roles["operator-config"]
	if input == nil || outcome != "completed" || !runtime.completed || runtime.record == nil || runtime.pending != "" || !exists || volume.Phase != phasePrepared || process.pending != "" || modelTaskState(l.events).pending != "" || modelTaskActionState(l.events).pending != "" {
		return result, engine.RuntimeContainer{}, false, failure("OFFLINE_SCOPE_DATABASE_TASK_PREPARATION_REQUIRED")
	}
	peers := map[string]string{}
	for name, s := range runtime.record.Services {
		peers[name] = s.ID
	}
	result = DatabaseTaskRecord{DescriptorSHA256: input.DescriptorSHA256, VolumeSHA256: wire.Digest(encode(volume)), RuntimeSHA256: wire.Digest(encode(*runtime.record)), Task: engine.DatabaseTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: operationID, Role: "database-" + kind}, VolumeOwner: l.volumeOwner(volume), Kind: kind, Image: input.ImageID, UID: volume.UID, GID: volume.GID, NetworkID: runtime.record.NetworkID, Peers: peers}, ID: knownID}
	if kind == engine.DatabaseSchemaCheck {
		result.MigrationSHA256, _ = migrationProof(l.events)
	}
	if kind == engine.DatabaseRuntimePrepare || kind == engine.DatabaseRuntimeCheck {
		result.SchemaSHA256, _ = schemaProof(l.events)
	}
	if kind == engine.DatabaseRuntimeCheck {
		result.AccessSHA256, _ = accessPreparationProof(l.events)
	}
	if bootstrapKind(kind) {
		output, ok := volumes.roles[engine.BootstrapStateRole]
		if !ok || output.Phase != phasePrepared {
			return result, engine.RuntimeContainer{}, false, failure("OFFLINE_SCOPE_BOOTSTRAP_STATE_REQUIRED")
		}
		owner := l.volumeOwner(output)
		result.Task.OutputOwner = &owner
		result.OutputVolumeSHA256 = wire.Digest(encode(output))
		result.PreviousTaskSHA256, _ = bootstrapPredecessorProof(beforeDatabaseTaskCreation(l.events, operationID), kind)
		result.SchemaSHA256, _ = schemaProof(l.events)
		result.AccessSHA256, _ = completedDatabaseTaskProof(l.events, engine.DatabaseRuntimeCheck)
	}
	if role, mode, ok := engine.StoreTaskContext(kind); ok {
		output, exists := volumes.roles[role]
		if !exists || output.Phase != phasePrepared {
			return result, engine.RuntimeContainer{}, false, failure("OFFLINE_SCOPE_PREPARED_STORE_REQUIRED")
		}
		request, e := l.readInputs(ctx, *input)
		if e != nil {
			return result, engine.RuntimeContainer{}, false, e
		}
		operation, e := storeRequest(request.files["operator-config"])
		if e != nil {
			return result, engine.RuntimeContainer{}, false, e
		}
		owner := l.volumeOwner(output)
		result.Task.OutputOwner = &owner
		if mode != "check" {
			result.Task.Reason = operation.Reason
		}
		result.OutputVolumeSHA256 = wire.Digest(encode(output))
		result.PreviousTaskSHA256, _ = storePredecessorProof(beforeDatabaseTaskCreation(l.events, operationID), kind)
		result.SchemaSHA256, _ = schemaProof(l.events)
		result.AccessSHA256, _ = completedDatabaseTaskProof(l.events, engine.DatabaseRuntimeCheck)
	}
	if !validDatabaseTaskRecord(l.events, &result, l.marker.ScopeID) || requireRunningDatabase && !databaseCreationProcessReady(l.events) {
		return result, engine.RuntimeContainer{}, false, failure("OFFLINE_SCOPE_DATABASE_ONLY_RUNNING_PROCESS_REQUIRED")
	}
	object, found, e := client.InspectDatabaseTask(ctx, result.Task)
	if e != nil {
		return result, object, found, e
	}
	if found && (knownID != "" && object.ID != knownID || createdOnly && !neverStartedModelTask(object)) {
		return result, object, found, failure("OFFLINE_SCOPE_DATABASE_TASK_MISSING_OR_CHANGED")
	}
	var reader runtimeEngine = client
	if found {
		reader = databaseTaskRuntimeView{runtimeEngine: client, task: result.Task, object: object}
	}
	verified, e := l.verifyRuntimeContext(ctx, reader, worker, false)
	if e != nil {
		return result, object, found, e
	}
	if verified.model.Services["control"].Image != result.Task.Image {
		return result, object, found, failure("OFFLINE_SCOPE_DATABASE_TASK_CONTROL_IMAGE_REQUIRED")
	}
	configFound, runtimeFound, bootstrapFound := false, false, false
	for _, file := range verified.inputs.files["operator-config"] {
		if file.Name == "control.yaml" {
			configFound = true
		}
		if file.Name == "bootstrap-request.json" {
			bootstrapFound = true
		}
		if file.Name == "runtime.yaml" {
			runtimeFound = true
		}
	}
	if !configFound || (kind == engine.DatabaseRuntimePrepare || kind == engine.DatabaseRuntimeCheck) && !runtimeFound || bootstrapKind(kind) && !bootstrapFound {
		return result, object, found, failure("OFFLINE_SCOPE_DATABASE_TASK_CONFIG_REQUIRED")
	}
	if requireRunningDatabase && verified.observed.Services["postgres"].State != "running" {
		return result, object, found, failure("OFFLINE_SCOPE_DATABASE_RUNNING_REQUIRED")
	}
	for name, s := range verified.observed.Services {
		if name != "postgres" && s.State != "created" && s.State != "exited" {
			return result, object, found, failure("OFFLINE_SCOPE_STOPPED_APPLICATION_REQUIRED")
		}
	}
	if found && !object.State.Running {
		if _, e = client.InspectDatabaseTaskInput(ctx, result.Task, object.ID, verified.inputs.files["operator-config"]); e != nil {
			return result, object, found, e
		}
	}
	return result, object, found, nil
}
func createDatabaseMigrationTask(ctx context.Context, c configuration.Config, client databaseTaskEngine, worker runtimeOperator, reconcile bool) (DatabaseTaskResult, error) {
	return createDatabaseTask(ctx, c, client, worker, engine.DatabaseMigrate, reconcile)
}
func createDatabaseTask(ctx context.Context, c configuration.Config, client databaseTaskEngine, worker runtimeOperator, kind string, reconcile bool) (DatabaseTaskResult, error) {
	result := DatabaseTaskResult{Qualification: "never_started_database_task_creation_only_not_migrated_schema_application_or_target"}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return result, e
	}
	defer l.close()
	result.ScopeID = l.marker.ScopeID
	state := databaseTaskState(l.events)
	if kind != engine.DatabaseMigrate && kind != engine.DatabaseSchemaCheck && kind != engine.DatabaseRuntimePrepare && kind != engine.DatabaseRuntimeCheck && !bootstrapKind(kind) && !storeKind(kind) {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	advancing := state.record != nil && (nextDatabaseTaskKind(state.record.Task.Kind, kind) || bootstrapTransition(l.events, kind) || storeTransition(l.events, kind)) && state.pending == "" && databaseTaskActionState(l.events).removed
	if advancing {
		state = databaseTaskRecords{}
	} else if state.record != nil && state.record.Task.Kind != kind {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_MISMATCH")
	}
	result.Pending = state.pending != ""
	if state.pending != "" && !reconcile {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_RECONCILIATION_REQUIRED")
	}
	if reconcile && state.pending == "" && state.record == nil {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_NOT_RECORDED")
	}
	op, known := "", ""
	if state.record != nil {
		op = state.record.Task.Owner.OperationID
		known = state.record.ID
	} else {
		op, e = nonce()
		if e != nil {
			return result, e
		}
	}
	current, observed, found, e := l.databaseTaskContext(ctx, client, worker, kind, op, known, true, true)
	if e != nil {
		return result, e
	}
	if state.record != nil {
		if !databaseTaskIdentity(current, *state.record) {
			return result, failure("OFFLINE_SCOPE_DATABASE_TASK_INPUT_MISMATCH")
		}
		if state.pending == "" {
			if !found {
				return result, failure("OFFLINE_SCOPE_RECORDED_DATABASE_TASK_MISSING")
			}
			result.ID = observed.ID
			result.Status = "observed_recorded_never_started_database_task"
			return result, nil
		}
		if e = admitProcessJournal(l, 1); e != nil {
			return result, e
		}
		id, e := nonce()
		if e != nil {
			return result, e
		}
		outcome, code := "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_OUTCOME_UNKNOWN"
		if found {
			current.ID = observed.ID
			outcome = "completed"
			code = ""
		}
		if e = l.appendEvent(Event{OperationID: id, Action: "database_task_reconcile", ReferenceOperationID: state.pending, Outcome: outcome, Code: code, DatabaseTask: &current}); e != nil {
			return result, e
		}
		result.ID = current.ID
		result.Pending = outcome != "completed"
		result.Status = "observed_recorded_never_started_database_task"
		if result.Pending {
			result.Status = "database_task_creation_uncertain"
			return result, failure(code)
		}
		return result, nil
	}
	if found {
		return result, failure("OFFLINE_SCOPE_UNRECORDED_DATABASE_TASK_PRESERVED")
	}
	if e = admitProcessJournal(l, 2); e != nil {
		return result, e
	}
	if e = l.appendEvent(Event{OperationID: op, Action: "database_task_create", Outcome: "started", DatabaseTask: &current}); e != nil {
		return result, e
	}
	result.Pending = true
	created, requestError := client.CreateDatabaseTask(ctx, current.Task)
	if utilityIDPattern.MatchString(created.ID) {
		current.ID = created.ID
	}
	if requestError == nil {
		var found bool
		observed, found, e = client.InspectDatabaseTask(ctx, current.Task)
		if e != nil || !found || observed.ID != current.ID || !neverStartedModelTask(observed) {
			requestError = failure("OFFLINE_SCOPE_DATABASE_TASK_CREATE_UNCERTAIN")
		}
	}
	if requestError == nil {
		// Revalidate all bytes, consumers and runtime identities after creation.
		if _, _, _, e = l.databaseTaskContext(ctx, client, worker, kind, op, current.ID, true, true); e != nil {
			requestError = e
		}
	}
	outcome, code := "completed", ""
	if requestError != nil {
		outcome = "uncertain"
		code = "OFFLINE_SCOPE_DATABASE_TASK_CREATE_UNCERTAIN"
	}
	if e = l.appendEvent(Event{OperationID: op, Action: "database_task_create", Outcome: outcome, Code: code, DatabaseTask: &current}); e != nil {
		return result, e
	}
	result.ID = current.ID
	result.Pending = requestError != nil
	result.Status = "observed_recorded_never_started_database_task"
	if requestError != nil {
		result.Status = "database_task_creation_uncertain"
		return result, failure(code)
	}
	return result, nil
}
