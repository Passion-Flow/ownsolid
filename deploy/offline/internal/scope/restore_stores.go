package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreStoresResult struct {
	ScopeID          string                                 `json:"scope_id"`
	GenerationID     string                                 `json:"generation_id,omitempty"`
	Status           string                                 `json:"status"`
	PlanPublished    bool                                   `json:"plan_published"`
	CompletedSteps   int                                    `json:"completed_steps"`
	TotalSteps       int                                    `json:"total_steps"`
	Pending          bool                                   `json:"pending"`
	ProofSHA256      string                                 `json:"proof_sha256,omitempty"`
	Qualification    string                                 `json:"qualification"`
	NativePostgreSQL *wire.NativePostgreSQLRestoreReference `json:"native_postgresql,omitempty"`
}

func (l *locked) restoreStoresResult() RestoreStoresResult {
	s := restoreStoresState(l.events)
	r := RestoreStoresResult{ScopeID: l.marker.ScopeID, Status: "no_selected_store_restore_recorded", Qualification: "selected_store_stage_only_not_application_activation_health_browser_or_target_qualification"}
	if s.record == nil {
		return r
	}
	r.GenerationID = s.record.GenerationID
	r.Pending = true
	r.CompletedSteps = len(s.completed)
	r.TotalSteps = len(restoreStoresStepsForRecord(s.record))
	_, r.PlanPublished = s.completed[0]
	r.Status = "selected_store_restore_incomplete"
	if !r.PlanPublished {
		r.Status = "selected_store_plan_publication_unresolved"
	}
	if s.pending != "" {
		r.Status = "selected_store_restore_requires_reconciliation"
	}
	if s.failed {
		r.Status = "selected_store_restore_failed_requires_cleanup"
	}
	if s.aborting {
		r.Status = "selected_store_restore_cleanup_pending"
	}
	if s.aborted {
		r.Status = "selected_store_restore_aborted_target_preserved"
		r.Pending = false
	}
	if proof, ok := restoreStoresProof(l.events); ok {
		r.ProofSHA256 = proof
		if s.record.Native != nil {
			if observed, e := l.nativeRestoreObservation(context.Background(), "stores"); e == nil {
				r.NativePostgreSQL = observed.NativePostgreSQL
			}
		}
		r.Pending = false
		r.Status = "selected_stores_verified_activation_pending"
	}
	return r
}
func restoreStoresMode(mode string) bool {
	return mode == "restore-stores" || mode == "reconcile-restored-stores" || mode == "abort-restored-stores"
}
func RunRestoreStores(ctx context.Context, c configuration.Config, request configuration.RestoreRequest, mode string) (RestoreStoresResult, error) {
	if request.Validate() != nil {
		return RestoreStoresResult{}, failure("OFFLINE_SCOPE_RESTORE_STORES_CONFIGURATION_REQUIRED")
	}
	if !restoreStoresMode(mode) {
		return RestoreStoresResult{}, failure("OFFLINE_SCOPE_RESTORE_STORES_COMMAND_INVALID")
	}
	if mode == "abort-restored-stores" {
		out, handled, e := abortRestoreStoresMetadata(ctx, c, request)
		if handled || e != nil {
			return out, e
		}
	}
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoreStoresResult{}, failure("OFFLINE_SCOPE_RESTORE_STORES_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoreStoresResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoreStoresResult{}, e
	}
	return runRestoreStores(ctx, c, request, mode, client, worker)
}
func (l *locked) restoreStoresRequest(request configuration.RestoreRequest) (RestorePlan, error) {
	p, e := l.readBoundRestorePlan()
	if e != nil {
		return p, e
	}
	if !reflect.DeepEqual(p.Request, request) {
		return p, failure("OFFLINE_SCOPE_RESTORE_RESERVATION_CONTEXT_CHANGED")
	}
	return p, nil
}
func abortRestoreStoresMetadata(ctx context.Context, c configuration.Config, request configuration.RestoreRequest) (RestoreStoresResult, bool, error) {
	if ctx.Err() != nil {
		return RestoreStoresResult{}, true, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return RestoreStoresResult{}, true, e
	}
	defer l.close()
	if _, e = l.restoreStoresRequest(request); e != nil {
		return l.restoreStoresResult(), true, e
	}
	s := restoreStoresState(l.events)
	if s.record == nil {
		return l.restoreStoresResult(), true, failure("OFFLINE_SCOPE_RESTORE_STORES_NOT_RECORDED")
	}
	if s.complete || s.aborted {
		return l.restoreStoresResult(), true, nil
	}
	if restoreStoresActorsIntended(l.events) {
		return l.restoreStoresResult(), false, nil
	}
	return l.finishRestoreStoresAbort(ctx, *s.record, nil)
}
func restoreStoresActorsIntended(events []Event) bool {
	for _, e := range events {
		if e.RestoreStores != nil && e.Action == "restore_stores_effect" && e.RestoreStores.Phase != "plan-publish" {
			return true
		}
	}
	return false
}
func runRestoreStores(ctx context.Context, c configuration.Config, request configuration.RestoreRequest, mode string, client restoreStoresEngine, worker runtimeOperator) (out RestoreStoresResult, err error) {
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	original, e := l.restoreStoresRequest(request)
	if e != nil {
		return l.restoreStoresResult(), e
	}
	if requiresMVPNativeRestore(l.marker, original) {
		e := l.runMVPNativeRestoreStage(ctx, original, "stores", mode, client, worker)
		return l.restoreStoresResult(), e
	}
	state := restoreStoresState(l.events)
	if state.aborted {
		if mode == "abort-restored-stores" {
			return l.restoreStoresResult(), nil
		}
		return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_ABORTED_NEW_TARGET_REQUIRED")
	}
	if state.complete {
		return l.restoreStoresResult(), nil
	}
	if mode == "abort-restored-stores" {
		if state.record == nil {
			return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_NOT_RECORDED")
		}
		if !restoreStoresActorsIntended(l.events) {
			out, _, e := l.finishRestoreStoresAbort(ctx, *state.record, nil)
			return out, e
		}
		p, e := l.readRestoreStoresPlan()
		if e != nil {
			return l.restoreStoresResult(), e
		}
		return l.abortRestoreStores(ctx, client, p, *state.record)
	}
	if state.failed || state.aborting {
		return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_CLEANUP_REQUIRED")
	}
	if state.pending != "" && mode != "reconcile-restored-stores" {
		return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_RECONCILIATION_REQUIRED")
	}
	if state.record == nil && mode != "restore-stores" {
		return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_NOT_RECORDED")
	}
	var plan RestoreStorePlan
	if state.record == nil {
		generation, e := nonce()
		if e != nil {
			return l.restoreStoresResult(), e
		}
		plan, _, e = l.makeRestoreStorePlan(ctx, original, generation)
		if e != nil {
			return l.restoreStoresResult(), e
		}
		if e = l.publishRestoreStoresPlan(ctx, plan, false, func(p RestoreStorePlan) error { return writeBackupFile(l.root, restoreStoresPlanFile, encode(p)) }); e != nil {
			return l.restoreStoresResult(), e
		}
	} else if _, published := state.completed[0]; !published {
		plan, e = l.readRestoreStoresPlan()
		if e != nil {
			if _, fileError := l.root.Lstat(restoreStoresPlanFile); !os.IsNotExist(fileError) {
				return l.restoreStoresResult(), e
			}
			plan, _, e = l.makeRestoreStorePlan(ctx, original, state.record.GenerationID)
			if e != nil {
				return l.restoreStoresResult(), e
			}
		}
		if e = l.publishRestoreStoresPlan(ctx, plan, true, func(p RestoreStorePlan) error { return writeBackupFile(l.root, restoreStoresPlanFile, encode(p)) }); e != nil {
			return l.restoreStoresResult(), e
		}
	} else {
		plan, e = l.readRestoreStoresPlan()
		if e != nil {
			return l.restoreStoresResult(), e
		}
	}
	if e = l.validateRestoreStorePlanContext(ctx, plan); e != nil {
		return l.restoreStoresResult(), e
	}
	files, e := l.makeRestoreQualificationInput(ctx, original)
	if e != nil {
		return l.restoreStoresResult(), e
	}
	if e = client.ValidateConfigurationFiles(files, plan.Jobs[0].Import.UID, plan.Jobs[0].Import.GID); e != nil {
		return l.restoreStoresResult(), e
	}
	var bytes int64
	for _, job := range plan.Jobs {
		size := job.Import.StoreRestore.Artifact.Bytes
		if size < 1 || bytes > request.MaximumBytes-size {
			return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_TOTAL_ARCHIVE_BUDGET_EXCEEDED")
		}
		bytes += size
	}
	if e = backupDiskAdmission(filepath.Join(c.ScopeRoot, restorePlanFile), bytes*2+l.maximumJournalBytes()); e != nil {
		return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_DISK_HEADROOM_REQUIRED")
	}
	state = restoreStoresState(l.events)
	if e = admitProcessJournal(l, (len(restoreStoresSteps(state.record.Stores))-len(state.completed))*3+3); e != nil {
		return l.restoreStoresResult(), e
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		s := restoreStoresState(l.events)
		if s.record == nil || s.complete || s.aborted {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, e := l.abortRestoreStores(cleanup, client, plan, *s.record); e != nil {
			err = failure("OFFLINE_SCOPE_RESTORE_STORES_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = l.restoreStoresResult()
	}()
	base := restoreStoresPlanRecord(plan)
	for index, step := range restoreStoresSteps(base.Stores) {
		state = restoreStoresState(l.events)
		if _, done := state.completed[index]; done {
			continue
		}
		if index == 0 {
			return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_PLAN_PUBLICATION_REQUIRED")
		}
		view, e := l.restoreStoresRuntimeView(ctx, client, plan)
		if e != nil {
			return l.restoreStoresResult(), e
		}
		verified, e := l.verifyRuntimeContext(ctx, view, worker, false)
		if e != nil {
			return l.restoreStoresResult(), e
		}
		if e = restoreTargetLineage(l.marker, l.events, verified, original.Source, original.Compatibility); e != nil {
			return l.restoreStoresResult(), e
		}
		current := base
		current.Step = index
		current.Phase = step.phase
		current.Role = step.role
		current.Kind = step.kind
		current.ObjectID = ""
		id, e := nonce()
		if e != nil {
			return l.restoreStoresResult(), e
		}
		action, reference := "restore_stores_effect", ""
		reconcile := state.pending != ""
		if reconcile {
			if mode != "reconcile-restored-stores" || state.record == nil || state.record.Step != index {
				return l.restoreStoresResult(), failure("OFFLINE_SCOPE_RESTORE_STORES_RECONCILIATION_REQUIRED")
			}
			action = "restore_stores_reconcile"
			reference = state.pending
		} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", RestoreStores: &current}); e != nil {
			return l.restoreStoresResult(), e
		}
		observed, e := l.executeRestoreStoresStep(ctx, client, plan, current, files, original.Request.BackupRoot, reconcile)
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
			code = "OFFLINE_SCOPE_RESTORE_STORES_EFFECT_UNRESOLVED"
			if e.Error() == restoreStoresTaskFailed {
				outcome = "failed"
				code = restoreStoresTaskFailed
			}
		}
		if journalError := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, RestoreStores: &observed}); journalError != nil {
			return l.restoreStoresResult(), journalError
		}
		if e != nil {
			return l.restoreStoresResult(), e
		}
	}
	return l.restoreStoresResult(), nil
}

