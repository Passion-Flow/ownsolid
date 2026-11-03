package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreSQLResult struct {
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

func (l *locked) restoreSQLResult() RestoreSQLResult {
	s := restoreSQLState(l.events)
	reservation := restoreState(l.events)
	r := RestoreSQLResult{ScopeID: l.marker.ScopeID, Status: "no_sql_restore_recorded", TotalSteps: len(restoreSQLStepsForRecord(s.record)), Qualification: "SQL_stage_only_not_schema_runtime_access_auth_stores_application_or_target_qualification"}
	if reservation.record != nil {
		r.AttemptID = reservation.record.AttemptID
	}
	if s.record == nil {
		return r
	}
	r.CompletedSteps = len(s.completed)
	r.Pending = true
	r.Status = "sql_restore_incomplete_requires_reconciliation"
	if s.failed {
		r.Status = "sql_restore_failed_requires_cleanup"
	}
	if s.aborting {
		r.Status = "sql_restore_cleanup_pending"
	}
	if s.aborted {
		r.Status = "sql_restore_aborted_target_preserved"
		r.Pending = false
	}
	if proof, ok := restoreSQLProof(l.events); ok {
		r.Status = "sql_restored_other_qualification_pending"
		r.Pending = false
		r.ProofSHA256 = proof
		if s.record.Native != nil {
			if observed, e := l.nativeRestoreObservation(context.Background(), "sql"); e == nil {
				r.NativePostgreSQL = observed.NativePostgreSQL
			}
		}
	}
	return r
}

func RunRestoreSQL(ctx context.Context, c configuration.Config, r configuration.RestoreRequest, mode string) (RestoreSQLResult, error) {
	if r.Validate() != nil || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoreSQLResult{}, failure("OFFLINE_SCOPE_RESTORE_SQL_CONFIGURATION_REQUIRED")
	}
	if mode != "restore-sql" && mode != "reconcile-restore-sql" && mode != "abort-restore-sql" {
		return RestoreSQLResult{}, failure("OFFLINE_SCOPE_RESTORE_SQL_COMMAND_INVALID")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoreSQLResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoreSQLResult{}, e
	}
	return runRestoreSQL(ctx, c, r, mode, client, worker)
}

