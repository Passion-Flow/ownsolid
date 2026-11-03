package scope

import (
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const restoreStoresPlanFile = "restore-stores-plan.json"

// The immutable private plan retains exact task/cleanup facts. Journal entries
// bind its digest instead of duplicating potentially large audit reasons/jobs.
type RestoreStoresRecord struct {
	Native              *MVPNativeRestoreStageRecord `json:"native,omitempty"`
	GenerationID        string                       `json:"generation_id"`
	PlanSHA256          string                       `json:"plan_sha256"`
	RestorePlanSHA256   string                       `json:"restore_plan_sha256"`
	SQLProofSHA256      string                       `json:"sql_proof_sha256"`
	DatabaseProofSHA256 string                       `json:"database_proof_sha256"`
	Stores              []string                     `json:"stores"`
	Step                int                          `json:"step"`
	Phase               string                       `json:"phase"`
	Role                string                       `json:"role,omitempty"`
	Kind                string                       `json:"kind,omitempty"`
	ObjectID            string                       `json:"object_id,omitempty"`
	ResultSHA256        string                       `json:"result_sha256,omitempty"`
}
type restoreStoresStep struct{ phase, role, kind string }

func restoreStoresSteps(stores []string) []restoreStoresStep {
	steps := []restoreStoresStep{{"plan-publish", "", ""}}
	for _, phase := range []string{"volume-create", "utility-create", "payload-write", "utility-remove"} {
		steps = append(steps, restoreStoresStep{phase, "configuration", ""})
	}
	for _, kind := range restoredStoreCatalog() {
		selected := false
		for _, role := range stores {
			if role == kind.role {
				selected = true
			}
		}
		if !selected {
			continue
		}
		for _, phase := range []string{"volume-create", "utility-create", "archive-write", "utility-remove"} {
			steps = append(steps, restoreStoresStep{phase, kind.role, ""})
		}
		for _, command := range []string{kind.importKind, kind.verifyKind} {
			for _, phase := range []string{"task-create", "task-execute", "task-remove"} {
				steps = append(steps, restoreStoresStep{phase, kind.role, command})
			}
		}
		steps = append(steps, restoreStoresStep{"volume-remove", kind.role, ""})
	}
	return append(steps, restoreStoresStep{"volume-remove", "configuration", ""}, restoreStoresStep{"complete", "", ""})
}

type restoreStoresRecords struct {
	record                                   *RestoreStoresRecord
	pending, action, outcome, abortReference string
	completed                                map[int]RestoreStoresRecord
	failed, aborting, aborted, complete      bool
}

func restoreStoresState(events []Event) restoreStoresRecords {
	s := restoreStoresRecords{completed: map[int]RestoreStoresRecord{}}
	for _, e := range events {
		if e.RestoreStores == nil {
			continue
		}
		r := *e.RestoreStores
		s.record = &r
		s.outcome = e.Outcome
		if e.Action == "restore_stores_abort" {
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
		if e.Action == "restore_stores_effect" {
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
func restoreStoresIdentity(a, b RestoreStoresRecord) bool {
	a.Step, b.Step = 0, 0
	a.Phase, b.Phase = "", ""
	a.Role, b.Role = "", ""
	a.Kind, b.Kind = "", ""
	a.ObjectID, b.ObjectID = "", ""
	a.ResultSHA256, b.ResultSHA256 = "", ""
	return reflect.DeepEqual(a, b)
}
func restoreStoresAction(action string) bool {
	return action == "restore_stores_effect" || action == "restore_stores_reconcile" || action == "restore_stores_abort"
}
func validRestoreStoresRecord(r *RestoreStoresRecord, events []Event) bool {
	reservation := restoreState(events)
	sql, sqlOK := restoreSQLProof(events)
	database, databaseOK := restoreDatabaseProof(events)
	if r == nil || reservation.record == nil || !reservation.reserved || reservation.pending != "" || reservation.aborted || !sqlOK || !databaseOK || r.SQLProofSHA256 != sql || r.DatabaseProofSHA256 != database || r.RestorePlanSHA256 != reservation.record.PlanSHA256 || !wire.ValidSHA256(r.PlanSHA256) || !identityPattern.MatchString(r.GenerationID) || r.Native == nil && r.GenerationID == reservation.record.AttemptID || r.Native != nil && r.GenerationID != reservation.record.AttemptID || len(r.Stores) < 1 || len(r.Stores) > len(restoredStoreCatalog()) {
		return false
	}
	databaseRecord := restoreDatabaseState(events).record
	if databaseRecord == nil || (r.Native == nil) != (databaseRecord.Native == nil) || r.Native == nil && r.ResultSHA256 != "" || r.Native != nil && (!validMVPNativeRestoreStageRecord(r.Native, mvpNativeRestoreStoresStage, reservation.record.AttemptID) || len(r.Stores) != len(restoredStoreCatalog()) || r.Native.ExecutionSHA256 != databaseRecord.Native.ExecutionSHA256) {
		return false
	}
	previous := -1
	for _, role := range r.Stores {
		position := -1
		for i, kind := range restoredStoreCatalog() {
			if role == kind.role {
				position = i
			}
		}
		if position <= previous {
			return false
		}
		previous = position
	}
	steps := restoreStoresStepsForRecord(r)
	return r.Step >= 0 && r.Step < len(steps) && r.Phase == steps[r.Step].phase && r.Role == steps[r.Step].role && r.Kind == steps[r.Step].kind
}
func restoreStoresProof(events []Event) (string, bool) {
	s := restoreStoresState(events)
	if s.record == nil || !s.complete || s.failed || s.aborted || s.pending != "" || len(s.completed) != len(restoreStoresStepsForRecord(s.record)) {
		return "", false
	}
	if s.record.Native != nil {
		records := make([]RestoreStoresRecord, len(s.completed))
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
func validateRestoreStoresEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_RESTORE_STORES_TRANSITION_INVALID") }
	r := e.RestoreStores
	s := restoreStoresState(events)
	if !identityPattern.MatchString(e.OperationID) || !validRestoreStoresRecord(r, events) || e.RestoreDatabase != nil || e.RestoreSQL != nil || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	if s.record != nil && (!restoreStoresIdentity(*s.record, *r) || s.aborted || s.complete) {
		return bad()
	}
	if e.Action == "restore_stores_abort" {
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
	if e.Action == "restore_stores_effect" && e.Outcome == "started" {
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
	if e.Action == "restore_stores_effect" {
		if s.action != "restore_stores_effect" || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action == "restore_stores_reconcile" {
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
			if !nativeRestoreStoresResultValid(*r, events) {
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
	if e.Outcome == "failed" && r.Phase == "task-execute" && e.Code == "OFFLINE_SCOPE_RESTORE_STORES_TASK_FAILED" {
		return nil
	}
	if codePattern.MatchString(e.Code) && ((e.Action == "restore_stores_effect" && e.Outcome == "uncertain") || (e.Action == "restore_stores_reconcile" && e.Outcome == "unresolved")) {
		return nil
	}
	return bad()
}
