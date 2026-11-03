package scope

import (
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoredCurrentStoresRecord struct {
	StoreCount                int                         `json:"store_count,omitempty"`
	GenerationID              string                      `json:"generation_id"`
	PlanSHA256                string                      `json:"plan_sha256"`
	Admission                 RestoredDependencyAdmission `json:"admission"`
	DatabaseProofSHA256       string                      `json:"database_proof_sha256"`
	SelectedStoresProofSHA256 string                      `json:"selected_stores_proof_sha256"`
	Step                      int                         `json:"step"`
	ObjectID                  string                      `json:"object_id,omitempty"`
}

func currentStoresSteps(counts ...int) []restoreDatabaseStep {
	count := 0
	if len(counts) > 1 {
		return nil
	}
	if len(counts) == 1 {
		count = counts[0]
	}
	if count != 0 && count != 4 {
		return nil
	}
	kinds := restoredStoreCatalog()
	if count == 0 {
		kinds = kinds[:2]
	}
	steps := []restoreDatabaseStep{{"plan-publish", ""}, {"volume-create", ""}, {"utility-create", ""}, {"payload-write", ""}, {"utility-remove", ""}}
	for _, kind := range kinds {
		for _, phase := range []string{"task-create", "task-execute", "task-remove"} {
			steps = append(steps, restoreDatabaseStep{phase, kind.verifyKind})
		}
	}
	return append(steps, restoreDatabaseStep{"volume-remove", ""}, restoreDatabaseStep{"complete", ""})
}

type currentStoresRecords struct {
	record                                   *RestoredCurrentStoresRecord
	pending, action, outcome, abortReference string
	completed                                map[int]RestoredCurrentStoresRecord
	complete, failed, aborting, aborted      bool
}

func currentStoresState(events []Event) currentStoresRecords {
	s := currentStoresRecords{completed: map[int]RestoredCurrentStoresRecord{}}
	for _, e := range events {
		if e.RestoredCurrentStores == nil {
			continue
		}
		r := *e.RestoredCurrentStores
		if s.record == nil || s.record.GenerationID != r.GenerationID {
			s = currentStoresRecords{completed: map[int]RestoredCurrentStoresRecord{}}
		}
		s.record, s.outcome = &r, e.Outcome
		if e.Action == "current_stores_abort" {
			if e.Outcome == "started" {
				s.pending, s.action, s.abortReference, s.aborting = e.OperationID, e.Action, e.ReferenceOperationID, true
			}
			if e.Outcome == "completed" {
				s.pending, s.aborting, s.aborted = "", false, true
			}
			continue
		}
		if e.Outcome == "started" {
			s.pending, s.action = e.OperationID, e.Action
		}
		if e.Outcome == "completed" {
			s.pending = ""
			s.completed[r.Step] = r
			s.complete = r.Step == len(currentStoresSteps(r.StoreCount))-1
		}
		if e.Outcome == "failed" {
			s.pending, s.failed = "", true
		}
	}
	return s
}
func currentStoresAction(action string) bool {
	return action == "current_stores_effect" || action == "current_stores_reconcile" || action == "current_stores_abort"
}
func currentStoresIdentity(a, b RestoredCurrentStoresRecord) bool {
	a.Step, b.Step, a.ObjectID, b.ObjectID = 0, 0, "", ""
	return reflect.DeepEqual(a, b)
}
func currentStoresProof(events []Event) (string, bool) {
	s := currentStoresState(events)
	if s.record == nil || !s.complete || s.pending != "" || s.failed || s.aborted || len(s.completed) != len(currentStoresSteps(s.record.StoreCount)) {
		return "", false
	}
	return wire.Digest(encode(*s.record)), true
}
func validateCurrentStoresEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CURRENT_STORES_TRANSITION_INVALID") }
	r, s, admission := e.RestoredCurrentStores, currentStoresState(events), dependencyAdmission(events)
	database, databaseOK := liveDatabaseProof(events)
	stores, storesOK := restoreStoresProof(events)
	if !databaseOK || !storesOK || r == nil || r.DatabaseProofSHA256 != database || r.SelectedStoresProofSHA256 != stores {
		return bad()
	}
	if !currentStoresAction(e.Action) || r == nil || admission == nil || !reflect.DeepEqual(r.Admission, *admission) || !identityPattern.MatchString(r.GenerationID) || !identityPattern.MatchString(e.OperationID) || !wire.ValidSHA256(r.PlanSHA256) || r.Step < 0 || r.Step >= len(currentStoresSteps(r.StoreCount)) || processState(events).pending != "" {
		return bad()
	}
	selected := restoreStoresState(events).record
	if selected == nil || (len(selected.Stores) == 4) != (r.StoreCount == 4) {
		return bad()
	}
	if e.RestoredLiveDatabase != nil || e.ControlReplacement != nil || e.TLSProjection != nil || e.RestoreStores != nil || e.RestoreDatabase != nil || e.RestoreSQL != nil || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	unused := func() bool {
		for _, p := range events {
			if p.OperationID == e.OperationID {
				return false
			}
		}
		return true
	}
	if s.record == nil || s.record.GenerationID != r.GenerationID {
		if s.record != nil && !s.complete && !s.aborted {
			return bad()
		}
		for _, p := range events {
			if p.OperationID == r.GenerationID || p.RestoredLiveDatabase != nil && p.RestoredLiveDatabase.GenerationID == r.GenerationID || p.RestoredCurrentStores != nil && p.RestoredCurrentStores.GenerationID == r.GenerationID || p.Volume != nil && p.Volume.ResourceOperationID == r.GenerationID || p.TLSProjection != nil && p.TLSProjection.GenerationID == r.GenerationID {
				return bad()
			}
		}
		if e.Action != "current_stores_effect" || e.Outcome != "started" || r.Step != 0 || r.ObjectID != "" || e.Code != "" || e.ReferenceOperationID != "" || !unused() {
			return bad()
		}
		return nil
	}
	if !currentStoresIdentity(*s.record, *r) || s.complete || s.aborted {
		return bad()
	}
	if e.Action == "current_stores_abort" {
		if r.Step != s.record.Step || r.ObjectID != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if s.aborting || e.ReferenceOperationID != s.pending || e.Code != "" || !unused() {
				return bad()
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
	if s.failed || s.aborting {
		return bad()
	}
	if e.Action == "current_stores_effect" && e.Outcome == "started" {
		if s.pending != "" || r.Step != len(s.completed) || r.ObjectID != "" || e.Code != "" || e.ReferenceOperationID != "" || !unused() {
			return bad()
		}
		return nil
	}
	if s.pending == "" || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "current_stores_effect" {
		if s.action != e.Action || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action != "current_stores_reconcile" || e.ReferenceOperationID != s.pending || !unused() {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" {
		phase := currentStoresSteps(r.StoreCount)[r.Step].phase
		if phase == "utility-create" || phase == "task-create" {
			if !utilityIDPattern.MatchString(r.ObjectID) {
				return bad()
			}
		} else if r.ObjectID != "" {
			return bad()
		}
		return nil
	}
	if r.ObjectID != "" {
		return bad()
	}
	if e.Outcome == "failed" && currentStoresSteps(r.StoreCount)[r.Step].phase == "task-execute" && e.Code == restoreDatabaseTaskFailed {
		return nil
	}
	if codePattern.MatchString(e.Code) && (e.Action == "current_stores_effect" && e.Outcome == "uncertain" || e.Action == "current_stores_reconcile" && e.Outcome == "unresolved") {
		return nil
	}
	return bad()
}
