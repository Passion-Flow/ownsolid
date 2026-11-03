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

type liveDatabaseEngine interface {
	controlReplacementEngine
	restoreDatabaseEngine
}
type RestoredLiveDatabaseResult struct {
	ScopeID        string `json:"scope_id"`
	GenerationID   string `json:"generation_id,omitempty"`
	Status         string `json:"status"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	Pending        bool   `json:"pending"`
	ProofSHA256    string `json:"proof_sha256,omitempty"`
	Qualification  string `json:"qualification"`
}

func (l *locked) liveDatabaseResult() RestoredLiveDatabaseResult {
	s := liveDatabaseState(l.events)
	r := RestoredLiveDatabaseResult{ScopeID: l.marker.ScopeID, Status: "no_live_database_checks_recorded", TotalSteps: len(liveDatabaseSteps()), Qualification: "historical_schema_runtime_access_checks_only_not_current_readiness_stores_activation_browser_or_target"}
	if s.record == nil {
		return r
	}
	r.TotalSteps = len(liveDatabaseSteps(s.record.NativeRuntime))
	if s.record.NativeRuntime != nil {
		r.Qualification = "historical_native_runtime_preparation_and_access_checks_not_current_readiness_stores_activation_browser_or_target"
	}
	r.GenerationID = s.record.GenerationID
	r.CompletedSteps = len(s.completed)
	r.Pending = true
	r.Status = "live_database_checks_incomplete"
	if s.pending != "" {
		r.Status = "live_database_checks_reconciliation_required"
	}
	if s.failed {
		r.Status = "live_database_checks_failed_cleanup_required"
	}
	if s.aborting {
		r.Status = "live_database_checks_cleanup_pending"
	}
	if s.aborted {
		r.Status = "live_database_checks_aborted_target_preserved"
		r.Pending = false
	}
	if proof, ok := liveDatabaseProof(l.events); ok {
		r.Status = "live_database_checks_completed_activation_pending"
		r.Pending = false
		r.ProofSHA256 = proof
	}
	return r
}
func LiveDatabaseCommand(mode string) bool {
	return mode == "qualify-restored-live-database" || mode == "requalify-restored-live-database" || mode == "reconcile-restored-live-database" || mode == "abort-restored-live-database"
}
func RunRestoredLiveDatabase(ctx context.Context, c configuration.Config, mode string) (RestoredLiveDatabaseResult, error) {
	if !LiveDatabaseCommand(mode) || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoredLiveDatabaseResult{}, failure("OFFLINE_SCOPE_LIVE_DATABASE_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoredLiveDatabaseResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoredLiveDatabaseResult{}, e
	}
	return runRestoredLiveDatabase(ctx, c, mode, client, worker)
}
func runRestoredLiveDatabase(ctx context.Context, c configuration.Config, mode string, client liveDatabaseEngine, worker runtimeOperator) (out RestoredLiveDatabaseResult, err error) {
	if !LiveDatabaseCommand(mode) {
		return out, failure("OFFLINE_SCOPE_LIVE_DATABASE_COMMAND_INVALID")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	result := func() RestoredLiveDatabaseResult { return l.liveDatabaseResult() }
	if ctx.Err() != nil {
		return result(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	s := liveDatabaseState(l.events)
	if s.record != nil && (s.complete || s.aborted) && mode != "requalify-restored-live-database" {
		if mode == "abort-restored-live-database" && !s.aborted {
			return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_ALREADY_COMPLETED")
		}
		return result(), nil
	}
	if mode == "abort-restored-live-database" {
		if s.record == nil {
			return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_NOT_RECORDED")
		}
		p, e := l.readLiveDatabasePlan(*s.record)
		if e != nil {
			if s.record.Step != 0 || len(s.completed) != 0 {
				return result(), e
			}
			// The journal proves that publication never completed and no SDK
			// step began. Preserve any unusable file; abort only this metadata.
			p = RestoredLiveDatabasePlan{ScopeID: l.marker.ScopeID, GenerationID: s.record.GenerationID}
		}
		return l.abortLiveDatabase(ctx, client, p)
	}
	newGeneration := s.record == nil || mode == "requalify-restored-live-database"
	if mode == "requalify-restored-live-database" && (s.record == nil || !s.complete && !s.aborted) {
		return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_TERMINAL_GENERATION_REQUIRED")
	}
	if newGeneration && mode == "reconcile-restored-live-database" {
		return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_NOT_RECORDED")
	}
	if !newGeneration && (s.failed || s.aborting) {
		return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_CLEANUP_REQUIRED")
	}
	if !newGeneration && s.pending != "" && mode != "reconcile-restored-live-database" {
		return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_RECONCILIATION_REQUIRED")
	}
	maximumSteps := len(liveDatabaseSteps())
	if l.marker.Profile == wire.MVPPostgreSQLCPUProfile {
		maximumSteps = len(liveDatabaseSteps(&mvpNativeRuntimeActivation{}))
	}
	if e = admitProcessJournal(l, (maximumSteps+1)*3+6); e != nil {
		return result(), e
	}
	var plan RestoredLiveDatabasePlan
	if newGeneration {
		generation, e := nonce()
		if e != nil {
			return result(), e
		}
		plan, _, e = l.makeRestoredLiveDatabasePlan(ctx, client, worker, generation)
		if e != nil {
			return result(), e
		}
		r := liveDatabasePlanRecord(plan)
		id, e := nonce()
		if e != nil {
			return result(), e
		}
		if e = l.appendEvent(Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r}); e != nil {
			return result(), e
		}
	} else {
		plan, e = l.readLiveDatabasePlan(*s.record)
		if e != nil {
			if len(s.completed) != 0 {
				return result(), e
			}
			if _, stat := l.root.Lstat(liveDatabasePlanFile(s.record.GenerationID)); !os.IsNotExist(stat) {
				return result(), e
			}
			view := l.liveDatabaseAdmissionView(s.record.GenerationID)
			plan, _, e = view.makeRestoredLiveDatabasePlan(ctx, client, worker, s.record.GenerationID)
			if e != nil || !liveDatabaseIdentity(liveDatabasePlanRecord(plan), *s.record) {
				return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_PUBLICATION_RECONSTRUCTION_FAILED")
			}
		}
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		s := liveDatabaseState(l.events)
		if s.record == nil || s.complete || s.aborted {
			return
		}
		if plan.NativeRuntime != nil {
			if _, _, preparationError := mvpNativePreparation(l.events, *plan.NativeRuntime); preparationError != nil {
				// The exact original actor/result is required to reconcile a
				// possibly committed role transaction. Preserve it on timeout;
				// an explicit abort may abandon this target, never resend it.
				err = failure("OFFLINE_SCOPE_MVP_RUNTIME_PREPARATION_RECONCILIATION_REQUIRED")
				out = result()
				return
			}
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, e := l.abortLiveDatabase(cleanup, client, plan); e != nil {
			err = failure("OFFLINE_SCOPE_LIVE_DATABASE_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = result()
	}()
	for index, step := range liveDatabaseSteps(plan.NativeRuntime) {
		s = liveDatabaseState(l.events)
		if _, done := s.completed[index]; done {
			continue
		}
		filtered, e := l.liveDatabaseRuntimeView(ctx, client, plan)
		if e != nil {
			return result(), e
		}
		view := l.liveDatabaseAdmissionView(plan.GenerationID)
		current, files, e := view.makeRestoredLiveDatabasePlan(ctx, filtered, worker, plan.GenerationID)
		if e != nil {
			return result(), e
		}
		if !reflect.DeepEqual(current, plan) {
			return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_CURRENT_GENERATION_CHANGED")
		}
		if e = client.ValidateConfigurationFiles(files, plan.Tasks[0].UID, plan.Tasks[0].GID); e != nil {
			return result(), e
		}
		r := liveDatabasePlanRecord(plan)
		r.Step = index
		id, e := nonce()
		if e != nil {
			return result(), e
		}
		action, reference := "live_database_effect", ""
		reconcile := s.pending != ""
		if reconcile {
			if s.record.Step != index || !newGeneration && mode != "reconcile-restored-live-database" {
				return result(), failure("OFFLINE_SCOPE_LIVE_DATABASE_RECONCILIATION_REQUIRED")
			}
			action, reference = "live_database_reconcile", s.pending
			// The first publication intent belongs to this invocation and has no
			// SDK effect. Preserve its own result rather than inventing a reconcile.
			if newGeneration && index == 0 {
				action, reference, id, reconcile = "live_database_effect", "", s.pending, false
			}
		} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", RestoredLiveDatabase: &r}); e != nil {
			return result(), e
		}
		object, e := l.executeLiveDatabaseStep(ctx, client, plan, step, files, reconcile)
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
			code = "OFFLINE_SCOPE_LIVE_DATABASE_EFFECT_UNRESOLVED"
			if e.Error() == restoreDatabaseTaskFailed {
				outcome, code = "failed", restoreDatabaseTaskFailed
			}
		}
		r.ObjectID = object
		if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, RestoredLiveDatabase: &r}); je != nil {
			return result(), je
		}
		if e != nil {
			return result(), e
		}
	}
	return result(), nil
}

func (l *locked) liveDatabaseAdmissionView(generation string) *locked {
	view := *l
	view.events = []Event{}
	for _, e := range l.events {
		if e.RestoredLiveDatabase == nil || e.RestoredLiveDatabase.GenerationID != generation {
			view.events = append(view.events, e)
		}
	}
	return &view
}

type liveDatabaseRuntimeEngine struct {
	liveDatabaseEngine
	view runtimeEngine
}

func (v liveDatabaseRuntimeEngine) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	return v.view.RuntimeCandidates(ctx, scope, networks)
}
func (v liveDatabaseRuntimeEngine) InspectRuntimeNetwork(ctx context.Context, scope string) (engine.RuntimeNetwork, bool, error) {
	return v.view.InspectRuntimeNetwork(ctx, scope)
}
func (v liveDatabaseRuntimeEngine) VerifyVolumeConsumers(ctx context.Context, owner engine.Ownership, allowed []string) error {
	return v.view.VerifyVolumeConsumers(ctx, owner, allowed)
}
func (l *locked) liveDatabaseRuntimeView(ctx context.Context, c liveDatabaseEngine, p RestoredLiveDatabasePlan) (liveDatabaseEngine, error) {
	var view runtimeEngine = c
	s := liveDatabaseState(l.events)
	if s.record != nil && s.record.GenerationID == p.GenerationID {
		seen := func(phase, kind string) bool {
			for _, e := range l.events {
				if r := e.RestoredLiveDatabase; r != nil && r.GenerationID == p.GenerationID && e.Action == "live_database_effect" && e.Outcome == "started" && liveDatabaseSteps(r.NativeRuntime)[r.Step] == (restoreDatabaseStep{phase, kind}) {
					return true
				}
			}
			return false
		}
		done := func(phase, kind string) bool {
			for i := range s.completed {
				if liveDatabaseSteps(p.NativeRuntime)[i] == (restoreDatabaseStep{phase, kind}) {
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
	return liveDatabaseRuntimeEngine{liveDatabaseEngine: c, view: view}, nil
}
