package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ControlReplacementRecord records configuration replacement independently of
// historical runtime creation and restored application activation.
type ControlReplacementRecord struct {
	GenerationID   string         `json:"generation_id"`
	PlanSHA256     string         `json:"plan_sha256"`
	TLSProofSHA256 string         `json:"tls_proof_sha256"`
	RuntimeSHA256  string         `json:"runtime_sha256"`
	ModelSHA256    string         `json:"model_sha256"`
	Step           int            `json:"step"`
	Phase          string         `json:"phase"`
	ObjectID       string         `json:"object_id,omitempty"`
	Effective      *RuntimeRecord `json:"effective_runtime,omitempty"`
}

func controlReplacementSteps() []string {
	return []string{"plan-publish", "inspector-create", "payload-verify", "inspector-remove", "control-remove", "control-create", "control-verify", "complete"}
}

type controlReplacementRecords struct {
	record                                      *ControlReplacementRecord
	pending, outcome, action, recoveryReference string
	completed                                   map[int]ControlReplacementRecord
	complete, notApplied                        bool
}

func controlReplacementState(events []Event) controlReplacementRecords {
	s := controlReplacementRecords{completed: map[int]ControlReplacementRecord{}}
	for _, e := range events {
		if e.ControlReplacement == nil {
			continue
		}
		r := *e.ControlReplacement
		if s.record != nil && s.record.GenerationID != r.GenerationID {
			s = controlReplacementRecords{completed: map[int]ControlReplacementRecord{}}
		}
		s.record = &r
		s.outcome = e.Outcome
		if e.Outcome == "started" {
			s.pending = e.OperationID
			s.action = e.Action
			s.notApplied = false
		}
		if e.Outcome == "not_applied" {
			s.pending = ""
			s.notApplied = true
			s.recoveryReference = e.OperationID
		}
		if e.Outcome == "completed" {
			s.pending = ""
			s.completed[r.Step] = r
			s.complete = r.Phase == "complete"
		}
	}
	return s
}
func controlReplacementAction(action string) bool {
	return action == "control_replacement_effect" || action == "control_replacement_reconcile" || action == "control_replacement_recovery" || action == "control_replacement_retry_effect"
}
func controlReplacementIdentity(a, b ControlReplacementRecord) bool {
	a.Step, b.Step = 0, 0
	a.Phase, b.Phase = "", ""
	a.ObjectID, b.ObjectID = "", ""
	a.Effective, b.Effective = nil, nil
	return reflect.DeepEqual(a, b)
}
func controlReplacementProof(events []Event) (string, bool) {
	s := controlReplacementState(events)
	if !s.complete || s.pending != "" || s.record == nil || s.record.Effective == nil || len(s.completed) != len(controlReplacementSteps()) {
		return "", false
	}
	return wire.Digest(encode(*s.record)), true
}

