package scope

import (
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// BaseTask retains exact cleanup identity without credentials. The only reason
// is the protected plan's audit reason, also bound by the payload/context hashes.
type RestoreDatabaseRecord struct {
	Native         *MVPNativeRestoreStageRecord `json:"native,omitempty"`
	PlanSHA256     string                       `json:"plan_sha256"`
	SQLProofSHA256 string                       `json:"sql_proof_sha256"`
	PayloadSHA256  string                       `json:"payload_sha256"`
	ContextSHA256  string                       `json:"context_sha256"`
	Initializer    string                       `json:"initializer"`
	BaseTask       engine.DatabaseTask          `json:"base_task"`
	Step           int                          `json:"step"`
	Phase          string                       `json:"phase"`
	Kind           string                       `json:"kind,omitempty"`
	ObjectID       string                       `json:"object_id,omitempty"`
	ResultSHA256   string                       `json:"result_sha256,omitempty"`
}

type restoreDatabaseStep struct{ phase, kind string }

func restoreDatabaseSteps() []restoreDatabaseStep {
	steps := []restoreDatabaseStep{}
	for _, phase := range []string{"volume-create", "utility-create", "payload-write", "utility-remove"} {
		steps = append(steps, restoreDatabaseStep{phase, ""})
	}
	for _, kind := range []string{engine.DatabaseSchemaCheck, engine.DatabaseRuntimeRestorePrepare, engine.DatabaseRuntimeCheck, engine.DatabaseRestoredAuthInvalidate} {
		for _, phase := range []string{"task-create", "task-execute", "task-remove"} {
			steps = append(steps, restoreDatabaseStep{phase, kind})
		}
	}
	return append(steps, restoreDatabaseStep{"volume-remove", ""}, restoreDatabaseStep{"complete", ""})
}
func (r RestoreDatabaseRecord) task(kind string) engine.DatabaseTask {
	t := r.BaseTask
	t.Kind = kind
	t.Owner.Role = "database-" + kind
	if kind != engine.DatabaseRestoredAuthInvalidate {
		t.Reason = ""
	}
	return t
}
func restoreDatabaseIdentity(a, b RestoreDatabaseRecord) bool {
	a.Step, b.Step = 0, 0
	a.Phase, b.Phase = "", ""
	a.Kind, b.Kind = "", ""
	a.ObjectID, b.ObjectID = "", ""
	a.ResultSHA256, b.ResultSHA256 = "", ""
	return reflect.DeepEqual(a, b)
}

type restoreDatabaseRecords struct {
	record                                   *RestoreDatabaseRecord
	pending, action, outcome, abortReference string
	completed                                map[int]RestoreDatabaseRecord
	failed, aborting, aborted, complete      bool
}

func restoreDatabaseState(events []Event) restoreDatabaseRecords {
	s := restoreDatabaseRecords{completed: map[int]RestoreDatabaseRecord{}}
	for _, e := range events {
		if e.RestoreDatabase == nil {
			continue
		}
		r := *e.RestoreDatabase
		s.record = &r
		s.outcome = e.Outcome
		if e.Action == "restore_database_abort" {
			s.action = e.Action
			if e.Outcome == "started" {
				s.pending = e.OperationID
				s.aborting = true
				s.abortReference = e.ReferenceOperationID
			}
			if e.Outcome == "completed" {
				s.pending = ""
				s.aborting = false
				s.aborted = true
			}
			continue
		}
		if e.Action == "restore_database_effect" {
			s.action = e.Action
		}
		if e.Outcome == "started" {
			s.pending = e.OperationID
		}
		if e.Outcome == "completed" {
			s.pending = ""
			s.completed[r.Step] = r
			s.complete = r.Phase == "complete"
		}
		if e.Outcome == "failed" {
			s.pending = ""
			s.failed = true
		}
	}
	return s
}
func restoreDatabaseAction(action string) bool {
	return action == "restore_database_effect" || action == "restore_database_reconcile" || action == "restore_database_abort"
}
func validRestoreDatabaseRecord(r *RestoreDatabaseRecord, events []Event, scopeID string) bool {
	reservation := restoreState(events)
	sqlProof, ok := restoreSQLProof(events)
	if r == nil || reservation.record == nil || !reservation.reserved || reservation.pending != "" || reservation.aborted || !ok || r.SQLProofSHA256 != sqlProof || r.PlanSHA256 != reservation.record.PlanSHA256 || !wire.ValidSHA256(r.PayloadSHA256) || !wire.ValidSHA256(r.ContextSHA256) || !immutableDigest(r.Initializer) || r.Step < 0 || r.Step >= len(restoreDatabaseStepsForRecord(r)) {
		return false
	}
	step := restoreDatabaseStepsForRecord(r)[r.Step]
	sql := restoreSQLState(events).record
	if sql == nil || (r.Native == nil) != (sql.Native == nil) || r.Native == nil && r.ResultSHA256 != "" || r.Native != nil && (!validMVPNativeRestoreStageRecord(r.Native, mvpNativeRestoreDatabaseStage, reservation.record.AttemptID) || r.Native.ExecutionSHA256 != sql.Native.ExecutionSHA256) {
		return false
	}
	if r.Phase != step.phase || r.Kind != step.kind || r.BaseTask.Kind != engine.DatabaseRestoredAuthInvalidate || r.BaseTask.Owner.ScopeID != scopeID || r.BaseTask.Owner.OperationID != reservation.record.AttemptID || r.BaseTask.ValidateRestoredDatabaseTask() != nil {
		return false
	}
	for _, kind := range []string{engine.DatabaseSchemaCheck, engine.DatabaseRuntimeRestorePrepare, engine.DatabaseRuntimeCheck} {
		if r.task(kind).ValidateRestoredDatabaseTask() != nil {
			return false
		}
	}
	return true
}
func restoreDatabaseProof(events []Event) (string, bool) {
	s := restoreDatabaseState(events)
	if !s.complete || s.record == nil || s.aborted || s.failed || s.pending != "" || len(s.completed) != len(restoreDatabaseStepsForRecord(s.record)) {
		return "", false
	}
	if s.record.Native != nil {
		records := make([]RestoreDatabaseRecord, len(s.completed))
		for i := range records {
			r, ok := s.completed[i]
			if !ok {
				return "", false
			}
			records[i] = r
		}
		return wire.Digest(encode(records)), true
	}
	return wire.Digest(encode(*s.record)), true
}
func validateRestoreDatabaseEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_RESTORE_DATABASE_TRANSITION_INVALID") }
	r := e.RestoreDatabase
	s := restoreDatabaseState(events)
	if !identityPattern.MatchString(e.OperationID) || !validRestoreDatabaseRecord(r, events, e.ScopeID) || e.RestoreSQL != nil || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	if s.record != nil && (!restoreDatabaseIdentity(*s.record, *r) || s.aborted || s.complete) {
		return bad()
	}
	if e.Action == "restore_database_abort" {
		if s.record == nil || r.Step != s.record.Step || r.ObjectID != "" || r.ResultSHA256 != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if s.aborting || e.ReferenceOperationID != s.pending || e.Code != "" {
				return bad()
			}
			for _, prior := range events {
				if prior.OperationID == e.OperationID {
					return bad()
				}
			}
			return nil
		}
		if !s.aborting || e.OperationID != s.pending || e.ReferenceOperationID != s.abortReference {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" || e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
		return bad()
	}
	if s.aborting || s.failed {
		return bad()
	}
	if e.Action == "restore_database_effect" && e.Outcome == "started" {
		if s.pending != "" || e.ReferenceOperationID != "" || e.Code != "" || r.ObjectID != "" || r.ResultSHA256 != "" || r.Step != len(s.completed) {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.record == nil || s.pending == "" || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "restore_database_effect" {
		if s.action != "restore_database_effect" || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action == "restore_database_reconcile" {
		if e.ReferenceOperationID != s.pending {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
	} else {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" {
		if r.Native != nil {
			if !nativeRestoreDatabaseResultValid(*r, events) {
				return bad()
			}
			return nil
		}
		if r.Phase == "utility-create" || r.Phase == "task-create" {
			if !utilityIDPattern.MatchString(r.ObjectID) {
				return bad()
			}
		} else if r.ObjectID != "" {
			return bad()
		}
		return nil
	}
	if r.ObjectID != "" || r.ResultSHA256 != "" {
		return bad()
	}
	if e.Outcome == "failed" && r.Phase == "task-execute" && e.Code == "OFFLINE_SCOPE_RESTORE_DATABASE_TASK_FAILED" {
		return nil
	}
	if codePattern.MatchString(e.Code) && ((e.Action == "restore_database_effect" && e.Outcome == "uncertain") || (e.Action == "restore_database_reconcile" && e.Outcome == "unresolved")) {
		return nil
	}
	return bad()
}
