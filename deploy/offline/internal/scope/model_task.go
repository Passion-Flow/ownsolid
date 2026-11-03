package scope

import (
	"context"
	"path/filepath"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type modelTaskEngine interface {
	imageEngine
	InspectVolume(context.Context, engine.Ownership) (engine.Volume, bool, error)
	CreateModelTask(context.Context, engine.ModelStoreTask) (engine.RuntimeContainer, error)
	InspectModelTask(context.Context, engine.ModelStoreTask) (engine.RuntimeContainer, bool, error)
}
type ModelTaskResult struct {
	Status        string `json:"status"`
	ScopeID       string `json:"scope_id"`
	ID            string `json:"id,omitempty"`
	Pending       bool   `json:"pending"`
	Qualification string `json:"qualification"`
}

func CreateModelTask(ctx context.Context, c configuration.Config, reconcile bool) (ModelTaskResult, error) {
	if c.Engine == nil || c.Engine.Utility == nil {
		return ModelTaskResult{}, failure("OFFLINE_SCOPE_UTILITY_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ModelTaskResult{}, e
	}
	defer client.Close()
	return createModelTask(ctx, c, client, reconcile)
}
func (l *locked) modelTaskContext(ctx context.Context, client modelTaskEngine) (ModelTaskRecord, error) {
	var result ModelTaskRecord
	c := l.config
	verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return result, e
	}
	if verified.Profile != l.marker.Profile || verified.ReleaseID != l.marker.ReleaseID {
		return result, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
	}
	if e = validateImageAssets(l.events, verified.Manifest); e != nil {
		return result, e
	}
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return result, failure("OFFLINE_SCOPE_COMPLETED_INPUT_SNAPSHOT_REQUIRED")
	}
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return result, e
	}
	volumes := volumeState(l.events)
	images := imageState(l.events)
	if len(images.pending) > 0 || len(volumes.pending) > 0 || len(volumes.roles) != len(p.descriptor.Roles) || runtimeState(l.events).record != nil || processState(l.events).pending != "" {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_PREPARATION_REQUIRED")
	}
	if e = validateVolumeInputs(volumes, p); e != nil {
		return result, e
	}
	volume, exists := volumes.roles["models"]
	if !exists || len(p.files["models"]) != 0 {
		return result, failure("OFFLINE_SCOPE_EMPTY_MODELS_INPUT_REQUIRED")
	}
	for _, r := range volumes.roles {
		if r.Phase != phasePrepared {
			return result, failure("OFFLINE_SCOPE_MODEL_TASK_PREPARATION_REQUIRED")
		}
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return result, e
	}
	admitted, ok := images.completed[p.descriptor.ImageAssetID]
	if !ok {
		return result, failure("OFFLINE_SCOPE_INITIALIZER_IMAGE_NOT_ADMITTED")
	}
	image, found, e := client.Inspect(ctx, p.descriptor.ImageID)
	if e != nil {
		return result, e
	}
	if !found || !matchesImage(image, admitted) {
		return result, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
	}
	for _, r := range volumes.roles {
		if _, found, e = client.InspectVolume(ctx, l.volumeOwner(r)); e != nil {
			return result, e
		} else if !found {
			return result, failure("OFFLINE_SCOPE_RECORDED_VOLUME_MISSING")
		}
	}
	result = ModelTaskRecord{DescriptorSHA256: input.DescriptorSHA256, VolumeSHA256: wire.Digest(encode(volume)), Task: engine.ModelStoreTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, Role: engine.ModelTaskRole}, VolumeOwner: l.volumeOwner(volume), Image: input.ImageID, UID: volume.UID, GID: volume.GID}}
	return result, nil
}
func createModelTask(ctx context.Context, c configuration.Config, client modelTaskEngine, reconcile bool) (ModelTaskResult, error) {
	result := ModelTaskResult{Qualification: "never_started_operator_task_creation_only_not_initialized_storage_application_or_target"}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return result, e
	}
	defer l.close()
	result.ScopeID = l.marker.ScopeID
	state := modelTaskState(l.events)
	result.Pending = state.pending != ""
	if state.pending != "" && !reconcile {
		return result, failure("OFFLINE_SCOPE_MODEL_TASK_RECONCILIATION_REQUIRED")
	}
	current, e := l.modelTaskContext(ctx, client)
	if e != nil {
		return result, e
	}
	if state.record != nil {
		current.Task.Owner.OperationID = state.record.Task.Owner.OperationID
		current.ID = state.record.ID
		if !modelTaskIdentity(current, *state.record) {
			return result, failure("OFFLINE_SCOPE_MODEL_TASK_INPUT_MISMATCH")
		}
		observed, found, e := client.InspectModelTask(ctx, current.Task)
		if e != nil {
			return result, e
		}
		matches := found && neverStartedModelTask(observed) && (current.ID == "" || observed.ID == current.ID)
		if state.pending == "" {
			if !matches {
				return result, failure("OFFLINE_SCOPE_RECORDED_MODEL_TASK_MISSING_OR_CHANGED")
			}
			result.ID = observed.ID
			result.Status = "observed_recorded_never_started_model_task"
			return result, nil
		}
		if e = admitProcessJournal(l, 1); e != nil {
			return result, e
		}
		id, e := nonce()
		if e != nil {
			return result, e
		}
		outcome, code := "unresolved", "OFFLINE_SCOPE_MODEL_TASK_OUTCOME_UNKNOWN"
		if matches {
			current.ID = observed.ID
			outcome = "completed"
			code = ""
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return result, e
		}
		if e = l.appendEvent(Event{Action: "model_task_reconcile", OperationID: id, ReferenceOperationID: state.pending, Outcome: outcome, Code: code, ModelTask: &current}); e != nil {
			return result, e
		}
		result.ID = current.ID
		result.Pending = !matches
		result.Status = "recorded_model_task_" + outcome
		if !matches {
			return result, failure(code)
		}
		return result, nil
	}
	if reconcile {
		result.Status = "observed_no_pending_model_task_creation"
		return result, nil
	}
	if e = admitProcessJournal(l, 3); e != nil {
		return result, e
	}
	id, e := nonce()
	if e != nil {
		return result, e
	}
	current.Task.Owner.OperationID = id
	started := current
	if e = l.appendEvent(Event{Action: "model_task_create", OperationID: id, Outcome: "started", ModelTask: &started}); e != nil {
		return result, e
	}
	result.Pending = true
	observed, e := client.CreateModelTask(ctx, current.Task)
	if utilityIDPattern.MatchString(observed.ID) {
		current.ID = observed.ID
		result.ID = observed.ID
	}
	if e == nil {
		var found bool
		observed, found, e = client.InspectModelTask(ctx, current.Task)
		if e == nil && (!found || observed.ID != current.ID || !neverStartedModelTask(observed)) {
			e = failure("OFFLINE_SCOPE_MODEL_TASK_CREATE_UNCERTAIN")
		}
	}
	if e == nil {
		_, e = l.checkEngine(ctx, client)
	}
	if e == nil && ctx.Err() != nil {
		e = failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if e != nil {
		code := bundle.ErrorCode(e)
		if ce := l.appendEvent(Event{Action: "model_task_create", OperationID: id, Outcome: "uncertain", Code: code, ModelTask: &current}); ce != nil {
			return result, ce
		}
		return result, e
	}
	if e = l.appendEvent(Event{Action: "model_task_create", OperationID: id, Outcome: "completed", ModelTask: &current}); e != nil {
		return result, e
	}
	result.Pending = false
	result.Status = "recorded_never_started_model_task_creation"
	return result, nil
}

func neverStartedModelTask(value engine.RuntimeContainer) bool {
	started, e := time.Parse(time.RFC3339Nano, value.State.StartedAt)
	return e == nil && started.IsZero() && utilityIDPattern.MatchString(value.ID) && value.State.Status == "created" && value.State.PID == 0 && !value.State.Running && !value.State.Restarting && !value.State.Paused && !value.State.Dead && !value.State.OOMKilled && value.State.ExitCode == 0
}
