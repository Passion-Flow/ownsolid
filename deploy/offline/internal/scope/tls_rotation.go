package scope

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Plans are immutable facts at their original admission prefix. Later TLS
// generations must not reinterpret earlier container identities or authority.
func generationPrefix(events []Event, generation string, replacement bool) []Event {
	for i, event := range events {
		if !replacement && event.TLSProjection != nil && event.TLSProjection.GenerationID == generation || replacement && event.ControlReplacement != nil && event.ControlReplacement.GenerationID == generation {
			return events[:i]
		}
	}
	return events
}

func lastCompletedReplacement(events []Event) *ControlReplacementRecord {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.ControlReplacement != nil && e.ControlReplacement.Phase == "complete" && e.Outcome == "completed" {
			r := *e.ControlReplacement
			return &r
		}
	}
	return nil
}

func (l *locked) previousTLSConfiguration(ctx context.Context, generation string) (string, engine.Ownership, string, error) {
	control, owner, payload, _, e := l.previousTLSConfigurationWithPlan(ctx, generation)
	return control, owner, payload, e
}
func (l *locked) previousTLSConfigurationWithPlan(ctx context.Context, generation string) (string, engine.Ownership, string, *RestoredTLSPlan, error) {
	view := *l
	view.events = generationPrefix(l.events, generation, false)
	runtime := effectiveRuntimeState(view.events)
	role, found := volumeState(view.events).roles["control-config"]
	if runtime.record == nil || !found {
		return "", engine.Ownership{}, "", nil, failure("OFFLINE_SCOPE_TLS_ROTATION_CONTEXT_REQUIRED")
	}
	control := runtime.record.Services["control"].ID
	if prior := lastCompletedReplacement(view.events); prior != nil {
		var recorded *TLSProjectionRecord
		for _, event := range view.events {
			if event.TLSProjection != nil && event.TLSProjection.GenerationID == prior.GenerationID {
				r := *event.TLSProjection
				recorded = &r
			}
		}
		if recorded == nil {
			return "", engine.Ownership{}, "", nil, failure("OFFLINE_SCOPE_TLS_ROTATION_COMPLETED_REPLACEMENT_REQUIRED")
		}
		plan, e := view.readTLSProjectionPlan(*recorded)
		return control, plan.ConfigurationOwner, plan.PayloadSHA256, &plan, e
	}
	p, _, e := view.replacementModels(ctx)
	return control, view.volumeOwner(role), restoreConfigurationDigest(p.files["control-config"]), nil, e
}

func rotationReady(events []Event) bool {
	tls := tlsProjectionState(events)
	replacement := controlReplacementState(events)
	return tls.complete && !tls.aborted && tls.pending == "" && replacement.complete && replacement.pending == "" && replacement.record != nil && tls.record != nil && replacement.record.GenerationID == tls.record.GenerationID && processState(events).pending == "" && rotationChecksQuiescent(events)
}

func rotationChecksQuiescent(events []Event) bool {
	db, stores := liveDatabaseState(events), currentStoresState(events)
	return db.pending == "" && stores.pending == "" && (db.record == nil || db.complete || db.aborted) && (stores.record == nil || stores.complete || stores.aborted)
}

func (l *locked) cleanRotationControl(object engine.RuntimeContainer) bool {
	started, e := time.Parse(time.RFC3339Nano, object.State.StartedAt)
	if e != nil || object.State.ExitCode != 0 || object.State.Running || object.State.PID != 0 || object.State.OOMKilled || object.State.Paused || object.State.Restarting || object.State.Dead {
		return false
	}
	if object.State.Status == "created" {
		return started.IsZero()
	}
	return object.State.Status == "exited" && !started.IsZero() && knownActivatedControl(l.events, ProcessSnapshot{ID: object.ID, State: object.State.Status, ExitCode: object.State.ExitCode, StartedAt: object.State.StartedAt})
}

func (l *locked) verifyRotationProcess(ctx context.Context, c runtimeEngine) error {
	if lastCompletedReplacement(l.events) == nil {
		return nil
	}
	object, found, e := c.InspectRuntime(ctx, l.marker.ScopeID, "control")
	if e != nil || !found || !l.cleanRotationControl(object) {
		return failure("OFFLINE_SCOPE_TLS_ROTATION_KNOWN_STOPPED_CONTROL_REQUIRED")
	}
	return nil
}
