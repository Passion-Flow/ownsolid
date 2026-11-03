package scope

import (
	"context"
	"os"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type currentStoresEngine interface {
	controlReplacementEngine
	restoreDatabaseEngine
}
type RestoredCurrentStoresResult struct {
	ScopeID        string `json:"scope_id"`
	GenerationID   string `json:"generation_id,omitempty"`
	Status         string `json:"status"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	Pending        bool   `json:"pending"`
	ProofSHA256    string `json:"proof_sha256,omitempty"`
	Qualification  string `json:"qualification"`
}

func (l *locked) currentStoresResult() RestoredCurrentStoresResult {
	s := currentStoresState(l.events)
	r := RestoredCurrentStoresResult{ScopeID: l.marker.ScopeID, Status: "no_current_stores_checks_recorded", TotalSteps: len(currentStoresSteps()), Qualification: "historical_selected_store_checks_only_not_current_readiness_activation_browser_or_target"}
	if wire.IsCumulativeCPUProfile(l.marker.Profile) {
		r.TotalSteps = len(currentStoresSteps(4))
	}
	if s.record == nil {
		return r
	}
	r.GenerationID = s.record.GenerationID
	r.TotalSteps = len(currentStoresSteps(s.record.StoreCount))
	r.CompletedSteps = len(s.completed)
	r.Pending = true
	r.Status = "current_stores_checks_incomplete"
	if s.pending != "" {
		r.Status = "current_stores_checks_reconciliation_required"
	}
	if s.failed {
		r.Status = "current_stores_checks_failed_cleanup_required"
	}
	if s.aborting {
		r.Status = "current_stores_checks_cleanup_pending"
	}
	if s.aborted {
		r.Status = "current_stores_checks_aborted_target_preserved"
		r.Pending = false
	}
	if proof, ok := currentStoresProof(l.events); ok {
		r.Status = "current_stores_checks_completed_activation_pending"
		r.Pending = false
		r.ProofSHA256 = proof
	}
	return r
}
func CurrentStoresCommand(mode string) bool {
	return mode == "qualify-restored-current-stores" || mode == "requalify-restored-current-stores" || mode == "reconcile-restored-current-stores" || mode == "abort-restored-current-stores"
}
func RunRestoredCurrentStores(ctx context.Context, c configuration.Config, mode string) (RestoredCurrentStoresResult, error) {
	if !CurrentStoresCommand(mode) || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoredCurrentStoresResult{}, failure("OFFLINE_SCOPE_CURRENT_STORES_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoredCurrentStoresResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoredCurrentStoresResult{}, e
	}
	return runRestoredCurrentStores(ctx, c, mode, client, worker)
}
func runRestoredCurrentStores(ctx context.Context, c configuration.Config, mode string, client currentStoresEngine, worker runtimeOperator) (out RestoredCurrentStoresResult, err error) {
	if !CurrentStoresCommand(mode) {
		return out, failure("OFFLINE_SCOPE_CURRENT_STORES_COMMAND_INVALID")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	result := func() RestoredCurrentStoresResult { return l.currentStoresResult() }
	if ctx.Err() != nil {
		return result(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	s := currentStoresState(l.events)
	if s.record != nil && (s.complete || s.aborted) && mode != "requalify-restored-current-stores" {
		if mode == "abort-restored-current-stores" && !s.aborted {
			return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_ALREADY_COMPLETED")
		}
		return result(), nil
	}
	if mode == "abort-restored-current-stores" {
		if s.record == nil {
			return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_NOT_RECORDED")
		}
		p, e := l.readCurrentStoresPlan(*s.record)
		if e != nil {
			if s.record.Step != 0 || len(s.completed) != 0 {
				return result(), e
			}
			p = RestoredCurrentStoresPlan{ScopeID: l.marker.ScopeID, GenerationID: s.record.GenerationID}
		}
		return l.abortCurrentStores(ctx, client, p)
	}
	newGeneration := s.record == nil || mode == "requalify-restored-current-stores"
	if mode == "requalify-restored-current-stores" && (s.record == nil || !s.complete && !s.aborted) {
		return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_TERMINAL_GENERATION_REQUIRED")
	}
	if newGeneration && mode == "reconcile-restored-current-stores" {
		return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_NOT_RECORDED")
	}
	if !newGeneration && (s.failed || s.aborting) {
		return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_REQUIRED")
	}
	if !newGeneration && s.pending != "" && mode != "reconcile-restored-current-stores" {
		return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_RECONCILIATION_REQUIRED")
	}
	count := 0
	if wire.IsCumulativeCPUProfile(l.marker.Profile) {
		count = 4
	}
	if s.record != nil && !newGeneration {
		count = s.record.StoreCount
	}
	if e = admitProcessJournal(l, (len(currentStoresSteps(count))+1)*3+6); e != nil {
		return result(), e
	}
	var plan RestoredCurrentStoresPlan
	if newGeneration {
		generation, e := nonce()
		if e != nil {
			return result(), e
		}
		plan, _, e = l.makeRestoredCurrentStoresPlan(ctx, client, worker, generation)
		if e != nil {
			return result(), e
		}
		r := currentStoresPlanRecord(plan)
		id, e := nonce()
		if e != nil {
			return result(), e
		}
		if e = l.appendEvent(Event{OperationID: id, Action: "current_stores_effect", Outcome: "started", RestoredCurrentStores: &r}); e != nil {
			return result(), e
		}
	} else {
		plan, e = l.readCurrentStoresPlan(*s.record)
		if e != nil {
			if len(s.completed) != 0 {
				return result(), e
			}
			if _, stat := l.root.Lstat(currentStoresPlanFile(s.record.GenerationID)); !os.IsNotExist(stat) {
				return result(), e
			}
			view := l.currentStoresAdmissionView()
			plan, _, e = view.makeRestoredCurrentStoresPlan(ctx, client, worker, s.record.GenerationID)
			if e != nil || !currentStoresIdentity(currentStoresPlanRecord(plan), *s.record) {
				return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_PUBLICATION_RECONSTRUCTION_FAILED")
			}
		}
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		s := currentStoresState(l.events)
		if s.record == nil || s.complete || s.aborted {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, e := l.abortCurrentStores(cleanup, client, plan); e != nil {
			err = failure("OFFLINE_SCOPE_CURRENT_STORES_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = result()
	}()
	for index, step := range currentStoresSteps(currentStoresPlanRecord(plan).StoreCount) {
		s = currentStoresState(l.events)
		if _, done := s.completed[index]; done {
			continue
		}
		filtered, e := l.currentStoresRuntimeView(ctx, client, plan)
		if e != nil {
			return result(), e
		}
		view := l.currentStoresAdmissionView()
		current, files, e := view.makeRestoredCurrentStoresPlan(ctx, filtered, worker, plan.GenerationID)
		if e != nil {
			return result(), e
		}
		if !reflect.DeepEqual(current, plan) {
			return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_CURRENT_GENERATION_CHANGED")
		}
		if e = client.ValidateConfigurationFiles(files, plan.Tasks[0].UID, plan.Tasks[0].GID); e != nil {
			return result(), e
		}
		r := currentStoresPlanRecord(plan)
		r.Step = index
		id, e := nonce()
		if e != nil {
			return result(), e
		}
		action, reference := "current_stores_effect", ""
		reconcile := s.pending != ""
		if reconcile {
			if s.record.Step != index || !newGeneration && mode != "reconcile-restored-current-stores" {
				return result(), failure("OFFLINE_SCOPE_CURRENT_STORES_RECONCILIATION_REQUIRED")
			}
			action, reference = "current_stores_reconcile", s.pending
			// The first publication intent belongs to this invocation and has no
			// SDK effect. Preserve its own result rather than inventing a reconcile.
			if newGeneration && index == 0 {
				action, reference, id, reconcile = "current_stores_effect", "", s.pending, false
			}
		} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", RestoredCurrentStores: &r}); e != nil {
			return result(), e
		}
		object, e := l.executeCurrentStoresStep(ctx, client, plan, step, files, reconcile)
		if _, binding := l.checkEngine(ctx, client); binding != nil {
			e = binding
		}
		outcome, code := "completed", ""
		if e != nil {
			object = ""
			outcome = "uncertain"
			if reconcile {
				outcome = "unresolved"
			}
			code = "OFFLINE_SCOPE_CURRENT_STORES_EFFECT_UNRESOLVED"
			if e.Error() == restoreDatabaseTaskFailed {
				outcome, code = "failed", restoreDatabaseTaskFailed
			}
		}
		r.ObjectID = object
		if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, RestoredCurrentStores: &r}); je != nil {
			return result(), je
		}
		if e != nil {
			return result(), e
		}
	}
	return result(), nil
}

func (l *locked) currentStoresAdmissionView() *locked {
	view := *l
	view.events = []Event{}
	for _, e := range l.events {
		if e.RestoredCurrentStores == nil {
			view.events = append(view.events, e)
		}
	}
	return &view
}

type currentStoresRuntimeEngine struct {
	currentStoresEngine
	view runtimeEngine
}

func (v currentStoresRuntimeEngine) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	return v.view.RuntimeCandidates(ctx, scope, networks)
}
func (v currentStoresRuntimeEngine) InspectRuntimeNetwork(ctx context.Context, scope string) (engine.RuntimeNetwork, bool, error) {
	return v.view.InspectRuntimeNetwork(ctx, scope)
}
func (v currentStoresRuntimeEngine) VerifyVolumeConsumers(ctx context.Context, owner engine.Ownership, allowed []string) error {
	return v.view.VerifyVolumeConsumers(ctx, owner, allowed)
}
func (l *locked) currentStoresRuntimeView(ctx context.Context, c currentStoresEngine, p RestoredCurrentStoresPlan) (currentStoresEngine, error) {
	var view runtimeEngine = c
	s := currentStoresState(l.events)
	if s.record != nil && s.record.GenerationID == p.GenerationID {
		seen := func(phase, kind string) bool {
			for _, e := range l.events {
				if r := e.RestoredCurrentStores; r != nil && r.GenerationID == p.GenerationID && e.Action == "current_stores_effect" && e.Outcome == "started" && currentStoresSteps(r.StoreCount)[r.Step] == (restoreDatabaseStep{phase, kind}) {
					return true
				}
			}
			return false
		}
		done := func(phase, kind string) bool {
			for i := range s.completed {
				if currentStoresSteps(s.record.StoreCount)[i] == (restoreDatabaseStep{phase, kind}) {
					return true
				}
			}
			return false
		}
		t := p.Tasks[0]
		if seen("utility-create", "") && !done("utility-remove", "") {
			u, found, e := c.InspectUtility(ctx, t.VolumeOwner, p.Initializer, t.UID, t.GID)
			if e != nil {
				return nil, e
			}
			if found {
				view = restoredInputRuntimeView{runtimeEngine: view, scopeID: p.ScopeID, utilityID: u.ID}
			}
		}
		for _, task := range p.Tasks {
			if seen("task-create", task.Kind) && !done("task-remove", task.Kind) {
				o, found, e := c.InspectDatabaseTask(ctx, task)
				if e != nil {
					return nil, e
				}
				if found {
					view = databaseTaskRuntimeView{runtimeEngine: view, task: task, object: o}
				}
			}
		}
	}
	return currentStoresRuntimeEngine{currentStoresEngine: c, view: view}, nil
}
