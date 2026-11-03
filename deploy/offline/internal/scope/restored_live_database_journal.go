package scope

import (
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoredLiveDatabaseRecord struct {
	GenerationID  string                      `json:"generation_id"`
	PlanSHA256    string                      `json:"plan_sha256"`
	Admission     RestoredDependencyAdmission `json:"admission"`
	Step          int                         `json:"step"`
	ObjectID      string                      `json:"object_id,omitempty"`
	NativeRuntime *mvpNativeRuntimeActivation `json:"native_runtime,omitempty"`
}

func liveDatabaseSteps(native ...*mvpNativeRuntimeActivation) []restoreDatabaseStep {
	var variant *mvpNativeRuntimeActivation
	if len(native) > 0 {
		variant = native[0]
	}
	steps := []restoreDatabaseStep{{"plan-publish", ""}, {"volume-create", ""}, {"utility-create", ""}, {"payload-write", ""}, {"utility-remove", ""}}
	for _, kind := range liveDatabaseTaskKinds(variant) {
		for _, phase := range []string{"task-create", "task-execute", "task-remove"} {
			steps = append(steps, restoreDatabaseStep{phase, kind})
		}
	}
	return append(steps, restoreDatabaseStep{"volume-remove", ""}, restoreDatabaseStep{"complete", ""})
}

type liveDatabaseRecords struct {
	record                                   *RestoredLiveDatabaseRecord
	pending, action, outcome, abortReference string
	completed                                map[int]RestoredLiveDatabaseRecord
	complete, failed, aborting, aborted      bool
}

func liveDatabaseState(events []Event) liveDatabaseRecords {
	s := liveDatabaseRecords{completed: map[int]RestoredLiveDatabaseRecord{}}
	for _, e := range events {
		if e.RestoredLiveDatabase == nil {
			continue
		}
		r := *e.RestoredLiveDatabase
		if s.record == nil || s.record.GenerationID != r.GenerationID {
			s = liveDatabaseRecords{completed: map[int]RestoredLiveDatabaseRecord{}}
		}
		s.record, s.outcome = &r, e.Outcome
		if e.Action == "live_database_abort" {
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
			s.complete = r.Step == len(liveDatabaseSteps(r.NativeRuntime))-1
		}
		if e.Outcome == "failed" {
			s.pending, s.failed = "", true
		}
	}
	return s
}
func liveDatabaseAction(action string) bool {
	return action == "live_database_effect" || action == "live_database_reconcile" || action == "live_database_abort"
}
func liveDatabaseIdentity(a, b RestoredLiveDatabaseRecord) bool {
	a.Step, b.Step, a.ObjectID, b.ObjectID = 0, 0, "", ""
	return reflect.DeepEqual(a, b)
}
func liveDatabaseProof(events []Event) (string, bool) {
	s := liveDatabaseState(events)
	if s.record == nil || !s.complete || s.pending != "" || s.failed || s.aborted || len(s.completed) != len(liveDatabaseSteps(s.record.NativeRuntime)) {
		return "", false
	}
	return wire.Digest(encode(*s.record)), true
}
func validateLiveDatabaseEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_LIVE_DATABASE_TRANSITION_INVALID") }
	r, s, admission := e.RestoredLiveDatabase, liveDatabaseState(events), dependencyAdmission(events)
	if !liveDatabaseAction(e.Action) || r == nil || !validMVPNativeActivation(events, r.NativeRuntime) || admission == nil || !reflect.DeepEqual(r.Admission, *admission) || !identityPattern.MatchString(r.GenerationID) || !identityPattern.MatchString(e.OperationID) || !wire.ValidSHA256(r.PlanSHA256) || r.Step < 0 || r.Step >= len(liveDatabaseSteps(r.NativeRuntime)) || processState(events).pending != "" {
		return bad()
	}
	if e.ControlReplacement != nil || e.TLSProjection != nil || e.RestoreStores != nil || e.RestoreDatabase != nil || e.RestoreSQL != nil || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
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
		if r.NativeRuntime != nil {
			generation, proof, err := mvpNativePreparation(events, *r.NativeRuntime)
			if err != nil || generation != r.NativeRuntime.PreparationGenerationID || proof != r.NativeRuntime.PreparationProofSHA256 {
				return bad()
			}
		}
		if s.record != nil && !s.complete && !s.aborted {
			return bad()
		}
		for _, p := range events {
			if p.OperationID == r.GenerationID || p.RestoredLiveDatabase != nil && p.RestoredLiveDatabase.GenerationID == r.GenerationID || p.Volume != nil && p.Volume.ResourceOperationID == r.GenerationID || p.TLSProjection != nil && p.TLSProjection.GenerationID == r.GenerationID {
				return bad()
			}
		}
		if e.Action != "live_database_effect" || e.Outcome != "started" || r.Step != 0 || r.ObjectID != "" || e.Code != "" || e.ReferenceOperationID != "" || !unused() {
			return bad()
		}
		return nil
	}
	if !liveDatabaseIdentity(*s.record, *r) || s.complete || s.aborted {
		return bad()
	}
	if e.Action == "live_database_abort" {
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
	if e.Action == "live_database_effect" && e.Outcome == "started" {
		if s.pending != "" || r.Step != len(s.completed) || r.ObjectID != "" || e.Code != "" || e.ReferenceOperationID != "" || !unused() {
			return bad()
		}
		return nil
	}
	if s.pending == "" || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "live_database_effect" {
		if s.action != e.Action || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action != "live_database_reconcile" || e.ReferenceOperationID != s.pending || !unused() {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" {
		phase := liveDatabaseSteps(r.NativeRuntime)[r.Step].phase
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
	if e.Outcome == "failed" && liveDatabaseSteps(r.NativeRuntime)[r.Step].phase == "task-execute" && e.Code == restoreDatabaseTaskFailed {
		return nil
	}
	if codePattern.MatchString(e.Code) && (e.Action == "live_database_effect" && e.Outcome == "uncertain" || e.Action == "live_database_reconcile" && e.Outcome == "unresolved") {
		return nil
	}
	return bad()
}
