package scope

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func controlReplacementIntended(events []Event, phase string) bool {
	current := controlReplacementState(events)
	if current.record == nil {
		return false
	}
	for _, e := range events {
		if e.ControlReplacement != nil && e.ControlReplacement.GenerationID == current.record.GenerationID && (e.Action == "control_replacement_effect" || e.Action == "control_replacement_retry_effect") && e.Outcome == "started" && e.ControlReplacement.Phase == phase {
			return true
		}
	}
	return false
}
func controlReplacementCompleted(events []Event, phase string) bool {
	for _, r := range controlReplacementState(events).completed {
		if r.Phase == phase {
			return true
		}
	}
	return false
}
func (l *locked) controlReplacementRuntimeView(ctx context.Context, c tlsProjectionEngine) (runtimeEngine, error) {
	var view runtimeEngine = c
	if controlReplacementIntended(l.events, "inspector-create") && !controlReplacementCompleted(l.events, "inspector-remove") {
		tls, e := l.readTLSProjectionPlan(*tlsProjectionState(l.events).record)
		if e != nil {
			return nil, e
		}
		u, found, e := c.InspectUtility(ctx, tls.ConfigurationOwner, tls.Initializer, tls.UID, tls.GID)
		if e != nil {
			return nil, e
		}
		if found {
			s := controlReplacementState(l.events)
			if recorded, ok := s.completed[1]; ok && recorded.ObjectID != u.ID {
				return nil, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_INSPECTOR_CHANGED")
			}
			view = restoredInputRuntimeView{runtimeEngine: c, scopeID: l.marker.ScopeID, utilityID: u.ID}
		}
	}
	return view, nil
}
func (l *locked) currentControlReplacementFiles(ctx context.Context) (RestoredTLSPlan, []engine.ConfigurationFile, error) {
	var tls RestoredTLSPlan
	s := tlsProjectionState(l.events)
	if s.record == nil {
		return tls, nil, failure("OFFLINE_SCOPE_COMPLETED_TLS_PREPARATION_REQUIRED")
	}
	tls, e := l.readTLSProjectionPlan(*s.record)
	if e != nil {
		return tls, nil, e
	}
	if tls.FormatVersion == 2 {
		files, _, e := l.readGenerationPayload(ctx, tls)
		return tls, files, e
	}
	input, model, e := l.replacementModels(ctx)
	if e != nil {
		return tls, nil, e
	}
	certificate, e := protectedInput(l.config, tls.Request.CertificateFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return tls, nil, e
	}
	key, e := protectedInput(l.config, tls.Request.PrivateKeyFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return tls, nil, e
	}
	trust, e := protectedInput(l.config, tls.Request.TrustBundleFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return tls, nil, e
	}
	files, identity, certName, keyName, e := projectRestoredTLS(input, model, certificate, key, trust, time.Now())
	if e != nil {
		return tls, nil, e
	}
	if !reflect.DeepEqual(identity, tls.Identity) || certName != tls.CertificateName || keyName != tls.PrivateKeyName || restoreConfigurationDigest(files) != tls.PayloadSHA256 {
		return tls, nil, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_TLS_INPUT_CHANGED")
	}
	return tls, files, nil
}
func (l *locked) makeControlReplacementPlan(ctx context.Context, c tlsProjectionEngine, w runtimeOperator) (ControlReplacementPlan, error) {
	var p ControlReplacementPlan
	proof, ok := tlsProjectionProof(l.events)
	if !ok {
		return p, failure("OFFLINE_SCOPE_COMPLETED_TLS_PREPARATION_REQUIRED")
	}
	verified, e := l.verifyRuntimeContext(ctx, c, w, false)
	if e != nil {
		return p, e
	}
	tls, _, e := l.currentControlReplacementFiles(ctx)
	if e != nil {
		return p, e
	}
	if e = l.admitSelectedTLSCurrent(ctx, tls, time.Now()); e != nil {
		return p, e
	}
	if e = c.VerifyVolumeConsumers(ctx, tls.ConfigurationOwner, nil); e != nil {
		return p, e
	}
	if _, found, e := c.InspectUtility(ctx, tls.ConfigurationOwner, tls.Initializer, tls.UID, tls.GID); e != nil || found {
		return p, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_UNUSED_CONFIGURATION_REQUIRED")
	}
	previous, found, e := c.InspectRuntime(ctx, l.marker.ScopeID, "control")
	if e != nil || !found {
		return p, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_PREVIOUS_CONTROL_REQUIRED")
	}
	metadata, e := engine.RuntimeContainerDigest(previous)
	if e != nil {
		return p, e
	}
	_, baseline, e := l.replacementModels(ctx)
	if e != nil {
		return p, e
	}
	next, e := compose.ControlConfigurationModel(baseline, tls.GenerationID)
	if e != nil {
		return p, e
	}
	raw, e := json.Marshal(previous)
	if e != nil {
		return p, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_METADATA_INVALID")
	}
	before := verified.observed
	before.Services = make(map[string]compose.RuntimeServiceStatus, len(verified.observed.Services))
	for name, service := range verified.observed.Services {
		before.Services[name] = service
	}
	p = ControlReplacementPlan{FormatVersion: 1, ScopeID: l.marker.ScopeID, GenerationID: tls.GenerationID, TLSProofSHA256: proof, TLSPlanSHA256: tlsProjectionState(l.events).record.PlanSHA256, RuntimeSHA256: wire.Digest(encode(*runtimeState(l.events).record)), ModelSHA256: wire.Digest(encode(next)), PreviousMetadataSHA256: metadata, PreviousControlJSON: string(raw), Before: before}
	if e = l.validateControlReplacementPlan(ctx, p); e != nil {
		return p, e
	}
	return p, nil
}

// Partial replacement observation preserves absence; no placeholder control is
// inserted. Every peer must retain its exact pre-replacement stopped state.
func (l *locked) validateReplacementObservation(p ControlReplacementPlan, actual compose.RuntimeStatus) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_RUNTIME_CHANGED") }
	if actual.NetworkID != p.Before.NetworkID || actual.IngressNetworkID != p.Before.IngressNetworkID {
		return bad()
	}
	for name, service := range p.Before.Services {
		if name != "control" {
			if current, ok := actual.Services[name]; !ok || current != service {
				return bad()
			}
		}
	}
	current, present := actual.Services["control"]
	oldID := p.Before.Services["control"].ID
	if present && current.ID == oldID {
		if controlReplacementCompleted(l.events, "control-remove") || actual.Status != "observed_complete_compose_resources" || !reflect.DeepEqual(actual.Services, p.Before.Services) {
			return bad()
		}
		return nil
	}
	if !controlReplacementIntended(l.events, "control-remove") {
		return bad()
	}
	if !present {
		if actual.Status != "observed_partial_compose_resources" || len(actual.Services) != len(p.Before.Services)-1 || controlReplacementCompleted(l.events, "control-create") {
			return bad()
		}
		return nil
	}
	if !controlReplacementCompleted(l.events, "control-remove") || !controlReplacementIntended(l.events, "control-create") || actual.Status != "observed_complete_compose_resources" || len(actual.Services) != len(p.Before.Services) || current.State != "created" || current.ExitCode != 0 {
		return bad()
	}
	if recorded, ok := controlReplacementState(l.events).completed[controlReplacementCreateStep]; ok && current.ID != recorded.ObjectID {
		return bad()
	}
	return nil
}
