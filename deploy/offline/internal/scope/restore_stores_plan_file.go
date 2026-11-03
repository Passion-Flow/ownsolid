package scope

import (
	"context"
	"os"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoreStoresPlanRecord(p RestoreStorePlan) RestoreStoresRecord {
	roles := []string{}
	for _, job := range p.Jobs {
		roles = append(roles, job.Role)
	}
	r := RestoreStoresRecord{GenerationID: p.GenerationID, PlanSHA256: wire.Digest(encode(p)), RestorePlanSHA256: p.RestorePlanSHA256, SQLProofSHA256: p.SQLProofSHA256, DatabaseProofSHA256: p.DatabaseProofSHA256, Stores: roles, Phase: "plan-publish"}
	if p.Native != nil {
		native := *p.Native
		r.Native = &native
		r.Phase = "task-create"
		r.Role = "native-observer"
		r.Kind = engine.NativePostgreSQLRestoreVerify
	}
	return r
}
func (l *locked) readBoundRestorePlan() (RestorePlan, error) {
	var p RestorePlan
	s := restoreState(l.events)
	if s.record == nil {
		return p, failure("OFFLINE_SCOPE_RESTORE_RESERVATION_NOT_RECORDED")
	}
	raw, e := regular(l.root, restorePlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != s.record.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !l.validRestorePlan(p) {
		return p, failure("OFFLINE_SCOPE_RESTORE_PLAN_CHANGED")
	}
	return p, nil
}

// Rechecking an admitted plan needs only protected target state. Original
// archives are required later for a new upload, never for generated cleanup.
func (l *locked) validateRestoreStorePlanContext(ctx context.Context, p RestoreStorePlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_RESTORE_STORES_PLAN_CONTEXT_CHANGED") }
	restore, e := l.readBoundRestorePlan()
	if e != nil {
		return e
	}
	if p.Native != nil {
		expected, e := l.makeMVPNativeRestoreStorePlan(ctx, restore)
		if e != nil {
			return e
		}
		meta := restoreStoresPlanRecord(p)
		if !validRestoreStoresRecord(&meta, l.events) || !reflect.DeepEqual(expected, p) {
			return bad()
		}
		return nil
	}
	meta := restoreStoresPlanRecord(p)
	if !validRestoreStoresRecord(&meta, l.events) || p.FormatVersion != 1 || p.ScopeID != l.marker.ScopeID || p.RestoreAttemptID != restore.AttemptID || p.DescriptorSHA256 != restore.Target.DescriptorSHA256 || p.RuntimeSHA256 != restore.Target.RuntimeSHA256 || len(p.Jobs) != len(restore.Source.Stores) {
		return bad()
	}
	base, _, e := l.makeRestoreDatabaseRecord(ctx, restore)
	if e != nil {
		return e
	}
	if p.Initializer != base.Initializer || p.PayloadSHA256 != base.PayloadSHA256 || p.ContextSHA256 != base.ContextSHA256 {
		return bad()
	}
	selected := map[string]bool{}
	for _, role := range restore.Source.Stores {
		if selected[role] {
			return bad()
		}
		selected[role] = true
	}
	for _, job := range p.Jobs {
		if !selected[job.Role] || job.Import.StoreRestore == nil {
			return bad()
		}
		delete(selected, job.Role)
		target, found := volumeState(l.events).roles[job.Role]
		if !found || target.Phase != phasePrepared || target.UID != base.BaseTask.UID || target.GID != base.BaseTask.GID {
			return bad()
		}
		profile := *job.Import.StoreRestore
		if profile.Artifact.MaximumEntries != restore.Request.MaximumEntries || profile.Artifact.Bytes > restore.Request.MaximumBytes {
			return bad()
		}
		admitted := false
		for _, kind := range restoredStoreCatalog() {
			if kind.role != job.Role {
				continue
			}
			expected, e := assembleRestoreStoreJob(base.BaseTask, l.volumeOwner(target), profile, p.GenerationID, kind)
			if e != nil || !reflect.DeepEqual(expected, job) {
				return bad()
			}
			admitted = true
		}
		if !admitted {
			return bad()
		}
	}
	if len(selected) > 0 {
		return bad()
	}
	return nil
}
func (l *locked) readRestoreStoresPlan() (RestoreStorePlan, error) {
	var p RestoreStorePlan
	s := restoreStoresState(l.events)
	if s.record == nil {
		return p, failure("OFFLINE_SCOPE_RESTORE_STORES_NOT_RECORDED")
	}
	raw, e := regular(l.root, restoreStoresPlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != s.record.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !restoreStoresIdentity(restoreStoresPlanRecord(p), *s.record) {
		return p, failure("OFFLINE_SCOPE_RESTORE_STORES_PLAN_UNAVAILABLE_OR_CHANGED")
	}
	if e = l.validateRestoreStorePlanContext(context.Background(), p); e != nil {
		return p, e
	}
	return p, nil
}
func (l *locked) verifyRestoreStoresPlan() error {
	s := restoreStoresState(l.events)
	_, e := l.root.Lstat(restoreStoresPlanFile)
	if s.record == nil {
		if os.IsNotExist(e) {
			return nil
		}
		return failure("OFFLINE_SCOPE_RESTORE_STORES_UNRECORDED_PLAN_PRESERVED")
	}
	if _, e = l.readRestoreStoresPlan(); e == nil {
		return nil
	}
	// No actor can run before publication completes. Preserve a partial/missing
	// first plan for explicit readback/reconstruction or metadata-only abort.
	if len(s.completed) == 0 && s.record.Step == 0 {
		return nil
	}
	return e
}
func (l *locked) publishRestoreStoresPlan(ctx context.Context, p RestoreStorePlan, reconcile bool, publish func(RestoreStorePlan) error) error {
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if e := l.validateRestoreStorePlanContext(ctx, p); e != nil {
		return e
	}
	r := restoreStoresPlanRecord(p)
	s := restoreStoresState(l.events)
	if s.record != nil && !restoreStoresIdentity(*s.record, r) {
		return failure("OFFLINE_SCOPE_RESTORE_STORES_CONTEXT_CHANGED")
	}
	if s.aborted || s.aborting || s.failed {
		return failure("OFFLINE_SCOPE_RESTORE_STORES_ABORTED_OR_FAILED")
	}
	if _, done := s.completed[0]; done {
		_, e := l.readRestoreStoresPlan()
		return e
	}
	if s.record != nil && !reconcile {
		return failure("OFFLINE_SCOPE_RESTORE_STORES_RECONCILIATION_REQUIRED")
	}
	if s.record == nil && reconcile {
		return failure("OFFLINE_SCOPE_RESTORE_STORES_NOT_RECORDED")
	}
	if e := admitProcessJournal(l, 3); e != nil {
		return e
	}
	id, e := nonce()
	if e != nil {
		return e
	}
	action, reference := "restore_stores_effect", ""
	if reconcile {
		action = "restore_stores_reconcile"
		reference = s.pending
	} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", RestoreStores: &r}); e != nil {
		return e
	}
	_, existsError := l.root.Lstat(restoreStoresPlanFile)
	if os.IsNotExist(existsError) {
		e = publish(p)
	} else if existsError != nil {
		e = failure("OFFLINE_SCOPE_RESTORE_STORES_PLAN_UNAVAILABLE")
	}
	if e == nil {
		e = l.syncNamedRestorePlan(restoreStoresPlanFile, encode(p))
	}
	if e == nil {
		_, e = l.readRestoreStoresPlan()
	}
	outcome, code := "completed", ""
	if e != nil {
		outcome = "uncertain"
		if reconcile {
			outcome = "unresolved"
		}
		code = "OFFLINE_SCOPE_RESTORE_STORES_PLAN_UNRESOLVED"
	}
	if journalError := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, RestoreStores: &r}); journalError != nil {
		return journalError
	}
	return e
}
