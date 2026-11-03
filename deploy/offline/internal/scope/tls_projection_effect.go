package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type tlsProjectionEngine interface {
	storageEngine
	runtimeEngine
	ValidateConfigurationFiles([]engine.ConfigurationFile, int, int) error
	RemoveTLSGenerationVolume(context.Context, engine.Ownership) error
}

var _ tlsProjectionEngine = (*engine.Client)(nil)

func tlsProjectionIntentSeen(events []Event, generation, phase string) bool {
	for _, e := range events {
		if e.TLSProjection != nil && e.TLSProjection.GenerationID == generation && e.Action == "tls_projection_effect" && e.TLSProjection.Phase == phase {
			return true
		}
	}
	return false
}
func tlsProjectionStepCompleted(events []Event, phase string) bool {
	for _, r := range tlsProjectionState(events).completed {
		if r.Phase == phase {
			return true
		}
	}
	return false
}
func (l *locked) tlsProjectionRuntimeView(ctx context.Context, c tlsProjectionEngine, p RestoredTLSPlan) (runtimeEngine, error) {
	var view runtimeEngine = c
	if tlsProjectionIntentSeen(l.events, p.GenerationID, "utility-create") && !tlsProjectionStepCompleted(l.events, "utility-remove") {
		u, found, e := c.InspectUtility(ctx, p.ConfigurationOwner, p.Initializer, p.UID, p.GID)
		if e != nil {
			return nil, e
		}
		if found {
			view = restoredInputRuntimeView{runtimeEngine: c, scopeID: l.marker.ScopeID, utilityID: u.ID}
		}
	}
	return view, nil
}
func executeTLSProjectionStep(ctx context.Context, c tlsProjectionEngine, p RestoredTLSPlan, r TLSProjectionRecord, files []engine.ConfigurationFile, reconcile bool) (TLSProjectionRecord, error) {
	if !validTLSProjectionOwner(p) || r.GenerationID != p.GenerationID {
		return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_EFFECT_CONTEXT_INVALID")
	}
	if ctx.Err() != nil {
		return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	owner := p.ConfigurationOwner
	switch r.Phase {
	case "volume-create":
		if !reconcile {
			if _, e := c.CreateVolume(ctx, owner); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
			return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_VOLUME_UNRESOLVED")
		}
	case "utility-create":
		if !reconcile {
			if _, e := c.CreateUtility(ctx, owner, p.Initializer, p.UID, p.GID); e != nil {
				return r, e
			}
		}
		u, found, e := c.InspectUtility(ctx, owner, p.Initializer, p.UID, p.GID)
		if e != nil || !found {
			return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_UTILITY_UNRESOLVED")
		}
		r.ObjectID = u.ID
	case "payload-write":
		if !reconcile {
			if e := c.PutConfiguration(ctx, owner, p.Initializer, p.UID, p.GID, files); e != nil {
				return r, e
			}
		}
		observed, e := c.InspectConfiguration(ctx, owner, p.Initializer, p.UID, p.GID, files)
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_PAYLOAD_UNRESOLVED")
		}
	case "utility-remove":
		if !reconcile {
			if e := c.RemoveUtility(ctx, owner, p.Initializer, p.UID, p.GID); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, p.UID, p.GID); e != nil || found {
			return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_UTILITY_REMOVAL_UNRESOLVED")
		}
	case "complete":
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
			return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_VOLUME_UNRESOLVED")
		}
		if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, p.UID, p.GID); e != nil || found {
			return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_UTILITY_REMOVAL_UNRESOLVED")
		}
		if e := c.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return r, e
		}
	default:
		return r, failure("OFFLINE_SCOPE_TLS_PROJECTION_PHASE_INVALID")
	}
	return r, nil
}

// Caller owns a durable abort intent and one independent cleanup deadline.
// Only admitted generated actors are inspected or removed; source files and
// every old/runtime/data volume are preserved, including after a lost response.
func (l *locked) removeTLSProjectionActors(ctx context.Context, c tlsProjectionEngine, p RestoredTLSPlan) error {
	if !validTLSProjectionOwner(p) {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_EFFECT_CONTEXT_INVALID")
	}
	if !tlsProjectionIntentSeen(l.events, p.GenerationID, "volume-create") {
		return nil
	}
	if _, found, e := c.InspectVolume(ctx, p.ConfigurationOwner); e != nil {
		return e
	} else if !found {
		return nil
	}
	if _, found, e := c.InspectUtility(ctx, p.ConfigurationOwner, p.Initializer, p.UID, p.GID); e != nil {
		return e
	} else if found {
		if e = c.RemoveUtility(ctx, p.ConfigurationOwner, p.Initializer, p.UID, p.GID); e != nil {
			return e
		}
	}
	if e := c.RemoveTLSGenerationVolume(ctx, p.ConfigurationOwner); e != nil {
		return e
	}
	if _, found, e := c.InspectVolume(ctx, p.ConfigurationOwner); e != nil || found {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_CLEANUP_VOLUME_UNRESOLVED")
	}
	return nil
}

func validTLSProjectionOwner(p RestoredTLSPlan) bool {
	return identityPattern.MatchString(p.ScopeID) && identityPattern.MatchString(p.GenerationID) && p.ConfigurationOwner == (engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: engine.TLSConfigurationRole + "-" + p.GenerationID}) && p.UID > 0 && p.GID > 0 && immutableDigest(p.Initializer)
}

// The scope's normal process controls remain available while this exact
// never-started generated utility exists. Unknown actors remain visible.
type tlsProjectionProcessView struct {
	processEngine
	view runtimeEngine
}

func (v tlsProjectionProcessView) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	return v.view.RuntimeCandidates(ctx, scope, networks)
}
func (l *locked) tlsProjectionProcessClient(ctx context.Context, c processEngine) (processEngine, error) {
	s := tlsProjectionState(l.events)
	if s.record == nil || s.aborted || !tlsProjectionIntentSeen(l.events, s.record.GenerationID, "utility-create") || tlsProjectionStepCompleted(l.events, "utility-remove") {
		return c, nil
	}
	sdk, ok := c.(tlsProjectionEngine)
	if !ok {
		return nil, failure("OFFLINE_SCOPE_TLS_PROJECTION_GENERATED_INSPECTION_REQUIRED")
	}
	p, e := l.readTLSProjectionPlan(*s.record)
	if e != nil {
		return nil, e
	}
	view, e := l.tlsProjectionRuntimeView(ctx, sdk, p)
	if e != nil {
		return nil, e
	}
	return tlsProjectionProcessView{processEngine: c, view: view}, nil
}
