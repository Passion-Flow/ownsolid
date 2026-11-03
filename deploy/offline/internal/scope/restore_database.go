package scope

import (
	"context"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreDatabaseResult struct {
	ScopeID          string                                 `json:"scope_id"`
	AttemptID        string                                 `json:"attempt_id"`
	Status           string                                 `json:"status"`
	CompletedSteps   int                                    `json:"completed_steps"`
	TotalSteps       int                                    `json:"total_steps"`
	Pending          bool                                   `json:"pending"`
	ProofSHA256      string                                 `json:"proof_sha256,omitempty"`
	Qualification    string                                 `json:"qualification"`
	NativePostgreSQL *wire.NativePostgreSQLRestoreReference `json:"native_postgresql,omitempty"`
}

func (l *locked) restoreDatabaseResult() RestoreDatabaseResult {
	s := restoreDatabaseState(l.events)
	r := RestoreDatabaseResult{ScopeID: l.marker.ScopeID, Status: "no_restored_database_qualification_recorded", TotalSteps: len(restoreDatabaseStepsForRecord(s.record)), Qualification: "restored_database_stage_only_not_store_import_application_activation_browser_or_target_qualification"}
	if reservation := restoreState(l.events); reservation.record != nil {
		r.AttemptID = reservation.record.AttemptID
	}
	if s.record == nil {
		return r
	}
	r.Pending = true
	r.CompletedSteps = len(s.completed)
	r.Status = "restored_database_incomplete_requires_reconciliation"
	if s.failed {
		r.Status = "restored_database_failed_requires_cleanup"
	}
	if s.aborting {
		r.Status = "restored_database_cleanup_pending"
	}
	if s.aborted {
		r.Status = "restored_database_aborted_target_preserved"
		r.Pending = false
	}
	if proof, ok := restoreDatabaseProof(l.events); ok {
		r.Status = "restored_database_qualified_stores_activation_pending"
		r.Pending = false
		r.ProofSHA256 = proof
		if s.record.Native != nil {
			if observed, e := l.nativeRestoreObservation(context.Background(), "database"); e == nil {
				r.NativePostgreSQL = observed.NativePostgreSQL
			}
		}
	}
	return r
}
func RunRestoreDatabase(ctx context.Context, c configuration.Config, r configuration.RestoreRequest, mode string) (RestoreDatabaseResult, error) {
	if r.Validate() != nil || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoreDatabaseResult{}, failure("OFFLINE_SCOPE_RESTORE_DATABASE_CONFIGURATION_REQUIRED")
	}
	if mode != "qualify-restored-database" && mode != "reconcile-restored-database" && mode != "abort-restored-database" {
		return RestoreDatabaseResult{}, failure("OFFLINE_SCOPE_RESTORE_DATABASE_COMMAND_INVALID")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoreDatabaseResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoreDatabaseResult{}, e
	}
	return runRestoreDatabase(ctx, c, r, mode, client, worker)
}
func runRestoreDatabase(ctx context.Context, c configuration.Config, request configuration.RestoreRequest, mode string, client restoreDatabaseEngine, worker runtimeOperator) (out RestoreDatabaseResult, err error) {
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	reservation := restoreState(l.events)
	if !reservation.reserved || reservation.pending != "" || reservation.aborted || reservation.record == nil {
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_COMPLETED_RESTORE_RESERVATION_REQUIRED")
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil || !reflect.DeepEqual(plan.Request, request) {
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_CONTEXT_CHANGED")
	}
	if requiresMVPNativeRestore(l.marker, plan) {
		e := l.runMVPNativeRestoreStage(ctx, plan, "database", mode, client, worker)
		return l.restoreDatabaseResult(), e
	}
	s := restoreDatabaseState(l.events)
	if s.aborted {
		if mode == "abort-restored-database" {
			return l.restoreDatabaseResult(), nil
		}
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_ABORTED_NEW_TARGET_REQUIRED")
	}
	if s.complete {
		return l.restoreDatabaseResult(), nil
	}
	if mode == "abort-restored-database" {
		if s.record == nil {
			return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_NOT_RECORDED")
		}
		return l.abortRestoreDatabase(ctx, client, *s.record)
	}
	if s.failed || s.aborting {
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_CLEANUP_REQUIRED")
	}
	if s.pending != "" && mode != "reconcile-restored-database" {
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_RECONCILIATION_REQUIRED")
	}
	if s.record == nil && mode != "qualify-restored-database" {
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_NOT_RECORDED")
	}
	record, files, e := l.makeRestoreDatabaseRecord(ctx, plan)
	if e != nil {
		return l.restoreDatabaseResult(), e
	}
	if s.record != nil && !restoreDatabaseIdentity(*s.record, record) {
		return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_CONTEXT_CHANGED")
	}
	if e = client.ValidateConfigurationFiles(files, record.BaseTask.UID, record.BaseTask.GID); e != nil {
		return l.restoreDatabaseResult(), e
	}
	if e = admitProcessJournal(l, (len(restoreDatabaseSteps())-len(s.completed))*3+3); e != nil {
		return l.restoreDatabaseResult(), e
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		state := restoreDatabaseState(l.events)
		if state.record == nil || state.aborted || state.complete {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, cleanupError := l.abortRestoreDatabase(cleanup, client, *state.record); cleanupError != nil {
			err = failure("OFFLINE_SCOPE_RESTORE_DATABASE_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = l.restoreDatabaseResult()
	}()
	for index, step := range restoreDatabaseSteps() {
		s = restoreDatabaseState(l.events)
		if _, done := s.completed[index]; done {
			continue
		}
		view, e := l.restoreDatabaseRuntimeView(ctx, client, record)
		if e != nil {
			return l.restoreDatabaseResult(), e
		}
		verified, e := l.verifyRuntimeContext(ctx, view, worker, false)
		if e != nil {
			return l.restoreDatabaseResult(), e
		}
		if e = restoreTargetLineage(l.marker, l.events, verified, plan.Source, plan.Compatibility); e != nil {
			return l.restoreDatabaseResult(), e
		}
		current := record
		current.Step = index
		current.Phase = step.phase
		current.Kind = step.kind
		current.ObjectID = ""
		id, e := nonce()
		if e != nil {
			return l.restoreDatabaseResult(), e
		}
		action, reference := "restore_database_effect", ""
		reconcile := s.pending != ""
		if reconcile {
			if mode != "reconcile-restored-database" || s.record == nil || s.record.Step != index {
				return l.restoreDatabaseResult(), failure("OFFLINE_SCOPE_RESTORE_DATABASE_RECONCILIATION_REQUIRED")
			}
			action = "restore_database_reconcile"
			reference = s.pending
		} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", RestoreDatabase: &current}); e != nil {
			return l.restoreDatabaseResult(), e
		}
		observed, e := l.executeRestoreDatabaseStep(ctx, client, current, files, reconcile)
		if _, bindingError := l.checkEngine(ctx, client); bindingError != nil {
			e = bindingError
		}
		outcome, code := "completed", ""
		if e != nil {
			observed = current
			outcome = "uncertain"
			if reconcile {
				outcome = "unresolved"
			}
			code = "OFFLINE_SCOPE_RESTORE_DATABASE_EFFECT_UNRESOLVED"
			if e.Error() == restoreDatabaseTaskFailed {
				outcome = "failed"
				code = restoreDatabaseTaskFailed
			}
		}
		if journalError := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, RestoreDatabase: &observed}); journalError != nil {
			return l.restoreDatabaseResult(), journalError
		}
		if e != nil {
			return l.restoreDatabaseResult(), e
		}
	}
	return l.restoreDatabaseResult(), nil
}
func (l *locked) abortRestoreDatabase(ctx context.Context, c restoreDatabaseEngine, r RestoreDatabaseRecord) (RestoreDatabaseResult, error) {
	if _, e := l.checkEngine(ctx, c); e != nil {
		return l.restoreDatabaseResult(), e
	}
	s := restoreDatabaseState(l.events)
	if s.aborted || s.complete {
		return l.restoreDatabaseResult(), nil
	}
	r.ObjectID = ""
	id, reference := s.pending, s.abortReference
	if !s.aborting {
		var e error
		id, e = nonce()
		if e != nil {
			return l.restoreDatabaseResult(), e
		}
		reference = s.pending
		if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "restore_database_abort", Outcome: "started", RestoreDatabase: &r}); e != nil {
			return l.restoreDatabaseResult(), e
		}
	}
	cleanupError := l.removeRestoreDatabaseActors(ctx, c, r)
	outcome, code := "completed", ""
	if cleanupError != nil {
		outcome = "uncertain"
		code = "OFFLINE_SCOPE_RESTORE_DATABASE_CLEANUP_UNRESOLVED"
	}
	if e := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "restore_database_abort", Outcome: outcome, Code: code, RestoreDatabase: &r}); e != nil {
		return l.restoreDatabaseResult(), e
	}
	return l.restoreDatabaseResult(), cleanupError
}
func (l *locked) removeRestoreDatabaseActors(ctx context.Context, c restoreDatabaseEngine, r RestoreDatabaseRecord) error {
	for _, step := range restoreDatabaseSteps() {
		if step.phase != "task-create" || !restoreDatabaseIntentSeen(l.events, "task-create", step.kind) || restoreDatabaseStepCompleted(l.events, "task-remove", step.kind) {
			continue
		}
		task := r.task(step.kind)
		objects, e := c.RuntimeCandidates(ctx, l.marker.ScopeID, false)
		if e != nil {
			return e
		}
		present := false
		for _, o := range objects {
			if o.Labels["io.projectone.operation"] == task.Owner.OperationID && o.Labels["io.projectone.role"] == task.Owner.Role {
				present = true
			}
		}
		if !present {
			continue
		}
		object, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil {
			return e
		}
		if found {
			if object.State.Running {
				if e = c.StopDatabaseTask(ctx, task, object.ID, l.config.Compose.Process.StopSeconds); e != nil {
					return e
				}
			}
			if e = c.RemoveDatabaseTask(ctx, task, object.ID); e != nil {
				return e
			}
		}
	}
	if !restoreDatabaseIntentSeen(l.events, "volume-create", "") {
		return nil
	}
	owner := r.BaseTask.VolumeOwner
	if _, found, e := c.InspectVolume(ctx, owner); e != nil {
		return e
	} else if !found {
		return nil
	}
	if _, found, e := c.InspectUtility(ctx, owner, r.Initializer, r.BaseTask.UID, r.BaseTask.GID); e != nil {
		return e
	} else if found {
		if e = c.RemoveUtility(ctx, owner, r.Initializer, r.BaseTask.UID, r.BaseTask.GID); e != nil {
			return e
		}
	}
	if e := c.RemoveStoreRestoreVolume(ctx, owner); e != nil {
		return e
	}
	if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
		return failure("OFFLINE_SCOPE_RESTORE_DATABASE_CLEANUP_VOLUME_UNRESOLVED")
	}
	return nil
}
