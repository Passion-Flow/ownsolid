package scope

import (
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type DatabaseTaskActionRecord struct {
	StoreNamespace        *engine.StoreTaskObservation `json:"store_namespace,omitempty"`
	BootstrapIntentSHA256 string                       `json:"bootstrap_intent_sha256,omitempty"`
	CreationSHA256        string                       `json:"creation_sha256"`
	Target                string                       `json:"target"`
	Before                ProcessSnapshot              `json:"before"`
	After                 *ProcessSnapshot             `json:"after,omitempty"`
	OOMKilled             bool                         `json:"oom_killed,omitempty"`
	Absent                bool                         `json:"absent,omitempty"`
	Admission             *engine.ResourceAdmission    `json:"admission,omitempty"`
	InputVerified         bool                         `json:"input_verified,omitempty"`
	StopBefore            *ProcessSnapshot             `json:"stop_before,omitempty"`
}
type databaseTaskActions struct {
	record       *DatabaseTaskActionRecord
	pending      string
	action       string
	reference    string
	outcome      string
	everExecuted bool
	succeeded    bool
	failed       bool
	removed      bool
}

func isDatabaseTaskAction(action string) bool {
	return action == "database_task_execute" || action == "database_task_stop" || action == "database_task_remove" || action == "database_task_cancel" || action == "database_task_action_reconcile"
}
func databaseTaskActionState(events []Event) databaseTaskActions {
	s := databaseTaskActions{}
	for _, e := range events {
		if e.Action == "database_task_create" && e.Outcome == "started" {
			s = databaseTaskActions{}
		}
		if !isDatabaseTaskAction(e.Action) {
			continue
		}
		s.record = e.DatabaseTaskAction
		s.outcome = e.Outcome
		if e.Action != "database_task_action_reconcile" {
			s.pending = e.OperationID
			s.action = e.Action
			s.reference = e.ReferenceOperationID
		}
		if e.Outcome == "started" && e.Action == "database_task_execute" {
			s.everExecuted = true
		}
		if e.Outcome == "completed" || e.Outcome == "failed" {
			s.pending = ""
			if e.DatabaseTaskAction.Target == "execute" {
				s.succeeded = e.Outcome == "completed"
				s.failed = e.Outcome == "failed"
			}
			if e.DatabaseTaskAction.Target == "removed" && e.Outcome == "completed" {
				s.removed = true
			}
		}
	}
	return s
}
func databaseTaskActionIdentity(a, b DatabaseTaskActionRecord) bool {
	return a.CreationSHA256 == b.CreationSHA256 && a.Target == b.Target && a.Before == b.Before && reflect.DeepEqual(a.Admission, b.Admission) && reflect.DeepEqual(a.StopBefore, b.StopBefore)
}
func validDatabaseTaskAction(events []Event, r *DatabaseTaskActionRecord) bool {
	creation := databaseTaskState(events)
	if r == nil || creation.pending != "" || creation.record == nil || creation.outcome != "completed" || r.CreationSHA256 != wire.Digest(encode(*creation.record)) || !validProcessSnapshot(r.Before) || r.Before.ID != creation.record.ID {
		return false
	}
	if r.StoreNamespace != nil {
		role, _, ok := engine.StoreTaskContext(creation.record.Task.Kind)
		if !ok || r.Target != "execute" || !r.InputVerified || !engine.ValidStoreTaskObservation(creation.record.Task.Kind, *r.StoreNamespace) {
			return false
		}
		if expected := storeNamespace(events, role); expected != nil && *expected != *r.StoreNamespace {
			return false
		}
	}
	if storeKind(creation.record.Task.Kind) && r.InputVerified && r.StoreNamespace == nil {
		return false
	}
	if r.BootstrapIntentSHA256 != "" && (!bootstrapKind(creation.record.Task.Kind) || r.Target != "execute" || !r.InputVerified || !wire.ValidSHA256(r.BootstrapIntentSHA256)) {
		return false
	}
	if bootstrapKind(creation.record.Task.Kind) && r.InputVerified {
		if r.BootstrapIntentSHA256 == "" {
			return false
		}
		if expected := bootstrapCopySHA(events); expected != "" && r.BootstrapIntentSHA256 != expected {
			return false
		}
	}
	if r.After != nil && (!validProcessSnapshot(*r.After) || r.After.ID != r.Before.ID) || r.After == nil && (r.OOMKilled || r.InputVerified) || r.Absent && r.After != nil {
		return false
	}
	if r.Target == "execute" {
		if r.StopBefore != nil && (!validProcessSnapshot(*r.StopBefore) || r.StopBefore.State != "running" || r.StopBefore.ID != r.Before.ID || r.After != nil && r.After.StartedAt != r.StopBefore.StartedAt) {
			return false
		}
		return r.Before.State == "created" && r.Admission != nil && r.Admission.ValidateRecorded() == nil && !r.Absent
	}
	if r.Admission != nil || r.InputVerified || r.StopBefore != nil {
		return false
	}
	return r.Target == "stopped" && r.Before.State == "running" && !r.Absent || r.Target == "removed" && (r.Before.State == "created" || r.Before.State == "exited")
}
func completedDatabaseTaskAction(r *DatabaseTaskActionRecord) bool {
	if r.Target == "removed" {
		return r.Absent && r.After == nil
	}
	if r.After == nil || r.After.State != "exited" {
		return false
	}
	if r.Target == "stopped" {
		return r.After.StartedAt == r.Before.StartedAt
	}
	return r.After.StartedAt != r.Before.StartedAt && r.After.ExitCode == 0 && !r.OOMKilled && r.InputVerified
}
func failedDatabaseTaskAction(r *DatabaseTaskActionRecord) bool {
	return r.Target == "execute" && r.After != nil && r.After.State == "exited" && r.After.StartedAt != r.Before.StartedAt && (r.After.ExitCode != 0 || r.OOMKilled) && !r.InputVerified
}
func validateDatabaseTaskActionEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_TRANSITION_INVALID") }
	if e.DatabaseTask != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || !validDatabaseTaskAction(events, e.DatabaseTaskAction) || len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || !runtimeState(events).completed || processState(events).pending != "" || modelTaskState(events).pending != "" || modelTaskActionState(events).pending != "" {
		return bad()
	}
	r := e.DatabaseTaskAction
	s := databaseTaskActionState(events)
	if e.Action == "database_task_cancel" && e.Outcome == "started" {
		if s.pending == "" || s.record == nil || s.record.Target != "execute" || s.record.StopBefore != nil || r.StopBefore == nil || e.ReferenceOperationID != s.pending || e.Code != "" || r.After != nil || r.Absent || r.OOMKilled || r.InputVerified {
			return bad()
		}
		original := *r
		original.StopBefore = nil
		if !databaseTaskActionIdentity(*s.record, original) || s.record.After != nil && s.record.After.State != "created" && s.record.After.StartedAt != r.StopBefore.StartedAt {
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
		if e.Action == "database_task_action_reconcile" || s.pending != "" || s.removed || r.Target == "execute" && s.everExecuted || e.ReferenceOperationID != "" || e.Code != "" || r.After != nil || r.Absent || r.OOMKilled || r.InputVerified || r.StopBefore != nil {
			return bad()
		}
		if e.Action == "database_task_execute" && r.Target != "execute" || e.Action == "database_task_stop" && r.Target != "stopped" || e.Action == "database_task_remove" && r.Target != "removed" {
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
	if s.pending == "" || s.record == nil || !databaseTaskActionIdentity(*s.record, *r) {
		return bad()
	}
	if s.record.After != nil && s.record.After.State != "created" && r.After != nil && s.record.After.StartedAt != r.After.StartedAt {
		return bad()
	}
	if e.Action == "database_task_action_reconcile" {
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
		if e.Code == "" && completedDatabaseTaskAction(r) {
			return nil
		}
	case "failed":
		if codePattern.MatchString(e.Code) && failedDatabaseTaskAction(r) {
			return nil
		}
	case "uncertain":
		if e.Action != "database_task_action_reconcile" && codePattern.MatchString(e.Code) {
			return nil
		}
	case "unresolved":
		if e.Action == "database_task_action_reconcile" && codePattern.MatchString(e.Code) {
			return nil
		}
	}
	return bad()
}
func validateDatabaseTaskActionInputs(events []Event, c configuration.Config) error {
	for _, e := range events {
		if !isDatabaseTaskAction(e.Action) || e.DatabaseTaskAction.Admission == nil {
			continue
		}
		if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
			return failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_POLICY_MISMATCH")
		}
		a := e.DatabaseTaskAction.Admission
		if a.Requirement.MemoryBytes != c.Engine.Utility.MemoryBytes || a.Requirement.MilliCPU != c.Engine.Utility.MilliCPU || a.ReservePercent != c.Compose.Process.ReservePercent {
			return failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_POLICY_MISMATCH")
		}
	}
	return nil
}
