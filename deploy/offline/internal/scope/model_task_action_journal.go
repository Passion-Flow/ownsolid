package scope

import (
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type ModelTaskActionRecord struct {
	CreationSHA256 string                    `json:"creation_sha256"`
	Target         string                    `json:"target"`
	Before         ProcessSnapshot           `json:"before"`
	After          *ProcessSnapshot          `json:"after,omitempty"`
	OOMKilled      bool                      `json:"oom_killed,omitempty"`
	Absent         bool                      `json:"absent,omitempty"`
	Admission      *engine.ResourceAdmission `json:"admission,omitempty"`
	ResultVerified bool                      `json:"result_verified,omitempty"`
	StopBefore     *ProcessSnapshot          `json:"stop_before,omitempty"`
}
type modelTaskActions struct {
	record       *ModelTaskActionRecord
	pending      string
	action       string
	reference    string
	outcome      string
	everExecuted bool
	initialized  bool
	failed       bool
	removed      bool
}

func isModelTaskAction(action string) bool {
	return action == "model_task_execute" || action == "model_task_stop" || action == "model_task_remove" || action == "model_task_cancel" || action == "model_task_action_reconcile"
}
func modelTaskActionState(events []Event) modelTaskActions {
	s := modelTaskActions{}
	for _, e := range events {
		if !isModelTaskAction(e.Action) {
			continue
		}
		s.record = e.ModelTaskAction
		s.outcome = e.Outcome
		if e.Action != "model_task_action_reconcile" {
			s.pending = e.OperationID
			s.action = e.Action
			s.reference = e.ReferenceOperationID
		}
		if e.Outcome == "started" && e.Action == "model_task_execute" {
			s.everExecuted = true
		}
		if e.Outcome == "completed" || e.Outcome == "failed" {
			s.pending = ""
			if e.ModelTaskAction.Target == "execute" {
				s.initialized = e.Outcome == "completed"
				s.failed = e.Outcome == "failed"
			}
			if e.ModelTaskAction.Target == "removed" && e.Outcome == "completed" {
				s.removed = true
			}
		}
	}
	return s
}
func modelTaskActionIdentity(a, b ModelTaskActionRecord) bool {
	return a.CreationSHA256 == b.CreationSHA256 && a.Target == b.Target && a.Before == b.Before && reflect.DeepEqual(a.Admission, b.Admission) && reflect.DeepEqual(a.StopBefore, b.StopBefore)
}
func validModelTaskAction(events []Event, r *ModelTaskActionRecord) bool {
	creation := modelTaskState(events)
	if r == nil || creation.pending != "" || creation.record == nil || creation.outcome != "completed" || r.CreationSHA256 != wire.Digest(encode(*creation.record)) || !validProcessSnapshot(r.Before) || r.Before.ID != creation.record.ID {
		return false
	}
	if r.After != nil && (!validProcessSnapshot(*r.After) || r.After.ID != r.Before.ID) || r.After == nil && (r.OOMKilled || r.ResultVerified) || r.Absent && r.After != nil {
		return false
	}
	if r.Target == "execute" {
		if r.StopBefore != nil && (!validProcessSnapshot(*r.StopBefore) || r.StopBefore.State != "running" || r.StopBefore.ID != r.Before.ID || r.After != nil && r.After.StartedAt != r.StopBefore.StartedAt) {
			return false
		}
		return r.Before.State == "created" && r.Admission != nil && r.Admission.ValidateRecorded() == nil && !r.Absent
	}
	if r.Admission != nil || r.ResultVerified || r.StopBefore != nil {
		return false
	}
	return r.Target == "stopped" && r.Before.State == "running" && !r.Absent || r.Target == "removed" && (r.Before.State == "created" || r.Before.State == "exited")
}
func completedModelTaskAction(r *ModelTaskActionRecord) bool {
	if r.Target == "removed" {
		return r.Absent && r.After == nil
	}
	if r.After == nil || r.After.State != "exited" {
		return false
	}
	if r.Target == "stopped" {
		return r.After.StartedAt == r.Before.StartedAt
	}
	return r.After.StartedAt != r.Before.StartedAt && r.After.ExitCode == 0 && !r.OOMKilled && r.ResultVerified
}
func failedModelTaskAction(r *ModelTaskActionRecord) bool {
	return r.Target == "execute" && r.After != nil && r.After.State == "exited" && r.After.StartedAt != r.Before.StartedAt && (r.After.ExitCode != 0 || r.OOMKilled) && !r.ResultVerified
}
func validateModelTaskActionEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_TRANSITION_INVALID") }
	if e.ModelTask != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || !validModelTaskAction(events, e.ModelTaskAction) || len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || runtimeState(events).record != nil || processState(events).pending != "" {
		return bad()
	}
	r := e.ModelTaskAction
	s := modelTaskActionState(events)
	if e.Action == "model_task_cancel" && e.Outcome == "started" {
		if s.pending == "" || s.record == nil || s.record.Target != "execute" || s.record.StopBefore != nil || r.StopBefore == nil || e.ReferenceOperationID != s.pending || e.Code != "" || r.After != nil || r.Absent || r.OOMKilled || r.ResultVerified {
			return bad()
		}
		original := *r
		original.StopBefore = nil
		if !modelTaskActionIdentity(*s.record, original) || s.record.After != nil && s.record.After.State != "created" && s.record.After.StartedAt != r.StopBefore.StartedAt {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if e.Outcome == "started" {
		if e.Action == "model_task_action_reconcile" || s.pending != "" || s.removed || r.Target == "execute" && s.everExecuted || e.ReferenceOperationID != "" || e.Code != "" || r.After != nil || r.Absent || r.OOMKilled || r.ResultVerified || r.StopBefore != nil {
			return bad()
		}
		if e.Action == "model_task_execute" && r.Target != "execute" || e.Action == "model_task_stop" && r.Target != "stopped" || e.Action == "model_task_remove" && r.Target != "removed" {
			return bad()
		}
		if s.record != nil && s.record.After != nil && *s.record.After != r.Before {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.pending == "" || s.record == nil || !modelTaskActionIdentity(*s.record, *r) {
		return bad()
	}
	if s.record.After != nil && s.record.After.State != "created" && r.After != nil && s.record.After.StartedAt != r.After.StartedAt {
		return bad()
	}
	if e.Action == "model_task_action_reconcile" {
		if e.ReferenceOperationID != s.pending {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
	} else if s.pending != e.OperationID || s.outcome != "started" || e.Action != s.action || e.ReferenceOperationID != s.reference {
		return bad()
	}
	switch e.Outcome {
	case "completed":
		if e.Code == "" && completedModelTaskAction(r) {
			return nil
		}
	case "failed":
		if codePattern.MatchString(e.Code) && failedModelTaskAction(r) {
			return nil
		}
	case "uncertain":
		if e.Action != "model_task_action_reconcile" && codePattern.MatchString(e.Code) {
			return nil
		}
	case "unresolved":
		if e.Action == "model_task_action_reconcile" && codePattern.MatchString(e.Code) {
			return nil
		}
	}
	return bad()
}
func validateModelTaskActionInputs(events []Event, c configuration.Config) error {
	for _, e := range events {
		if !isModelTaskAction(e.Action) || e.ModelTaskAction.Admission == nil {
			continue
		}
		if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
			return failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_POLICY_MISMATCH")
		}
		a := e.ModelTaskAction.Admission
		if a.Requirement.MemoryBytes != c.Engine.Utility.MemoryBytes || a.Requirement.MilliCPU != c.Engine.Utility.MilliCPU || a.ReservePercent != c.Compose.Process.ReservePercent {
			return failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_POLICY_MISMATCH")
		}
	}
	return nil
}
