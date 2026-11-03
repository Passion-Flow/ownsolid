package scope

import (
	"context"
	"os"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type controlReplacementEngine interface {
	tlsProjectionEngine
	RemoveStoppedControl(context.Context, string, engine.RuntimeContainer) error
	InspectStoppedControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
}
type controlReplacementOperator interface {
	runtimeOperator
	ObserveControlReplacement(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus, bool) (compose.RuntimeStatus, error)
	CreateReplacementControl(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error)
}

var _ controlReplacementEngine = (*engine.Client)(nil)
var _ controlReplacementOperator = (*compose.Client)(nil)

type ControlReplacementResult struct {
	ScopeID        string `json:"scope_id"`
	GenerationID   string `json:"generation_id,omitempty"`
	Status         string `json:"status"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	Pending        bool   `json:"pending"`
	ProofSHA256    string `json:"proof_sha256,omitempty"`
	Qualification  string `json:"qualification"`
}

func (l *locked) controlReplacementResult() ControlReplacementResult {
	s := controlReplacementState(l.events)
	out := ControlReplacementResult{ScopeID: l.marker.ScopeID, Status: "no_control_replacement_recorded", TotalSteps: len(controlReplacementSteps()), Qualification: "stopped_control_configuration_replacement_only_not_activation_serving_browser_or_target"}
	if s.record == nil {
		return out
	}
	out.GenerationID = s.record.GenerationID
	out.CompletedSteps = len(s.completed)
	out.Pending = true
	out.Status = "control_replacement_incomplete"
	if s.pending != "" {
		out.Status = "control_replacement_requires_reconciliation"
	}
	if s.notApplied {
		out.Status = "control_replacement_confirmed_not_applied_recovery_required"
	}
	if proof, ok := controlReplacementProof(l.events); ok {
		out.ProofSHA256 = proof
		out.Pending = false
		out.Status = "control_configuration_replaced_activation_pending"
	}
	return out
}
func ControlReplacementCommand(mode string) bool {
	return mode == "replace-restored-control" || mode == "reconcile-restored-control" || mode == "recover-restored-control"
}
func ReplaceRestoredControl(ctx context.Context, c configuration.Config, reconcile bool) (ControlReplacementResult, error) {
	return runControlReplacementCommand(ctx, c, reconcile, false)
}
func RecoverRestoredControl(ctx context.Context, c configuration.Config) (ControlReplacementResult, error) {
	return runControlReplacementCommand(ctx, c, false, true)
}
func runControlReplacementCommand(ctx context.Context, c configuration.Config, reconcile, recoverMissing bool) (ControlReplacementResult, error) {
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil {
		return ControlReplacementResult{}, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ControlReplacementResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return ControlReplacementResult{}, e
	}
	return runControlReplacement(ctx, c, client, worker, reconcile, recoverMissing)
}
func replaceRestoredControl(ctx context.Context, c configuration.Config, client controlReplacementEngine, worker controlReplacementOperator, reconcile bool) (ControlReplacementResult, error) {
	return runControlReplacement(ctx, c, client, worker, reconcile, false)
}
func runControlReplacement(ctx context.Context, c configuration.Config, client controlReplacementEngine, worker controlReplacementOperator, reconcile, recoverMissing bool) (ControlReplacementResult, error) {
	l, e := open(c, true)
	if e != nil {
		return ControlReplacementResult{}, e
	}
	defer l.close()
	result := func() ControlReplacementResult { return l.controlReplacementResult() }
	if ctx.Err() != nil {
		return result(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	s := controlReplacementState(l.events)
	currentTLS := tlsProjectionState(l.events)
	if s.complete && s.record != nil && currentTLS.complete && currentTLS.record != nil && currentTLS.record.GenerationID != s.record.GenerationID && !reconcile && !recoverMissing {
		s = controlReplacementRecords{completed: map[int]ControlReplacementRecord{}}
	}
	if s.complete {
		_, e = l.readControlReplacementPlan()
		return result(), e
	}
	if s.notApplied && !recoverMissing {
		return result(), failure("OFFLINE_SCOPE_EXPLICIT_CONTROL_REPLACEMENT_RECOVERY_REQUIRED")
	}
	if s.pending != "" && !reconcile && !recoverMissing {
		return result(), failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_RECONCILIATION_REQUIRED")
	}
	if s.record == nil && (reconcile || recoverMissing) {
		return result(), failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_NOT_RECORDED")
	}
	var plan ControlReplacementPlan
	if s.record == nil {
		plan, e = l.makeControlReplacementPlan(ctx, client, worker)
		if e != nil {
			return result(), e
		}
		if e = l.publishControlReplacementPlan(ctx, plan, false, func() error {
			return writeNew(l.root, controlReplacementPlanName(l.events, plan.GenerationID), encode(plan))
		}); e != nil {
			return result(), e
		}
	} else if _, published := s.completed[0]; !published {
		plan, e = l.readControlReplacementPlan()
		if e != nil {
			if _, exists := l.root.Lstat(controlReplacementPlanName(l.events, s.record.GenerationID)); !os.IsNotExist(exists) {
				return result(), e
			}
			// Only absent first publication can be reconstructed. No SDK effect has
			// been admitted and the exact recorded digest is still required.
			plan, e = l.makeControlReplacementPlanBeforePublication(ctx, client, worker)
			if e != nil {
				return result(), e
			}
		}
		if e = l.publishControlReplacementPlan(ctx, plan, true, func() error {
			return writeNew(l.root, controlReplacementPlanName(l.events, plan.GenerationID), encode(plan))
		}); e != nil {
			return result(), e
		}
	} else {
		plan, e = l.readControlReplacementPlan()
		if e != nil {
			return result(), e
		}
	}
	if e = admitProcessJournal(l, (len(controlReplacementSteps())-len(controlReplacementState(l.events).completed))*3+3); e != nil {
		return result(), e
	}
	base := controlReplacementPlanRecord(plan)
	for index, phase := range controlReplacementSteps() {
		s = controlReplacementState(l.events)
		if _, done := s.completed[index]; done {
			continue
		}
		if index == 0 {
			return result(), failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_PLAN_REQUIRED")
		}
		if ctx.Err() != nil {
			return result(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		tls, files, e := l.currentControlReplacementFiles(ctx)
		if e != nil {
			return result(), e
		}
		if e = client.ValidateConfigurationFiles(files, tls.UID, tls.GID); e != nil {
			return result(), e
		}
		view, e := l.controlReplacementRuntimeView(ctx, client)
		if e != nil {
			return result(), e
		}
		verified, e := l.verifyRuntimeContextForReplacement(ctx, view, worker, false, &plan)
		if e != nil {
			return result(), e
		}
		r := base
		r.Step = index
		r.Phase = phase
		id, e := nonce()
		if e != nil {
			return result(), e
		}
		action, reference := "control_replacement_effect", ""
		if s.notApplied {
			action = "control_replacement_retry_effect"
			reference = s.recoveryReference
		}
		observeOnly := s.pending != ""
		if observeOnly && recoverMissing {
			if e = l.confirmControlReplacementNotApplied(ctx, client, plan, tls, files, *s.record); e != nil {
				return result(), e
			}
			if _, e = l.checkEngine(ctx, client); e != nil {
				return result(), e
			}
			if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: s.pending, Action: "control_replacement_recovery", Outcome: "not_applied", ControlReplacement: &r}); e != nil {
				return result(), e
			}
			return result(), nil
		}
		if observeOnly {
			if !reconcile || s.record.Step != index {
				return result(), failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_RECONCILIATION_REQUIRED")
			}
			action = "control_replacement_reconcile"
			reference = s.pending
		} else {
			if phase == "inspector-create" || phase == "control-remove" || phase == "control-create" {
				if e = l.admitSelectedTLSCurrent(ctx, tls, time.Now()); e != nil {
					return result(), e
				}
			}
			if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: "started", ControlReplacement: &r}); e != nil {
				return result(), e
			}
		}
		observed, e := l.executeControlReplacementStep(ctx, client, worker, plan, tls, files, verified, r, observeOnly)
		if _, binding := l.checkEngine(ctx, client); binding != nil {
			e = binding
		}
		outcome, code := "completed", ""
		if e != nil {
			observed = r
			outcome = "uncertain"
			if observeOnly {
				outcome = "unresolved"
			}
			code = "OFFLINE_SCOPE_CONTROL_REPLACEMENT_EFFECT_UNRESOLVED"
		}
		if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, ControlReplacement: &observed}); je != nil {
			return result(), je
		}
		if e != nil {
			return result(), e
		}
	}
	return result(), nil
}

func (l *locked) makeControlReplacementPlanBeforePublication(ctx context.Context, c tlsProjectionEngine, w runtimeOperator) (ControlReplacementPlan, error) {
	// Preserve the journal prefix while verifying the pre-effect runtime. The
	// temporary read-only view never writes or erases historical events.
	view := *l
	view.events = generationPrefix(l.events, controlReplacementState(l.events).record.GenerationID, true)
	return view.makeControlReplacementPlan(ctx, c, w)
}
func (l *locked) executeControlReplacementStep(ctx context.Context, c controlReplacementEngine, w controlReplacementOperator, p ControlReplacementPlan, tls RestoredTLSPlan, files []engine.ConfigurationFile, v verifiedRuntimeContext, r ControlReplacementRecord, reconcile bool) (ControlReplacementRecord, error) {
	bad := func() (ControlReplacementRecord, error) {
		return r, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_EFFECT_UNRESOLVED")
	}
	owner := tls.ConfigurationOwner
	switch r.Phase {
	case "inspector-create":
		if !reconcile {
			if _, e := c.CreateUtility(ctx, owner, tls.Initializer, tls.UID, tls.GID); e != nil {
				return r, e
			}
		}
		u, found, e := c.InspectUtility(ctx, owner, tls.Initializer, tls.UID, tls.GID)
		if e != nil || !found {
			return bad()
		}
		r.ObjectID = u.ID
	case "payload-verify":
		observed, e := c.InspectConfiguration(ctx, owner, tls.Initializer, tls.UID, tls.GID, files)
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return bad()
		}
	case "inspector-remove":
		if !reconcile {
			if e := c.RemoveUtility(ctx, owner, tls.Initializer, tls.UID, tls.GID); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectUtility(ctx, owner, tls.Initializer, tls.UID, tls.GID); e != nil || found {
			return bad()
		}
		if e := c.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return r, e
		}
	case "control-remove":
		previous, e := p.previousControl()
		if e != nil {
			return r, e
		}
		if !reconcile {
			if e = c.RemoveStoppedControl(ctx, p.ScopeID, previous); e != nil {
				return r, e
			}
		}
		_, model, e := l.replacementModels(ctx)
		if e != nil {
			return r, e
		}
		model, e = compose.ControlConfigurationModel(model, p.GenerationID)
		if e != nil {
			return r, e
		}
		if _, e = w.ObserveControlReplacement(ctx, c, v.scope, model, p.Before, false); e != nil {
			return r, e
		}
	case "control-create":
		_, model, e := l.replacementModels(ctx)
		if e != nil {
			return r, e
		}
		model, e = compose.ControlConfigurationModel(model, p.GenerationID)
		if e != nil {
			return r, e
		}
		var observed compose.RuntimeStatus
		if reconcile {
			observed, e = w.ObserveControlReplacement(ctx, c, v.scope, model, p.Before, true)
		} else {
			observed, e = w.CreateReplacementControl(ctx, c, v.scope, model, p.Before)
		}
		if e != nil {
			return r, e
		}
		r.ObjectID = observed.Services["control"].ID
	case "control-verify", "complete":
		id := controlReplacementState(l.events).completed[controlReplacementCreateStep].ObjectID
		target := ""
		for _, mount := range v.model.Services["control"].Mounts {
			if mount.Source == owner.Role {
				target = mount.Target
			}
		}
		observed, e := c.InspectStoppedControlConfiguration(ctx, owner, id, target, tls.UID, tls.GID, files)
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return bad()
		}
		if r.Phase == "control-verify" {
			r.ObjectID = id
		} else {
			next := replacementEffectiveRecord(*runtimeState(l.events).record, p.ModelSHA256, id)
			r.Effective = &next
		}
	default:
		return bad()
	}
	return r, nil
}