// Effective identity is derived at the event prefix. Earlier SQL, restore and
// process facts keep their original runtime digest and container IDs.
func effectiveRuntimeState(events []Event) runtimeRecords {
	result := runtimeState(events)
	for _, e := range events {
		if e.Outcome != "completed" {
			continue
		}
		var current *RuntimeRecord
		if e.ControlReplacement != nil && e.ControlReplacement.Phase == "complete" {
			current = e.ControlReplacement.Effective
		}
		if e.CumulativeUpgrade != nil && e.CumulativeUpgrade.Phase == "replacement-complete" {
			current = e.CumulativeUpgrade.Effective
		}
		if current != nil {
			r := *current
			result = runtimeRecords{record: &r, completed: true}
		}
	}
	return result
}
func validateControlReplacementEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_TRANSITION_INVALID") }
	r := e.ControlReplacement
	s := controlReplacementState(events)
	tls := tlsProjectionState(events)
	runtime := runtimeState(events)
	proof, ok := tlsProjectionProof(events)
	if !controlReplacementAction(e.Action) || !identityPattern.MatchString(e.OperationID) || r == nil || !ok || tls.record == nil || r.GenerationID != tls.record.GenerationID || r.TLSProofSHA256 != proof || !wire.ValidSHA256(r.PlanSHA256) || !wire.ValidSHA256(r.ModelSHA256) || runtime.record == nil || !runtime.completed || runtime.pending != "" || r.RuntimeSHA256 != wire.Digest(encode(*runtime.record)) || processState(events).pending != "" || r.Step < 0 || r.Step >= len(controlReplacementSteps()) || r.Phase != controlReplacementSteps()[r.Step] {
		return bad()
	}
	if e.TLSProjection != nil || e.RestoreStores != nil || e.RestoreDatabase != nil || e.RestoreSQL != nil || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	if s.complete && s.record != nil && s.record.GenerationID != r.GenerationID && e.Action == "control_replacement_effect" && e.Outcome == "started" && r.Step == 0 {
		for _, prior := range events {
			if prior.ControlReplacement != nil && prior.ControlReplacement.GenerationID == r.GenerationID {
				return bad()
			}
		}
		s = controlReplacementRecords{completed: map[int]ControlReplacementRecord{}}
	}
	if s.complete || s.record != nil && !controlReplacementIdentity(*s.record, *r) {
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
	if e.Action == "control_replacement_effect" && e.Outcome == "started" || e.Action == "control_replacement_retry_effect" && e.Outcome == "started" {
		if s.pending != "" || r.Step != len(s.completed) || e.Code != "" || r.ObjectID != "" || r.Effective != nil || !unused() {
			return bad()
		}
		if e.Action == "control_replacement_effect" && (s.notApplied || e.ReferenceOperationID != "") {
			return bad()
		}
		if e.Action == "control_replacement_retry_effect" && (!s.notApplied || e.ReferenceOperationID != s.recoveryReference || s.recoveryReference == "") {
			return bad()
		}
		return nil
	}
	if s.record == nil || s.pending == "" || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "control_replacement_recovery" {
		if e.Outcome != "not_applied" || e.ReferenceOperationID != s.pending || e.Code != "" || r.ObjectID != "" || r.Effective != nil || !unused() {
			return bad()
		}
		switch r.Phase {
		case "inspector-create", "inspector-remove", "control-remove", "control-create":
			return nil
		default:
			return bad()
		}
	}
	if e.Action == "control_replacement_effect" || e.Action == "control_replacement_retry_effect" {
		reference := ""
		if e.Action == "control_replacement_retry_effect" {
			reference = s.recoveryReference
		}
		if s.outcome != "started" || s.action != e.Action || e.OperationID != s.pending || e.ReferenceOperationID != reference {
			return bad()
		}
	} else if e.ReferenceOperationID != s.pending || !unused() {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" {
		switch r.Phase {
		case "inspector-create", "control-create", "control-verify":
			if !utilityIDPattern.MatchString(r.ObjectID) || r.Effective != nil {
				return bad()
			}
			if r.Phase == "control-create" && r.ObjectID == runtime.record.Services["control"].ID {
				return bad()
			}
			if r.Phase == "control-verify" && r.ObjectID != s.completed[controlReplacementCreateStep].ObjectID {
				return bad()
			}
		case "complete":
			if r.ObjectID != "" || !validRuntimeRecord(r.Effective) || r.Effective.ModelSHA256 != r.ModelSHA256 {
				return bad()
			}
			expected := *runtime.record
			expected.ModelSHA256 = r.ModelSHA256
			expected.Services = make(map[string]compose.RuntimeServiceStatus, len(runtime.record.Services))
			for name, service := range runtime.record.Services {
				expected.Services[name] = service
			}
			control := expected.Services["control"]
			control.ID = s.completed[controlReplacementCreateStep].ObjectID
			expected.Services["control"] = control
			if !reflect.DeepEqual(expected, *r.Effective) {
				return bad()
			}
		default:
			if r.ObjectID != "" || r.Effective != nil {
				return bad()
			}
		}
		return nil
	}
	if r.ObjectID != "" || r.Effective != nil || !codePattern.MatchString(e.Code) {
		return bad()
	}
	if (e.Action == "control_replacement_effect" || e.Action == "control_replacement_retry_effect") && e.Outcome == "uncertain" || e.Action == "control_replacement_reconcile" && e.Outcome == "unresolved" {
		return nil
	}
	return bad()
}
