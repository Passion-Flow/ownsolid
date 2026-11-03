package scope

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type databaseTaskActionEngine interface {
	databaseTaskEngine
	AdmitRuntimeResources(context.Context, engine.ResourceRequirement, int) (engine.ResourceAdmission, error)
	StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
	StopDatabaseTask(context.Context, engine.DatabaseTask, string, int) error
	RemoveDatabaseTask(context.Context, engine.DatabaseTask, string) error
	VerifyVolumeConsumers(context.Context, engine.Ownership, []string) error
}
type DatabaseTaskActionResult struct {
	BootstrapQualified     bool   `json:"bootstrap_qualified"`
	Kind                   string `json:"kind"`
	SchemaQualified        bool   `json:"schema_qualified"`
	RuntimeAccessQualified bool   `json:"runtime_access_qualified"`
	Status                 string `json:"status"`
	ScopeID                string `json:"scope_id"`
	ID                     string `json:"id"`
	Pending                bool   `json:"pending"`
	ProcessSucceeded       bool   `json:"process_succeeded"`
	Removed                bool   `json:"removed"`
	Qualification          string `json:"qualification"`
}

func DatabaseTaskAction(ctx context.Context, c configuration.Config, target string, reconcile bool) (DatabaseTaskActionResult, error) {
	return databaseTaskActionClient(ctx, c, target, reconcile, engine.DatabaseMigrate)
}
func DatabaseSchemaTaskAction(ctx context.Context, c configuration.Config, target string, reconcile bool) (DatabaseTaskActionResult, error) {
	return databaseTaskActionClient(ctx, c, target, reconcile, engine.DatabaseSchemaCheck)
}
func DatabaseAccessTaskAction(ctx context.Context, c configuration.Config, kind, target string, reconcile bool) (DatabaseTaskActionResult, error) {
	if kind != engine.DatabaseRuntimePrepare && kind != engine.DatabaseRuntimeCheck {
		return DatabaseTaskActionResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	return databaseTaskActionClient(ctx, c, target, reconcile, kind)
}
func databaseTaskActionClient(ctx context.Context, c configuration.Config, target string, reconcile bool, kind string) (DatabaseTaskActionResult, error) {
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return DatabaseTaskActionResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return DatabaseTaskActionResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return DatabaseTaskActionResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return DatabaseTaskActionResult{}, e
	}
	return databaseTaskAction(ctx, c, client, worker, target, reconcile, kind)
}
func captureDatabaseTaskAction(ctx context.Context, l *locked, client databaseTaskActionEngine, creation DatabaseTaskRecord, files []engine.ConfigurationFile, r *DatabaseTaskActionRecord) (string, string, error) {
	observed, found, e := client.InspectDatabaseTask(ctx, creation.Task)
	if e != nil {
		return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_OBSERVATION_UNAVAILABLE", e
	}
	if !found {
		if r.Target != "removed" {
			return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_DISAPPEARED", nil
		}
		if e = client.VerifyVolumeConsumers(ctx, creation.Task.VolumeOwner, nil); e != nil {
			return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_VOLUME_CONSUMER_CONFLICT", e
		}
		if creation.Task.OutputOwner != nil {
			if e = verifyTaskOutputConsumers(ctx, client, creation.Task); e != nil {
				return "unresolved", "OFFLINE_SCOPE_BOOTSTRAP_STATE_CONSUMER_CONFLICT", e
			}
		}
		r.After = nil
		r.OOMKilled = false
		r.Absent = true
		r.InputVerified = false
		return "completed", "", nil
	}
	if observed.ID != creation.ID {
		return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_ID_CHANGED", nil
	}
	after := taskProcessSnapshot(observed)
	if !validProcessSnapshot(after) {
		return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_PROCESS_SNAPSHOT_INVALID", nil
	}
	if r.StopBefore != nil && r.StopBefore.StartedAt != after.StartedAt || r.After != nil && r.After.State != "created" && r.After.StartedAt != after.StartedAt {
		return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_EXECUTION_CHANGED", nil
	}
	r.After = &after
	r.OOMKilled = observed.State.OOMKilled
	r.Absent = false
	r.InputVerified = false
	r.BootstrapIntentSHA256 = ""
	r.StoreNamespace = nil
	if r.Target == "execute" && after.State == "exited" && after.StartedAt != r.Before.StartedAt && after.ExitCode == 0 && !r.OOMKilled {
		contents, e := client.InspectDatabaseTaskInput(ctx, creation.Task, creation.ID, files)
		if e != nil {
			return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_RESULT_UNAVAILABLE", e
		}
		var expectedBytes int64
		for _, file := range files {
			expectedBytes += int64(len(file.Contents))
		}
		if contents.Status != "verified_private_volume_payload" || contents.Files != len(files) || contents.Bytes != expectedBytes {
			return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_RESULT_CONFLICT", nil
		}
		if bootstrapKind(creation.Task.Kind) {
			reader, ok := client.(interface {
				ReadBootstrapIntent(context.Context, engine.DatabaseTask, string) (engine.BootstrapIntent, error)
			})
			if !ok {
				return "unresolved", "OFFLINE_SCOPE_BOOTSTRAP_RESULT_READER_REQUIRED", nil
			}
			intent, e := reader.ReadBootstrapIntent(ctx, creation.Task, creation.ID)
			if e != nil {
				return "unresolved", "OFFLINE_SCOPE_BOOTSTRAP_RESULT_UNAVAILABLE", e
			}
			if e = l.persistBootstrapCopy(intent); e != nil {
				return "unresolved", "OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_UNAVAILABLE", e
			}
			r.BootstrapIntentSHA256 = intent.SHA256
		}
		if role, _, ok := engine.StoreTaskContext(creation.Task.Kind); ok {
			reader, ok := client.(interface {
				InspectStoreTaskResult(context.Context, engine.DatabaseTask, string) (engine.StoreTaskObservation, error)
			})
			if !ok {
				return "unresolved", "OFFLINE_SCOPE_STORE_RESULT_READER_REQUIRED", nil
			}
			namespace, e := reader.InspectStoreTaskResult(ctx, creation.Task, creation.ID)
			if e != nil || !engine.ValidStoreTaskObservation(creation.Task.Kind, namespace) {
				return "unresolved", "OFFLINE_SCOPE_STORE_RESULT_UNAVAILABLE", e
			}
			if expected := storeNamespace(l.events, role); expected != nil && *expected != namespace {
				return "unresolved", "OFFLINE_SCOPE_STORE_NAMESPACE_CHANGED", nil
			}
			r.StoreNamespace = &namespace
		}
		r.InputVerified = true
	}
	if completedDatabaseTaskAction(r) {
		return "completed", "", nil
	}
	if failedDatabaseTaskAction(r) {
		return "failed", "OFFLINE_SCOPE_MIGRATION_PROCESS_FAILED", nil
	}
	return "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_OUTCOME_UNKNOWN", nil
}
func databaseTaskAction(ctx context.Context, c configuration.Config, client databaseTaskActionEngine, worker runtimeOperator, target string, reconcile bool, kinds ...string) (DatabaseTaskActionResult, error) {
	kind := engine.DatabaseMigrate
	if len(kinds) == 1 {
		kind = kinds[0]
	}
	if len(kinds) > 1 || kind != engine.DatabaseMigrate && kind != engine.DatabaseSchemaCheck && kind != engine.DatabaseRuntimePrepare && kind != engine.DatabaseRuntimeCheck && !bootstrapKind(kind) && !storeKind(kind) {
		return DatabaseTaskActionResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	result := DatabaseTaskActionResult{Qualification: "scoped_migration_process_only_not_independent_schema_application_installation_or_target"}
	result.Kind = kind
	if kind == engine.DatabaseSchemaCheck {
		result.Qualification = "scoped_schema_check_process_only_qualification_requires_recorded_successful_removal_not_application_or_target"
	}
	if kind == engine.DatabaseRuntimePrepare || kind == engine.DatabaseRuntimeCheck {
		result.Qualification = "scoped_runtime_access_task_process_only_qualification_requires_independent_check_and_removal_not_application_or_target"
	}
	if bootstrapKind(kind) {
		result.Qualification = "scoped_bootstrap_process_only_independent_check_and_removal_required_not_application_or_target"
	}
	if storeKind(kind) {
		result.Qualification = "scoped_store_process_only_independent_check_and_removal_required_not_application_or_target"
	}
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return result, e
	}
	if !reconcile && target != "execute" && target != "stopped" && target != "removed" {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_TARGET_INVALID")
	}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return result, e
	}
	defer l.close()
	result.ScopeID = l.marker.ScopeID
	result.SchemaQualified = schemaQualified(l.events)
	result.RuntimeAccessQualified = runtimeAccessQualified(l.events)
	result.BootstrapQualified = bootstrapQualified(l.events)
	creation := databaseTaskState(l.events)
	state := databaseTaskActionState(l.events)
	result.Pending = state.pending != ""
	result.ProcessSucceeded = state.succeeded
	result.Removed = state.removed
	if creation.pending != "" || creation.record == nil || creation.outcome != "completed" {
		return result, failure("OFFLINE_SCOPE_COMPLETED_DATABASE_TASK_CREATION_REQUIRED")
	}
	if creation.record.Task.Kind != kind {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_MISMATCH")
	}
	result.ID = creation.record.ID
	canceling := state.pending != "" && !reconcile && target == "stopped" && state.record != nil && state.record.Target == "execute" && state.record.StopBefore == nil
	if state.pending != "" && !reconcile && !canceling {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_RECONCILIATION_REQUIRED")
	}
	current, _, _, e := l.databaseTaskContext(ctx, client, worker, kind, creation.record.Task.Owner.OperationID, creation.record.ID, false, target == "execute" && !state.everExecuted && !reconcile)
	if e != nil {
		return result, e
	}
	if !databaseTaskIdentity(current, *creation.record) {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_INPUT_MISMATCH")
	}
	if e = validateDatabaseTaskActionInputs(l.events, c); e != nil {
		return result, e
	}
	input, _, _ := inputsState(l.events)
	prepared, e := l.readInputs(ctx, *input)
	if e != nil {
		return result, e
	}
	files := prepared.files["operator-config"]
	if reconcile {
		if state.pending == "" {
			result.Status = "observed_no_pending_database_task_action"
			return result, nil
		}
		if e = admitProcessJournal(l, 1); e != nil {
			return result, e
		}
		r := *state.record
		outcome, code, observeError := captureDatabaseTaskAction(ctx, l, client, *creation.record, files, &r)
		if observeError != nil {
			outcome = "unresolved"
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return result, e
		}
		id, e := nonce()
		if e != nil {
			return result, e
		}
		if e = l.appendEvent(Event{Action: "database_task_action_reconcile", OperationID: id, ReferenceOperationID: state.pending, Outcome: outcome, Code: code, DatabaseTaskAction: &r}); e != nil {
			return result, e
		}
		result.SchemaQualified = schemaQualified(l.events)
		result.RuntimeAccessQualified = runtimeAccessQualified(l.events)
		result.BootstrapQualified = bootstrapQualified(l.events)
		result.Pending = outcome == "unresolved"
		result.Status = "recorded_database_task_action_" + outcome
		if outcome == "completed" {
			if r.Target == "execute" {
				result.ProcessSucceeded = true
			}
			if r.Target == "removed" {
				result.Removed = true
			}
			return result, nil
		}
		return result, failure(code)
	}
	observed, found, e := client.InspectDatabaseTask(ctx, creation.record.Task)
	if e != nil {
		return result, e
	}
	if state.removed {
		if found {
			return result, failure("OFFLINE_SCOPE_REMOVED_DATABASE_TASK_REAPPEARED")
		}
		if e = client.VerifyVolumeConsumers(ctx, creation.record.Task.VolumeOwner, nil); e != nil {
			return result, e
		}
		if target != "removed" {
			return result, failure("OFFLINE_SCOPE_DATABASE_TASK_ALREADY_REMOVED")
		}
		if creation.record.Task.OutputOwner != nil {
			if e = verifyTaskOutputConsumers(ctx, client, creation.record.Task); e != nil {
				return result, e
			}
		}
		result.Status = "observed_recorded_database_task_removal"
		return result, nil
	}
	if !found || observed.ID != creation.record.ID {
		return result, failure("OFFLINE_SCOPE_RECORDED_DATABASE_TASK_MISSING_OR_CHANGED")
	}
	before := taskProcessSnapshot(observed)
	if !validProcessSnapshot(before) {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_PROCESS_SNAPSHOT_INVALID")
	}
	if !canceling && state.record != nil && state.record.After != nil && *state.record.After != before {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_EXECUTION_CHANGED")
	}
	if target == "execute" && state.everExecuted {
		result.Status = "observed_recorded_migration_process_outcome"
		if state.failed {
			return result, failure("OFFLINE_SCOPE_MIGRATION_PROCESS_FAILED")
		}
		if !state.succeeded {
			return result, failure("OFFLINE_SCOPE_DATABASE_TASK_EXECUTION_PRESERVED")
		}
		return result, nil
	}
	if target == "execute" && !neverStartedModelTask(observed) {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_EXECUTION_PRESERVED")
	}
	if canceling && (before.State != "running" || state.record.After != nil && (state.record.After.State == "exited" || state.record.After.State != "created" && state.record.After.StartedAt != before.StartedAt)) {
		return result, failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_RECONCILIATION_REQUIRED")
	}
	if target == "stopped" && before.State != "running" {
		result.Status = "observed_database_task_already_stopped"
		return result, nil
	}
	if target == "removed" && before.State == "running" {
		return result, failure("OFFLINE_SCOPE_RUNNING_DATABASE_TASK_PRESERVED")
	}
	r := DatabaseTaskActionRecord{CreationSHA256: wire.Digest(encode(*creation.record)), Target: target, Before: before}
	if canceling {
		r = *state.record
		r.After = nil
		r.Absent = false
		r.OOMKilled = false
		r.InputVerified = false
		r.StopBefore = &before
	}
	if target == "execute" {
		admission, e := client.AdmitRuntimeResources(ctx, engine.ResourceRequirement{MemoryBytes: c.Engine.Utility.MemoryBytes, MilliCPU: c.Engine.Utility.MilliCPU}, c.Compose.Process.ReservePercent)
		if e != nil {
			return result, e
		}
		if admission.ValidateRecorded() != nil || admission.Requirement.MemoryBytes != c.Engine.Utility.MemoryBytes || admission.Requirement.MilliCPU != c.Engine.Utility.MilliCPU || admission.ReservePercent != c.Compose.Process.ReservePercent {
			return result, failure("OFFLINE_SCOPE_DATABASE_TASK_ADMISSION_INVALID")
		}
		r.Admission = &admission
	}
	if e = admitProcessJournal(l, 3); e != nil {
		return result, e
	}
	id, e := nonce()
	if e != nil {
		return result, e
	}
	action := map[string]string{"execute": "database_task_execute", "stopped": "database_task_stop", "removed": "database_task_remove"}[target]
	reference := ""
	if canceling {
		action = "database_task_cancel"
		reference = state.pending
	}
	started := r
	if e = l.appendEvent(Event{Action: action, OperationID: id, ReferenceOperationID: reference, Outcome: "started", DatabaseTaskAction: &started}); e != nil {
		return result, e
	}
	result.Pending = true
	var submittedError error
	switch target {
	case "execute":
		_, submittedError = client.StartDatabaseTask(ctx, creation.record.Task, creation.record.ID, files, c.Compose.Process.ReservePercent)
	case "stopped":
		submittedError = client.StopDatabaseTask(ctx, creation.record.Task, creation.record.ID, c.Compose.Process.StopSeconds)
	case "removed":
		submittedError = client.RemoveDatabaseTask(ctx, creation.record.Task, creation.record.ID)
	}
	outcome, code := "unresolved", "OFFLINE_SCOPE_DATABASE_TASK_OUTCOME_UNKNOWN"
	observation, stopObservation := context.WithTimeout(ctx, time.Duration(c.Compose.Process.ObserveSeconds)*time.Second)
	defer stopObservation()
	for {
		var observeError error
		outcome, code, observeError = captureDatabaseTaskAction(observation, l, client, *creation.record, files, &r)
		if submittedError != nil || observeError != nil || outcome != "unresolved" {
			break
		}
		select {
		case <-observation.Done():
			code = "OFFLINE_SCOPE_DATABASE_TASK_OBSERVATION_DEADLINE"
		case <-time.After(time.Duration(c.Compose.Process.PollMilliseconds) * time.Millisecond):
			continue
		}
		break
	}
	if submittedError != nil {
		outcome = "uncertain"
		code = bundle.ErrorCode(submittedError)
	} else if outcome == "unresolved" {
		outcome = "uncertain"
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		outcome = "uncertain"
		code = bundle.ErrorCode(e)
	}
	if e = l.appendEvent(Event{Action: action, OperationID: id, ReferenceOperationID: reference, Outcome: outcome, Code: code, DatabaseTaskAction: &r}); e != nil {
		return result, e
	}
	result.SchemaQualified = schemaQualified(l.events)
	result.RuntimeAccessQualified = runtimeAccessQualified(l.events)
	result.BootstrapQualified = bootstrapQualified(l.events)
	result.Pending = outcome == "uncertain"
	result.Status = "recorded_database_task_" + target + "_" + outcome
	if outcome == "completed" {
		if r.Target == "execute" {
			result.ProcessSucceeded = true
		}
		if target == "removed" {
			result.Removed = true
		}
		return result, nil
	}
	if canceling && outcome == "failed" {
		result.Status = "recorded_migration_process_canceled_and_stopped"
		return result, nil
	}
	return result, failure(code)
}
