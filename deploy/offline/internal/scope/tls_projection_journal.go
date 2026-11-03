package scope

import (
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// TLSProjectionRecord binds a fixed preparation sequence to an independently
// admitted private plan. Completion alone never authorizes container replacement or serving.
type TLSProjectionRecord struct {
	GenerationID     string `json:"generation_id"`
	PlanSHA256       string `json:"plan_sha256"`
	StoreProofSHA256 string `json:"store_proof_sha256"`
	DescriptorSHA256 string `json:"descriptor_sha256"`
	RuntimeSHA256    string `json:"runtime_sha256"`
	Step             int    `json:"step"`
	Phase            string `json:"phase"`
	ObjectID         string `json:"object_id,omitempty"`
	PayloadCustody   bool   `json:"payload_custody,omitempty"`
}

func tlsProjectionSteps() []string {
	return []string{"plan-publish", "volume-create", "utility-create", "payload-write", "utility-remove", "complete"}
}

type tlsProjectionRecords struct {
	record                                   *TLSProjectionRecord
	pending, action, outcome, abortReference string
	completed                                map[int]TLSProjectionRecord
	aborting, aborted, complete              bool
}

func tlsProjectionState(events []Event) tlsProjectionRecords {
	s := tlsProjectionRecords{completed: map[int]TLSProjectionRecord{}}
	for _, e := range events {
		if e.TLSProjection == nil {
			continue
		}
		r := *e.TLSProjection
		if s.record != nil && s.record.GenerationID != r.GenerationID {
			s = tlsProjectionRecords{completed: map[int]TLSProjectionRecord{}}
		}
		s.record = &r
		s.outcome = e.Outcome
		if e.Action == "tls_projection_abort" {
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
		if e.Action == "tls_projection_effect" {
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
	}
	return s
}
func tlsProjectionAction(action string) bool {
	return action == "tls_projection_effect" || action == "tls_projection_reconcile" || action == "tls_projection_abort"
}
func tlsProjectionIdentity(a, b TLSProjectionRecord) bool {
	a.Step, b.Step = 0, 0
	a.Phase, b.Phase = "", ""
	a.ObjectID, b.ObjectID = "", ""
	return reflect.DeepEqual(a, b)
}
func validTLSProjectionRecord(r *TLSProjectionRecord, events []Event) bool {
	if r == nil || !identityPattern.MatchString(r.GenerationID) || !wire.ValidSHA256(r.PlanSHA256) {
		return false
	}
	proof, ok := restoreStoresProof(events)
	restore := restoreState(events)
	stores := restoreStoresState(events)
	runtime := runtimeState(events)
	input, outcome, _ := inputsState(events)
	if !ok || restore.record == nil || restore.aborted || !restore.reserved || restore.pending != "" || stores.record == nil || r.StoreProofSHA256 != proof || r.GenerationID == restore.record.AttemptID || r.GenerationID == stores.record.GenerationID || input == nil || outcome != "completed" || r.DescriptorSHA256 != input.DescriptorSHA256 || runtime.record == nil || !runtime.completed || runtime.pending != "" || r.RuntimeSHA256 != wire.Digest(encode(*runtime.record)) {
		return false
	}
	for _, e := range events {
		if e.TLSProjection == nil && (e.OperationID == r.GenerationID || e.Volume != nil && e.Volume.ResourceOperationID == r.GenerationID) {
			return false
		}
	}
	steps := tlsProjectionSteps()
	return r.Step >= 0 && r.Step < len(steps) && r.Phase == steps[r.Step]
}
func tlsProjectionProof(events []Event) (string, bool) {
	s := tlsProjectionState(events)
	if s.record == nil || !s.complete || s.pending != "" || s.aborted || s.aborting || len(s.completed) != len(tlsProjectionSteps()) {
		return "", false
	}
	return wire.Digest(encode(*s.record)), true
}
func validateTLSProjectionEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_TLS_PROJECTION_TRANSITION_INVALID") }
	r := e.TLSProjection
	s := tlsProjectionState(events)
	if !identityPattern.MatchString(e.OperationID) || !validTLSProjectionRecord(r, events) || e.RestoreStores != nil || e.RestoreDatabase != nil || e.RestoreSQL != nil || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	if s.record != nil && (s.aborted || rotationReady(events)) && r.GenerationID != s.record.GenerationID && e.Action == "tls_projection_effect" && e.Outcome == "started" && r.Step == 0 {
		for _, prior := range events {
			if prior.TLSProjection != nil && prior.TLSProjection.GenerationID == r.GenerationID {
				return bad()
			}
		}
		s = tlsProjectionRecords{completed: map[int]TLSProjectionRecord{}}
	}
	if s.record != nil && (!tlsProjectionIdentity(*s.record, *r) || s.aborted) {
		return bad()
	}
	unused := func() bool {
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return false
			}
		}
		return true
	}
	if e.Action == "tls_projection_abort" {
		if s.record == nil || r.Step != s.record.Step || r.ObjectID != "" {
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
	if s.complete || s.aborting {
		return bad()
	}
	if e.Action == "tls_projection_effect" && e.Outcome == "started" {
		if s.pending != "" || e.ReferenceOperationID != "" || e.Code != "" || r.ObjectID != "" || r.Step != len(s.completed) || !unused() {
			return bad()
		}
		return nil
	}
	if s.record == nil || s.pending == "" || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "tls_projection_effect" {
		if s.action != "tls_projection_effect" || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action == "tls_projection_reconcile" {
		if e.ReferenceOperationID != s.pending || !unused() {
			return bad()
		}
	} else {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" {
		if r.Phase == "utility-create" {
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
	if codePattern.MatchString(e.Code) && (e.Action == "tls_projection_effect" && e.Outcome == "uncertain" || e.Action == "tls_projection_reconcile" && e.Outcome == "unresolved") {
		return nil
	}
	return bad()
}
