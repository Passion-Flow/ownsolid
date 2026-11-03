package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type ModelTaskRecord struct {
	DescriptorSHA256 string                `json:"descriptor_sha256"`
	VolumeSHA256     string                `json:"volume_sha256"`
	Task             engine.ModelStoreTask `json:"task"`
	ID               string                `json:"id,omitempty"`
}
type modelTaskRecords struct {
	record  *ModelTaskRecord
	pending string
	outcome string
}

func modelTaskState(events []Event) modelTaskRecords {
	state := modelTaskRecords{}
	for _, e := range events {
		if e.Action == "model_task_create" {
			state.record = e.ModelTask
			state.outcome = e.Outcome
			state.pending = e.OperationID
			if e.Outcome == "completed" {
				state.pending = ""
			}
		}
		if e.Action == "model_task_reconcile" {
			state.record = e.ModelTask
			state.outcome = e.Outcome
			if e.Outcome == "completed" {
				state.pending = ""
			}
		}
	}
	return state
}
func modelTaskIdentity(a, b ModelTaskRecord) bool { a.ID = b.ID; return a == b }
func validModelTaskRecord(events []Event, r *ModelTaskRecord, scopeID string) bool {
	if r == nil || !wire.ValidSHA256(r.DescriptorSHA256) || !wire.ValidSHA256(r.VolumeSHA256) || r.ID != "" && !utilityIDPattern.MatchString(r.ID) {
		return false
	}
	input, outcome, _ := inputsState(events)
	volumes := volumeState(events)
	volume, ok := volumes.roles["models"]
	if input == nil || outcome != "completed" || input.DescriptorSHA256 != r.DescriptorSHA256 || !ok || volume.Phase != phasePrepared || wire.Digest(encode(volume)) != r.VolumeSHA256 {
		return false
	}
	t := r.Task
	return t.Owner.ScopeID == scopeID && t.Owner.Role == engine.ModelTaskRole && identityPattern.MatchString(t.Owner.OperationID) && t.VolumeOwner == (engine.Ownership{ScopeID: scopeID, OperationID: volume.ResourceOperationID, Role: "models"}) && t.Image == input.ImageID && t.Image == volume.ImageID && t.UID == volume.UID && t.GID == volume.GID
}
func validateModelTaskEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_MODEL_TASK_TRANSITION_INVALID") }
	if e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || !validModelTaskRecord(events, e.ModelTask, e.ScopeID) || len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || processState(events).pending != "" || runtimeState(events).record != nil {
		return bad()
	}
	s := modelTaskState(events)
	if e.Action == "model_task_create" && e.Outcome == "started" {
		if s.record != nil || e.ReferenceOperationID != "" || e.Code != "" || e.ModelTask.ID != "" || e.OperationID != e.ModelTask.Task.Owner.OperationID {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.pending == "" || s.record == nil || !modelTaskIdentity(*s.record, *e.ModelTask) || s.record.ID != "" && s.record.ID != e.ModelTask.ID {
		return bad()
	}
	if e.Action == "model_task_create" {
		if s.pending != e.OperationID || s.outcome != "started" || e.ReferenceOperationID != "" {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" && utilityIDPattern.MatchString(e.ModelTask.ID) || e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
		return bad()
	}
	if e.Action == "model_task_reconcile" {
		if e.ReferenceOperationID != s.pending {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		if e.Outcome == "completed" && e.Code == "" && utilityIDPattern.MatchString(e.ModelTask.ID) || e.Outcome == "unresolved" && codePattern.MatchString(e.Code) {
			return nil
		}
	}
	return bad()
}
