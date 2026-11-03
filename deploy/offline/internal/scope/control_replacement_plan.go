package scope

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const controlReplacementPlanFile = "control-replacement.json"
const controlReplacementCreateStep = 5

func controlReplacementPlanName(events []Event, generation string) string {
	for _, event := range events {
		if event.ControlReplacement != nil {
			if event.ControlReplacement.GenerationID == generation {
				return controlReplacementPlanFile
			}
			break
		}
	}
	if lastCompletedReplacement(events) == nil {
		return controlReplacementPlanFile
	}
	return "control-replacement-" + generation + ".json"
}

func validControlReplacementPlanName(name string) bool {
	generation := strings.TrimSuffix(strings.TrimPrefix(name, "control-replacement-"), ".json")
	return identityPattern.MatchString(generation) && name == "control-replacement-"+generation+".json"
}

// Private supported metadata is retained for exact stopped deletion, never
// returned as a diagnostic. Original input and restore records stay immutable.
type ControlReplacementPlan struct {
	FormatVersion          int                   `json:"format_version"`
	ScopeID                string                `json:"scope_id"`
	GenerationID           string                `json:"generation_id"`
	TLSProofSHA256         string                `json:"tls_proof_sha256"`
	TLSPlanSHA256          string                `json:"tls_plan_sha256"`
	RuntimeSHA256          string                `json:"runtime_sha256"`
	ModelSHA256            string                `json:"model_sha256"`
	PreviousMetadataSHA256 string                `json:"previous_metadata_sha256"`
	PreviousControlJSON    string                `json:"previous_control_json"`
	Before                 compose.RuntimeStatus `json:"before"`
}