// Metadata-only abort proves no generated effect was submitted. It is a real
// terminal journal transition and deliberately performs no Engine operation.
func (l *locked) finishRestoreStoresAbort(ctx context.Context, r RestoreStoresRecord, cleanup func() error) (RestoreStoresResult, bool, error) {
	if cleanup == nil && restoreStoresActorsIntended(l.events) {
		return l.restoreStoresResult(), true, failure("OFFLINE_SCOPE_RESTORE_STORES_GENERATED_CLEANUP_REQUIRED")
	}
	if ctx.Err() != nil {
		return l.restoreStoresResult(), true, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	s := restoreStoresState(l.events)
	if s.aborted || s.complete {
		return l.restoreStoresResult(), true, nil
	}
	r.ObjectID = ""
	id, reference := s.pending, s.abortReference
	if !s.aborting {
		var e error
		id, e = nonce()
		if e != nil {
			return l.restoreStoresResult(), true, e
		}
		reference = s.pending
		if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "restore_stores_abort", Outcome: "started", RestoreStores: &r}); e != nil {
			return l.restoreStoresResult(), true, e
		}
	}
	var cleanupError error
	if cleanup != nil {
		cleanupError = cleanup()
	}
	outcome, code := "completed", ""
	if cleanupError != nil {
		outcome = "uncertain"
		code = "OFFLINE_SCOPE_RESTORE_STORES_CLEANUP_UNRESOLVED"
	}
	if e := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "restore_stores_abort", Outcome: outcome, Code: code, RestoreStores: &r}); e != nil {
		return l.restoreStoresResult(), true, e
	}
	return l.restoreStoresResult(), true, cleanupError
}
func (l *locked) abortRestoreStores(ctx context.Context, c restoreStoresEngine, p RestoreStorePlan, r RestoreStoresRecord) (RestoreStoresResult, error) {
	if _, e := l.checkEngine(ctx, c); e != nil {
		return l.restoreStoresResult(), e
	}
	out, _, e := l.finishRestoreStoresAbort(ctx, r, func() error { return l.removeRestoreStoresActors(ctx, c, p) })
	return out, e
}
func (l *locked) removeRestoreStoresActors(ctx context.Context, c restoreStoresEngine, p RestoreStorePlan) error {
	for _, job := range p.Jobs {
		for _, task := range []engine.DatabaseTask{job.Import, job.Verify} {
			if !restoreStoresIntentSeen(l.events, "task-create", job.Role, task.Kind) || restoreStoresStepCompleted(l.events, "task-remove", job.Role, task.Kind) {
				continue
			}
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
	}
	roles := []string{}
	for _, job := range p.Jobs {
		roles = append(roles, job.Role)
	}
	roles = append(roles, "configuration")
	uid, gid := p.Jobs[0].Import.UID, p.Jobs[0].Import.GID
	for _, role := range roles {
		if !restoreStoresIntentSeen(l.events, "volume-create", role, "") {
			continue
		}
		owner, _ := restoreStoresVolume(p, role)
		if _, found, e := c.InspectVolume(ctx, owner); e != nil {
			return e
		} else if !found {
			continue
		}
		if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
			return e
		} else if found {
			if e = c.RemoveUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
				return e
			}
		}
		if e := c.RemoveStoreRestoreVolume(ctx, owner); e != nil {
			return e
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
			return failure("OFFLINE_SCOPE_RESTORE_STORES_CLEANUP_VOLUME_UNRESOLVED")
		}
	}
	return nil
}
