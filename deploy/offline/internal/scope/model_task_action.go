package scope

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type modelTaskActionEngine interface {
	modelTaskEngine
	AdmitRuntimeResources(context.Context, engine.ResourceRequirement, int) (engine.ResourceAdmission, error)
	StartModelTask(context.Context, engine.ModelStoreTask, string, int) (engine.ResourceAdmission, error)
	StopModelTask(context.Context, engine.ModelStoreTask, string, int) error
	RemoveModelTask(context.Context, engine.ModelStoreTask, string) error
	InspectModelTaskStorage(context.Context, engine.ModelStoreTask, string, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
	VerifyVolumeConsumers(context.Context, engine.Ownership, []string) error
}
type ModelTaskActionResult struct {
	Status        string `json:"status"`
	ScopeID       string `json:"scope_id"`
	ID            string `json:"id"`
	Pending       bool   `json:"pending"`
	Initialized   bool   `json:"initialized"`
	Removed       bool   `json:"removed"`
	Qualification string `json:"qualification"`
}

func ModelTaskAction(ctx context.Context, c configuration.Config, target string, reconcile bool) (ModelTaskActionResult, error) {
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return ModelTaskActionResult{}, failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return ModelTaskActionResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ModelTaskActionResult{}, e
	}
	defer client.Close()
	return modelTaskAction(ctx, c, client, target, reconcile)
}
func taskProcessSnapshot(value engine.RuntimeContainer) ProcessSnapshot {
	return ProcessSnapshot{ID: value.ID, State: value.State.Status, ExitCode: value.State.ExitCode, StartedAt: value.State.StartedAt}
}
func captureModelTaskAction(ctx context.Context, client modelTaskActionEngine, creation ModelTaskRecord, r *ModelTaskActionRecord) (string, string, error) {
	observed, found, e := client.InspectModelTask(ctx, creation.Task)
	if e != nil {
		return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_OBSERVATION_UNAVAILABLE", e
	}
	if !found {
		if r.Target != "removed" {
			return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_DISAPPEARED", nil
		}
		if e = client.VerifyVolumeConsumers(ctx, creation.Task.VolumeOwner, nil); e != nil {
			return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_VOLUME_CONSUMER_CONFLICT", e
		}
		r.After = nil
		r.OOMKilled = false
		r.Absent = true
		r.ResultVerified = false
		return "completed", "", nil
	}
	if observed.ID != creation.ID {
		return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_ID_CHANGED", nil
	}
	after := taskProcessSnapshot(observed)
	if !validProcessSnapshot(after) {
		return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_PROCESS_SNAPSHOT_INVALID", nil
	}
	if r.StopBefore != nil && r.StopBefore.StartedAt != after.StartedAt || r.After != nil && r.After.State != "created" && r.After.StartedAt != after.StartedAt {
		return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_EXECUTION_CHANGED", nil
	}
	r.After = &after
	r.OOMKilled = observed.State.OOMKilled
	r.Absent = false
	r.ResultVerified = false
	if r.Target == "execute" && after.State == "exited" && after.StartedAt != r.Before.StartedAt && after.ExitCode == 0 && !r.OOMKilled {
		contents, e := client.InspectModelTaskStorage(ctx, creation.Task, creation.ID, []engine.ConfigurationFile{})
		if e != nil {
			return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_RESULT_UNAVAILABLE", e
		}
		if contents.Status != "verified_private_volume_payload" || contents.Files != 0 || contents.Bytes != 0 {
			return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_RESULT_CONFLICT", nil
		}
		r.ResultVerified = true
	}
	if completedModelTaskAction(r) {
		return "completed", "", nil
	}
	if failedModelTaskAction(r) {
		return "failed", "OFFLINE_SCOPE_MODEL_INITIALIZER_FAILED", nil
	}
	return "unresolved", "OFFLINE_SCOPE_MODEL_TASK_OUTCOME_UNKNOWN", nil
}
func modelTaskAction(ctx context.Context, c configuration.Config, client modelTaskActionEngine, target string, reconcile bool) (ModelTaskActionResult, error) {
	result := ModelTaskActionResult{Qualification: "scoped_model_directory_initializer_only_not_database_application_installation_or_target"}
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return result, e
	}
	if !reconcile && target != "execute" && target != "stopped" && target != "removed" {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_TARGET_INVALID")
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
	creation := modelTaskState(l.events)
	state := modelTaskActionState(l.events)
	result.Pending = state.pending != ""
	result.Initialized = state.initialized
	result.Removed = state.removed
	if creation.pending != "" || creation.record == nil || creation.outcome != "completed" {
		return result, failure("OFFLINE_SCOPE_COMPLETED_MODEL_TASK_CREATION_REQUIRED")
	}
	result.ID = creation.record.ID
	canceling := state.pending != "" && !reconcile && target == "stopped" && state.record != nil && state.record.Target == "execute" && state.record.StopBefore == nil
	if state.pending != "" && !reconcile && !canceling {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_RECONCILIATION_REQUIRED")
	}
	current, e := l.modelTaskContext(ctx, client)
	if e != nil {
		return result, e
	}
	current.Task.Owner.OperationID = creation.record.Task.Owner.OperationID
	if !modelTaskIdentity(current, *creation.record) {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_INPUT_MISMATCH")
	}
	if e = validateModelTaskActionInputs(l.events, c); e != nil {
		return result, e
	}
	if reconcile {
		if state.pending == "" {
			result.Status = "observed_no_pending_model_task_action"
			return result, nil
		}
		if e = admitProcessJournal(l, 1); e != nil {
			return result, e
		}
		r := *state.record
		outcome, code, observeError := captureModelTaskAction(ctx, client, *creation.record, &r)
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
		if e = l.appendEvent(Event{Action: "model_task_action_reconcile", OperationID: id, ReferenceOperationID: state.pending, Outcome: outcome, Code: code, ModelTaskAction: &r}); e != nil {
			return result, e
		}
		result.Pending = outcome == "unresolved"
		result.Status = "recorded_model_task_action_" + outcome
		if outcome == "completed" {
			if r.Target == "execute" {
				result.Initialized = true
			}
			if r.Target == "removed" {
				result.Removed = true
			}
			return result, nil
		}
		return result, failure(code)
	}
	observed, found, e := client.InspectModelTask(ctx, creation.record.Task)
	if e != nil {
		return result, e
	}
	if state.removed {
		if found {
			return result, failure("OFFLINE_SCOPE_REMOVED_MODEL_TASK_REAPPEARED")
		}
		if e = client.VerifyVolumeConsumers(ctx, creation.record.Task.VolumeOwner, nil); e != nil {
			return result, e
		}
		if target != "removed" {
			return result, failure("OFFLINE_SCOPE_MODEL_TASK_ALREADY_REMOVED")
		}
		result.Status = "observed_recorded_model_task_removal"
		return result, nil
	}
	if !found || observed.ID != creation.record.ID {
		return result, failure("OFFLINE_SCOPE_RECORDED_MODEL_TASK_MISSING_OR_CHANGED")
	}
	before := taskProcessSnapshot(observed)
	if !validProcessSnapshot(before) {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_PROCESS_SNAPSHOT_INVALID")
	}
	if !canceling && state.record != nil && state.record.After != nil && *state.record.After != before {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_EXECUTION_CHANGED")
	}
	if target == "execute" && state.everExecuted {
		result.Status = "observed_recorded_model_initializer_outcome"
		if state.failed {
			return result, failure("OFFLINE_SCOPE_MODEL_INITIALIZER_FAILED")
		}
		if !state.initialized {
			return result, failure("OFFLINE_SCOPE_MODEL_TASK_EXECUTION_PRESERVED")
		}
		return result, nil
	}
	if target == "execute" && !neverStartedModelTask(observed) {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_EXECUTION_PRESERVED")
	}
	if canceling && (before.State != "running" || state.record.After != nil && (state.record.After.State == "exited" || state.record.After.State != "created" && state.record.After.StartedAt != before.StartedAt)) {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_RECONCILIATION_REQUIRED")
	}
	if target == "stopped" && before.State != "running" {
		result.Status = "observed_model_task_already_stopped"
		return result, nil
	}
	if target == "removed" && before.State == "running" {
		return result, failure("OFFLINE_SCOPE_RUNNING_MODEL_TASK_PRESERVED")
	}
	r := ModelTaskActionRecord{CreationSHA256: wire.Digest(encode(*creation.record)), Target: target, Before: before}
	if canceling {
		r = *state.record
		r.After = nil
		r.Absent = false
		r.OOMKilled = false
		r.ResultVerified = false
		r.StopBefore = &before
	}
	if target == "execute" {
		admission, e := client.AdmitRuntimeResources(ctx, engine.ResourceRequirement{MemoryBytes: c.Engine.Utility.MemoryBytes, MilliCPU: c.Engine.Utility.MilliCPU}, c.Compose.Process.ReservePercent)
		if e != nil {
			return result, e
		}
		if admission.ValidateRecorded() != nil || admission.Requirement.MemoryBytes != c.Engine.Utility.MemoryBytes || admission.Requirement.MilliCPU != c.Engine.Utility.MilliCPU || admission.ReservePercent != c.Compose.Process.ReservePercent {
			return result, failure("OFFLINE_SCOPE_MODEL_TASK_ADMISSION_INVALID")
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
	action := map[string]string{"execute": "model_task_execute", "stopped": "model_task_stop", "removed": "model_task_remove"}[target]
	reference := ""
	if canceling {
		action = "model_task_cancel"
		reference = state.pending
	}
	started := r
	if e = l.appendEvent(Event{Action: action, OperationID: id, ReferenceOperationID: reference, Outcome: "started", ModelTaskAction: &started}); e != nil {
		return result, e
	}
	result.Pending = true
	var submittedError error
	switch target {
	case "execute":
		_, submittedError = client.StartModelTask(ctx, creation.record.Task, creation.record.ID, c.Compose.Process.ReservePercent)
	case "stopped":
		submittedError = client.StopModelTask(ctx, creation.record.Task, creation.record.ID, c.Compose.Process.StopSeconds)
	case "removed":
		submittedError = client.RemoveModelTask(ctx, creation.record.Task, creation.record.ID)
	}
	outcome, code := "unresolved", "OFFLINE_SCOPE_MODEL_TASK_OUTCOME_UNKNOWN"
	observation, stopObservation := context.WithTimeout(ctx, time.Duration(c.Compose.Process.ObserveSeconds)*time.Second)
	defer stopObservation()
	for {
		var observeError error
		outcome, code, observeError = captureModelTaskAction(observation, client, *creation.record, &r)
		if submittedError != nil || observeError != nil || outcome != "unresolved" {
			break
		}
		select {
		case <-observation.Done():
			code = "OFFLINE_SCOPE_MODEL_TASK_OBSERVATION_DEADLINE"
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
	if e = l.appendEvent(Event{Action: action, OperationID: id, ReferenceOperationID: reference, Outcome: outcome, Code: code, ModelTaskAction: &r}); e != nil {
		return result, e
	}
	result.Pending = outcome == "uncertain"
	result.Status = "recorded_model_task_" + target + "_" + outcome
	if outcome == "completed" {
		if r.Target == "execute" {
			result.Initialized = true
		}
		if target == "removed" {
			result.Removed = true
		}
		return result, nil
	}
	if canceling && outcome == "failed" {
		result.Status = "recorded_model_initializer_canceled_and_stopped"
		return result, nil
	}
	return result, failure(code)
}