func controlReplacementPlanRecord(p ControlReplacementPlan) ControlReplacementRecord {
	return ControlReplacementRecord{GenerationID: p.GenerationID, PlanSHA256: wire.Digest(encode(p)), TLSProofSHA256: p.TLSProofSHA256, RuntimeSHA256: p.RuntimeSHA256, ModelSHA256: p.ModelSHA256, Phase: controlReplacementSteps()[0]}
}
func (l *locked) replacementModels(ctx context.Context) (preparedInputs, compose.Model, error) {
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return preparedInputs{}, compose.Model{}, failure("OFFLINE_SCOPE_COMPLETED_INPUT_SNAPSHOT_REQUIRED")
	}
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return p, compose.Model{}, e
	}
	model, e := compose.DecodeModel(p.model, l.config.Verification.MaximumMetadataBytes)
	return p, model, e
}
func (l *locked) validateControlReplacementPlan(ctx context.Context, p ControlReplacementPlan) error {
	view := *l
	view.events = generationPrefix(l.events, p.GenerationID, true)
	return view.validateControlReplacementPlanAtAdmission(ctx, p)
}
func (l *locked) validateControlReplacementPlanAtAdmission(ctx context.Context, p ControlReplacementPlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_PLAN_CONTEXT_CHANGED") }
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	tls := tlsProjectionState(l.events)
	proof, ok := tlsProjectionProof(l.events)
	original := runtimeState(l.events)
	effective := effectiveRuntimeState(l.events)
	if p.FormatVersion != 1 || p.ScopeID != l.marker.ScopeID || !ok || tls.record == nil || original.record == nil || !original.completed || original.pending != "" || p.GenerationID != tls.record.GenerationID || p.TLSProofSHA256 != proof || p.TLSPlanSHA256 != tls.record.PlanSHA256 || p.RuntimeSHA256 != wire.Digest(encode(*original.record)) {
		return bad()
	}
	previous, e := p.previousControl()
	if e != nil {
		return e
	}
	generation, e := l.readTLSProjectionPlan(*tls.record)
	if e != nil {
		return e
	}
	_, model, e := l.replacementModels(ctx)
	if e != nil {
		return e
	}
	next, e := compose.ControlConfigurationModel(model, p.GenerationID)
	if e != nil || p.ModelSHA256 != wire.Digest(encode(next)) || previous.ID != generation.PreviousControlID {
		return bad()
	}
	fingerprint, e := engine.RuntimeContainerDigest(previous)
	if e != nil || fingerprint != p.PreviousMetadataSHA256 || !l.cleanRotationControl(previous) || effective.record == nil || p.Before.Status != "observed_complete_compose_resources" || !runtimeIDsMatch(*effective.record, p.Before) || len(p.Before.Services) != len(original.record.Services) {
		return bad()
	}
	if previous.Name != "/"+engine.RuntimeContainerName(p.ScopeID, "control") || previous.Config.Labels["io.projectone.isolated-scope"] != p.ScopeID || p.Before.Services["control"].State != previous.State.Status {
		return bad()
	}
	for _, s := range p.Before.Services {
		if s.State != "created" && s.State != "exited" || s.ExitCode != 0 {
			return bad()
		}
	}
	return nil
}
func (l *locked) readControlReplacementPlan() (ControlReplacementPlan, error) {
	var p ControlReplacementPlan
	s := controlReplacementState(l.events)
	if s.record == nil {
		return p, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_NOT_RECORDED")
	}
	raw, e := regular(l.root, controlReplacementPlanName(l.events, s.record.GenerationID), l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != s.record.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !controlReplacementIdentity(controlReplacementPlanRecord(p), *s.record) {
		return p, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_PLAN_UNAVAILABLE_OR_CHANGED")
	}
	if e = l.validateControlReplacementPlan(context.Background(), p); e != nil {
		return p, e
	}
	return p, nil
}
func (l *locked) verifyControlReplacementPlan() error {
	last := map[string]int{}
	for i, event := range l.events {
		if event.ControlReplacement != nil {
			last[event.ControlReplacement.GenerationID] = i
		}
	}
	for i, event := range l.events {
		if event.ControlReplacement == nil {
			continue
		}
		if last[event.ControlReplacement.GenerationID] != i {
			continue
		}
		view := *l
		view.events = l.events[:i+1]
		s := controlReplacementState(view.events)
		if _, e := view.readControlReplacementPlan(); e != nil {
			if _, published := s.completed[0]; published || s.record.Step > 0 {
				return e
			}
		}
	}
	return nil
}
func (l *locked) publishControlReplacementPlan(ctx context.Context, p ControlReplacementPlan, reconcile bool, publish func() error) error {
	if e := l.validateControlReplacementPlan(ctx, p); e != nil {
		return e
	}
	r := controlReplacementPlanRecord(p)
	s := controlReplacementState(l.events)
	if s.complete && s.record != nil && s.record.GenerationID != p.GenerationID {
		s = controlReplacementRecords{completed: map[int]ControlReplacementRecord{}}
	}
	if s.record != nil && !controlReplacementIdentity(*s.record, r) {
		return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_CONTEXT_CHANGED")
	}
	if _, done := s.completed[0]; done {
		_, e := l.readControlReplacementPlan()
		return e
	}
	if s.record != nil && !reconcile {
		return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_RECONCILIATION_REQUIRED")
	}
	if s.record == nil && reconcile {
		return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_NOT_RECORDED")
	}
	if e := admitProcessJournal(l, 3); e != nil {
		return e
	}
	id, e := nonce()
	if e != nil {
		return e
	}
	action, reference := "control_replacement_effect", ""
	if reconcile {
		action = "control_replacement_reconcile"
		reference = s.pending
	} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", ControlReplacement: &r}); e != nil {
		return e
	}
	name := controlReplacementPlanName(l.events, p.GenerationID)
	_, exists := l.root.Lstat(name)
	if os.IsNotExist(exists) {
		if publish == nil {
			e = failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_PLAN_PUBLISHER_REQUIRED")
		} else {
			e = publish()
		}
	} else if exists != nil {
		e = failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_PLAN_UNAVAILABLE")
	}
	if e == nil {
		e = l.syncNamedRestorePlan(name, encode(p))
	}
	if e == nil {
		_, e = l.readControlReplacementPlan()
	}
	outcome, code := "completed", ""
	if e != nil {
		outcome = "uncertain"
		if reconcile {
			outcome = "unresolved"
		}
		code = "OFFLINE_SCOPE_CONTROL_REPLACEMENT_PLAN_UNRESOLVED"
	}
	if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, ControlReplacement: &r}); je != nil {
		return je
	}
	return e
}
func replacementEffectiveRecord(original RuntimeRecord, modelSHA, newID string) RuntimeRecord {
	next := original
	next.ModelSHA256 = modelSHA
	next.Services = map[string]compose.RuntimeServiceStatus{}
	for name, service := range original.Services {
		next.Services[name] = service
	}
	control := next.Services["control"]
	control.ID = newID
	next.Services["control"] = control
	return next
}

func (p ControlReplacementPlan) previousControl() (engine.RuntimeContainer, error) {
	var object engine.RuntimeContainer
	if wire.UniqueJSON([]byte(p.PreviousControlJSON), &object) != nil {
		return object, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_METADATA_INVALID")
	}
	raw, e := json.Marshal(object)
	if e != nil || string(raw) != p.PreviousControlJSON {
		return object, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_METADATA_INVALID")
	}
	return object, nil
}