func runRestoreSQL(ctx context.Context, c configuration.Config, r configuration.RestoreRequest, mode string, client restoreSQLEngine, worker runtimeOperator) (out RestoreSQLResult, err error) {
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	reservation := restoreState(l.events)
	if !reservation.reserved || reservation.pending != "" || reservation.aborted {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_COMPLETED_RESTORE_RESERVATION_REQUIRED")
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil || !reflect.DeepEqual(plan.Request, r) {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_CONTEXT_CHANGED")
	}
	if requiresMVPNativeRestore(l.marker, plan) {
		e := l.runMVPNativeRestoreStage(ctx, plan, "sql", mode, client, worker)
		return l.restoreSQLResult(), e
	}
	s := restoreSQLState(l.events)
	if s.aborted {
		if mode == "abort-restore-sql" {
			return l.restoreSQLResult(), nil
		}
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_ABORTED_NEW_TARGET_REQUIRED")
	}
	if mode == "abort-restore-sql" {
		if s.record == nil {
			return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_NOT_RECORDED")
		}
		return l.abortRestoreSQL(ctx, client, *s.record)
	}
	if s.complete {
		return l.restoreSQLResult(), nil
	}
	if s.failed || s.aborting {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_CLEANUP_REQUIRED")
	}
	if s.pending != "" && mode != "reconcile-restore-sql" {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_RECONCILIATION_REQUIRED")
	}
	if s.record == nil && mode != "restore-sql" {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_NOT_RECORDED")
	}
	record, files, e := l.makeRestoreSQLRecord(ctx, plan)
	if e != nil {
		return l.restoreSQLResult(), e
	}
	if s.record != nil && !restoreSQLIdentity(*s.record, record) {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_RECORDED_CONTEXT_CHANGED")
	}
	if s.record == nil {
		f, e := os.OpenFile(filepath.Join(r.BackupRoot, "postgres.tar"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_ARCHIVE_UNAVAILABLE")
		}
		_, e = client.ValidatePostgresRestoreArchive(ctx, record.Task, f)
		closeError := f.Close()
		if e != nil {
			return l.restoreSQLResult(), e
		}
		if closeError != nil {
			return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_ARCHIVE_CLOSE_FAILED")
		}
	}
	if e = admitProcessJournal(l, (len(restoreSQLSteps())-len(s.completed))*3+3); e != nil {
		return l.restoreSQLResult(), e
	}
	if e = backupDiskAdmission(filepath.Join(c.ScopeRoot, restorePlanFile), record.Task.Restore.ArchiveBytes); e != nil {
		return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_DISK_HEADROOM_REQUIRED")
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		state := restoreSQLState(l.events)
		if state.record == nil || state.aborted || state.complete {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, cleanupError := l.abortRestoreSQL(cleanup, client, *state.record); cleanupError != nil {
			err = failure("OFFLINE_SCOPE_RESTORE_SQL_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = l.restoreSQLResult()
	}()
	for index, step := range restoreSQLSteps() {
		s = restoreSQLState(l.events)
		if _, done := s.completed[index]; done {
			continue
		}
		view, e := l.restoreSQLRuntimeView(ctx, client, record)
		if e != nil {
			return l.restoreSQLResult(), e
		}
		verified, e := l.verifyRuntimeContext(ctx, view, worker, false)
		if e != nil {
			return l.restoreSQLResult(), e
		}
		if e = restoreTargetLineage(l.marker, l.events, verified, plan.Source, plan.Compatibility); e != nil {
			return l.restoreSQLResult(), e
		}
		stepRecord := record
		stepRecord.Step = index
		stepRecord.Phase = step.phase
		stepRecord.Role = step.role
		stepRecord.ObjectID = ""
		stepRecord.ResultSHA256 = ""
		id, e := nonce()
		if e != nil {
			return l.restoreSQLResult(), e
		}
		action, reference := "restore_sql_effect", ""
		reconcile := s.pending != ""
		if reconcile {
			if mode != "reconcile-restore-sql" || s.record == nil || s.record.Step != index {
				return l.restoreSQLResult(), failure("OFFLINE_SCOPE_RESTORE_SQL_RECONCILIATION_REQUIRED")
			}
			action = "restore_sql_reconcile"
			reference = s.pending
		} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", RestoreSQL: &stepRecord}); e != nil {
			return l.restoreSQLResult(), e
		}
		observed, e := l.executeRestoreSQLStep(ctx, client, stepRecord, files, r, reconcile)
		if _, bindingError := l.checkEngine(ctx, client); bindingError != nil {
			e = bindingError
		}
		outcome, code := "completed", ""
		if e != nil {
			observed = stepRecord
			outcome = "uncertain"
			if reconcile {
				outcome = "unresolved"
			}
			code = "OFFLINE_SCOPE_RESTORE_SQL_EFFECT_UNRESOLVED"
			if e.Error() == restoreSQLTaskFailed {
				outcome = "failed"
				code = restoreSQLTaskFailed
			}
		}
		if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, RestoreSQL: &observed}); je != nil {
			return l.restoreSQLResult(), je
		}
		if e != nil {
			return l.restoreSQLResult(), e
		}
	}
	return l.restoreSQLResult(), nil
}

func (l *locked) abortRestoreSQL(ctx context.Context, c restoreSQLEngine, record RestoreSQLRecord) (RestoreSQLResult, error) {
	if _, e := l.checkEngine(ctx, c); e != nil {
		return l.restoreSQLResult(), e
	}
	s := restoreSQLState(l.events)
	if s.aborted {
		return l.restoreSQLResult(), nil
	}
	record.ObjectID = ""
	record.ResultSHA256 = ""
	id, reference := s.pending, s.abortReference
	if !s.aborting {
		var e error
		id, e = nonce()
		if e != nil {
			return l.restoreSQLResult(), e
		}
		reference = s.pending
		if e = l.appendEvent(Event{OperationID: id, Action: "restore_sql_abort", ReferenceOperationID: reference, Outcome: "started", RestoreSQL: &record}); e != nil {
			return l.restoreSQLResult(), e
		}
	}
	cleanupError := l.removeRestoreSQLActors(ctx, c, record)
	outcome, code := "completed", ""
	if cleanupError != nil {
		outcome = "uncertain"
		code = "OFFLINE_SCOPE_RESTORE_SQL_CLEANUP_UNRESOLVED"
	}
	if e := l.appendEvent(Event{OperationID: id, Action: "restore_sql_abort", ReferenceOperationID: reference, Outcome: outcome, Code: code, RestoreSQL: &record}); e != nil {
		return l.restoreSQLResult(), e
	}
	return l.restoreSQLResult(), cleanupError
}

func (l *locked) removeRestoreSQLActors(ctx context.Context, c restoreSQLEngine, r RestoreSQLRecord) error {
	// Once volumes were removed, their task cannot still be a consumer. Query
	// independent candidates first so repeated cleanup needs no absent volume.
	if restoreSQLIntentSeen(l.events, "task-create", "postgres") && !restoreSQLStepCompleted(l.events, "task-remove", "postgres") {
		objects, e := c.RuntimeCandidates(ctx, l.marker.ScopeID, false)
		if e != nil {
			return e
		}
		present := false
		for _, o := range objects {
			if o.Labels["io.projectone.operation"] == r.Task.Owner.OperationID && o.Labels["io.projectone.role"] == r.Task.Owner.Role {
				present = true
			}
		}
		if present {
			task, found, e := c.InspectPostgresBackupTask(ctx, r.Task)
			if e != nil {
				return e
			}
			if found {
				if task.State.Running {
					if e = c.StopPostgresBackupTask(ctx, r.Task, task.ID, l.config.Compose.Process.StopSeconds); e != nil {
						return e
					}
				}
				if e = c.RemovePostgresBackupTask(ctx, r.Task, task.ID); e != nil {
					return e
				}
			}
		}
	}
	for _, role := range []string{"input", "source", "result"} {
		if !restoreSQLIntentSeen(l.events, "volume-create", role) {
			continue
		}
		owner, _ := restoreSQLVolume(r, role)
		if _, found, e := c.InspectVolume(ctx, owner); e != nil {
			return e
		} else if !found {
			continue
		}
		utility, found, e := c.InspectUtility(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID)
		if e != nil {
			return e
		}
		if found {
			if !utilityIDPattern.MatchString(utility.ID) {
				return failure("OFFLINE_SCOPE_RESTORE_SQL_CLEANUP_ID_INVALID")
			}
			if e = c.RemoveUtility(ctx, owner, r.Initializer, r.Task.UID, r.Task.GID); e != nil {
				return e
			}
		}
		if e = c.RemovePostgresRestoreVolume(ctx, owner); e != nil {
			return e
		}
		if _, found, e = c.InspectVolume(ctx, owner); e != nil || found {
			return failure("OFFLINE_SCOPE_RESTORE_SQL_CLEANUP_VOLUME_UNRESOLVED")
		}
	}
	return nil
}
