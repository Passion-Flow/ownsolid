package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RuntimeRecord struct {
	IngressNetworkID string                                  `json:"ingress_network_id,omitempty"`
	DescriptorSHA256 string                                  `json:"descriptor_sha256"`
	ModelSHA256      string                                  `json:"model_sha256"`
	NetworkID        string                                  `json:"network_id,omitempty"`
	Services         map[string]compose.RuntimeServiceStatus `json:"services,omitempty"`
	RecoverySHA256   string                                  `json:"recovery_sha256,omitempty"`
}
type runtimeRecords struct {
	record    *RuntimeRecord
	pending   string
	completed bool
}

func runtimeState(events []Event) runtimeRecords {
	state := runtimeRecords{}
	for _, event := range events {
		if event.Action == runtimeAbsenceRecoveryAction {
			state = runtimeRecords{}
		}
		if event.Action == "runtime_create" {
			state.record = event.Runtime
			if event.Outcome == "completed" {
				state.pending = ""
				state.completed = true
			} else {
				state.pending = event.OperationID
			}
		}
		if event.Action == "runtime_reconcile" {
			state.record = event.Runtime
			if event.Outcome == "completed" {
				state.pending = ""
				state.completed = true
			}
		}
	}
	return state
}
func validRuntimeRecord(r *RuntimeRecord) bool {
	if r == nil || r.RecoverySHA256 != "" && !wire.ValidSHA256(r.RecoverySHA256) || r.IngressNetworkID != "" && !utilityIDPattern.MatchString(r.IngressNetworkID) || !wire.ValidSHA256(r.DescriptorSHA256) || !wire.ValidSHA256(r.ModelSHA256) || r.NetworkID != "" && !utilityIDPattern.MatchString(r.NetworkID) || len(r.Services) > 3 {
		return false
	}
	seen := map[string]bool{}
	for name, s := range r.Services {
		if name != "control" && name != "postgres" && name != "gateway" || !utilityIDPattern.MatchString(s.ID) || s.State != "created" || s.ExitCode != 0 || seen[s.ID] || r.NetworkID == "" {
			return false
		}
		seen[s.ID] = true
	}
	return true
}
func runtimeProgress(a, b RuntimeRecord) bool {
	if a.RecoverySHA256 != b.RecoverySHA256 || a.DescriptorSHA256 != b.DescriptorSHA256 || a.ModelSHA256 != b.ModelSHA256 || a.NetworkID != "" && a.NetworkID != b.NetworkID || a.IngressNetworkID != "" && a.IngressNetworkID != b.IngressNetworkID {
		return false
	}
	for name, s := range a.Services {
		if b.Services[name] != s {
			return false
		}
	}
	return true
}
func validateRuntimeEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_RUNTIME_JOURNAL_TRANSITION_INVALID") }
	if modelTaskState(events).record != nil {
		a := modelTaskActionState(events)
		if a.pending != "" || !a.removed || !a.initialized {
			return bad()
		}
	}
	if !validRuntimeRecord(e.Runtime) || e.Process != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil {
		return bad()
	}
	input, outcome, _ := inputsState(events)
	volumes := volumeState(events)
	if input == nil || outcome != "completed" || input.DescriptorSHA256 != e.Runtime.DescriptorSHA256 || input.ModelSHA256 != e.Runtime.ModelSHA256 || len(imageState(events).pending) > 0 || len(volumes.pending) > 0 || len(volumes.roles) == 0 {
		return bad()
	}
	for _, r := range volumes.roles {
		if r.Phase != phasePrepared {
			return bad()
		}
	}
	state := runtimeState(events)
	if state.completed {
		return bad()
	}
	if e.Action == runtimeAbsenceRecoveryAction {
		if e.Outcome != "completed" || e.Code != "" || state.pending == "" || e.ReferenceOperationID != state.pending || state.record == nil || !runtimeRecordAbsent(*state.record) || !runtimeRecordAbsent(*e.Runtime) || e.Runtime.DescriptorSHA256 != state.record.DescriptorSHA256 || e.Runtime.ModelSHA256 != state.record.ModelSHA256 || !wire.ValidSHA256(e.Runtime.RecoverySHA256) || !runtimeRecoveryInitialHistory(events) {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if e.Runtime.RecoverySHA256 != "" {
		return bad()
	}
	if e.Action == "runtime_create" {
		if e.ReferenceOperationID != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if state.record != nil || e.Code != "" || e.Runtime.NetworkID != "" || e.Runtime.IngressNetworkID != "" || len(e.Runtime.Services) > 0 {
				return bad()
			}
			for _, prior := range events {
				if prior.OperationID == e.OperationID {
					return bad()
				}
			}
			return nil
		}
		if state.pending != e.OperationID || state.record == nil || !runtimeProgress(*state.record, *e.Runtime) {
			return bad()
		}
		// Once an uncertain terminal is written, only explicit reconciliation may follow.
		for _, prior := range events {
			if prior.Action == "runtime_create" && prior.OperationID == e.OperationID && prior.Outcome == "uncertain" {
				return bad()
			}
		}
		if e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
		if e.Outcome == "completed" && e.Code == "" && len(e.Runtime.Services) > 0 && e.Runtime.NetworkID != "" {
			return nil
		}
		return bad()
	}
	if e.Action == "runtime_reconcile" {
		if state.pending == "" || state.pending != e.ReferenceOperationID || state.record == nil || !runtimeProgress(*state.record, *e.Runtime) {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		if e.Outcome == "unresolved" && codePattern.MatchString(e.Code) {
			return nil
		}
		if e.Outcome == "completed" && e.Code == "" && len(e.Runtime.Services) > 0 && e.Runtime.NetworkID != "" {
			return nil
		}
	}
	return bad()
}
func validateRuntimeInputs(state runtimeRecords, p preparedInputs) error {
	if state.record == nil {
		return nil
	}
	r := state.record
	if r.DescriptorSHA256 != p.record.DescriptorSHA256 || r.ModelSHA256 != p.record.ModelSHA256 {
		return failure("OFFLINE_SCOPE_RUNTIME_INPUT_IDENTITY_MISMATCH")
	}
	model, e := compose.DecodeModel(p.model, int64(len(p.model)))
	if e != nil {
		return e
	}
	if !compose.RequiresIngress(model) && r.IngressNetworkID != "" || state.completed && compose.RequiresIngress(model) && r.IngressNetworkID == "" {
		return failure("OFFLINE_SCOPE_RUNTIME_TOPOLOGY_PROOF_REQUIRED")
	}
	for name := range r.Services {
		if _, ok := model.Services[name]; !ok {
			return failure("OFFLINE_SCOPE_RUNTIME_INPUT_IDENTITY_MISMATCH")
		}
	}
	if state.completed && len(r.Services) != len(model.Services) {
		return failure("OFFLINE_SCOPE_RUNTIME_INPUT_IDENTITY_MISMATCH")
	}
	return nil
}
