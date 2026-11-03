package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const restorePlanFile = "restore-plan.json"

type RestorePlan struct {
	FormatVersion  int                          `json:"format_version"`
	AttemptID      string                       `json:"attempt_id"`
	Request        configuration.RestoreRequest `json:"request"`
	Source         RestoreSourceResult          `json:"source"`
	Target         RestoreTargetResult          `json:"target"`
	RequiredStages []string                     `json:"required_stages"`
	Compatibility  *restoreCompatibilityProof   `json:"compatibility,omitempty"`
}

func restoreRequiredStages() []string {
	return []string{"logical-sql-restore", "independent-schema-check", "restricted-runtime-access", "restored-authority-invalidation", "declared-store-import-and-verification", "generated-resource-cleanup", "restored-activation-qualification"}
}

type RestoreReservationResult struct {
	ScopeID       string `json:"scope_id"`
	AttemptID     string `json:"attempt_id"`
	Status        string `json:"status"`
	PlanSHA256    string `json:"plan_sha256"`
	Pending       bool   `json:"pending"`
	Qualification string `json:"qualification"`
}

func (l *locked) restoreReservationResult() RestoreReservationResult {
	s := restoreState(l.events)
	r := RestoreReservationResult{ScopeID: l.marker.ScopeID, Status: "no_restore_reserved", Qualification: "restore_reservation_only_no_SQL_or_store_import_access_auth_activation_browser_or_target_qualification"}
	if s.record == nil {
		return r
	}
	r.AttemptID = s.record.AttemptID
	r.PlanSHA256 = s.record.PlanSHA256
	r.Pending = true
	r.Status = "restore_reservation_outcome_unresolved"
	if s.reserved {
		r.Status = "restore_reserved_execution_pending"
	}
	if s.pending != "" {
		r.Status = "restore_reservation_outcome_unresolved"
	}
	if s.aborted {
		r.Pending = false
		r.Status = "restore_reservation_aborted_no_execution"
	}
	if sql := restoreSQLState(l.events); sql.record != nil {
		r.Status = "restore_sql_execution_pending_qualification"
		r.Qualification = "restore_progress_only_not_schema_access_auth_store_or_application_activation_qualification"
		if sql.complete {
			r.Status = "restore_sql_complete_qualification_pending"
		}
		if sql.aborted {
			r.Status = "restore_sql_aborted_new_target_required"
		}
	}
	if database := restoreDatabaseState(l.events); database.record != nil {
		r.Status = "restore_database_qualification_incomplete"
		if database.complete {
			r.Status = "restore_database_complete_stores_pending"
		}
		if database.aborted {
			r.Status = "restore_database_aborted_new_target_required"
		}
	}
	if stores := restoreStoresState(l.events); stores.record != nil {
		r.Status = "restore_stores_incomplete_activation_pending"
		if stores.complete {
			r.Status = "restore_stores_complete_activation_pending"
		}
		if stores.aborted {
			r.Status = "restore_stores_aborted_new_target_required"
		}
	}
	return r
}

func RunRestoreReservation(ctx context.Context, c configuration.Config, r configuration.RestoreRequest, mode string) (RestoreReservationResult, error) {
	if r.Validate() != nil || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoreReservationResult{}, failure("OFFLINE_SCOPE_RESTORE_TARGET_CONFIGURATION_REQUIRED")
	}
	if mode != "prepare-restore" && mode != "reconcile-restore-reservation" && mode != "abort-restore-reservation" {
		return RestoreReservationResult{}, failure("OFFLINE_SCOPE_RESTORE_RESERVATION_COMMAND_INVALID")
	}
	if ctx.Err() != nil {
		return RestoreReservationResult{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return RestoreReservationResult{}, e
	}
	defer l.close()
	if mode == "abort-restore-reservation" {
		return l.abortRestoreReservation(r)
	}
	source, e := InspectRestoreSource(ctx, c, r.RestoreSourceRequest)
	if e != nil {
		return l.restoreReservationResult(), e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return l.restoreReservationResult(), e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return l.restoreReservationResult(), e
	}
	compatibility, e := l.restoreReservationCompatibility(r, source)
	if e != nil {
		return l.restoreReservationResult(), e
	}
	target, e := l.inspectRestoreTarget(ctx, r.RestoreSourceRequest, source, client, worker, compatibility)
	if e != nil {
		return l.restoreReservationResult(), e
	}
	return l.reserveRestore(ctx, r, source, target, mode, func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }, compatibility)
}

func (l *locked) reserveRestore(ctx context.Context, r configuration.RestoreRequest, source RestoreSourceResult, target RestoreTargetResult, mode string, publish func(RestorePlan) error, compatibility ...*restoreCompatibilityProof) (RestoreReservationResult, error) {
	if r.Validate() != nil || (mode != "prepare-restore" && mode != "reconcile-restore-reservation") {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_COMMAND_INVALID")
	}
	if ctx.Err() != nil {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	state := restoreState(l.events)
	id, e := nonce()
	if e != nil {
		return l.restoreReservationResult(), e
	}
	attempt := id
	if state.record != nil {
		attempt = state.record.AttemptID
	}
	plan := RestorePlan{FormatVersion: 1, AttemptID: attempt, Request: r, Source: source, Target: target, RequiredStages: restoreRequiredStages()}
	if len(compatibility) > 1 {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_BINDING_REQUIRED")
	}
	if len(compatibility) == 1 {
		plan.Compatibility = compatibility[0]
	}
	record := RestoreRecord{AttemptID: attempt, PlanSHA256: wire.Digest(encode(plan)), BindingSHA256: target.BindingSHA256}
	if !l.validRestorePlan(plan) {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_PLAN_CONTEXT_INVALID")
	}
	if state.record != nil {
		if *state.record != record || state.aborted {
			return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_CONTEXT_CHANGED")
		}
		if state.pending == "" && state.reserved {
			return l.restoreReservationResult(), nil
		}
		if mode != "reconcile-restore-reservation" || state.action != "restore_reserve" {
			return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_RECONCILIATION_REQUIRED")
		}
	} else if mode != "prepare-restore" {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_NOT_RECORDED")
	}
	if e = admitProcessJournal(l, 3); e != nil {
		return l.restoreReservationResult(), e
	}
	planBytes := int64(len(encode(plan)))
	if planBytes > l.config.Verification.MaximumMetadataBytes {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_PLAN_METADATA_LIMIT")
	}
	if backupDiskAdmission(filepath.Join(l.config.ScopeRoot, restorePlanFile), planBytes) != nil {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_DISK_HEADROOM_REQUIRED")
	}
	action, reference := "restore_reserve", ""
	if state.record == nil {
		if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", Restore: &record}); e != nil {
			return l.restoreReservationResult(), e
		}
	} else {
		action = "restore_reconcile_reservation"
		reference = state.pending
	}
	raw, e := regular(l.root, restorePlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		if _, exists := l.root.Lstat(restorePlanFile); os.IsNotExist(exists) {
			e = publish(plan)
		}
		if e == nil {
			raw, e = regular(l.root, restorePlanFile, l.config.Verification.MaximumMetadataBytes)
		}
	}
	if e == nil && !bytes.Equal(raw, encode(plan)) {
		e = failure("OFFLINE_SCOPE_RESTORE_PLAN_CHANGED")
	}
	if e == nil {
		e = l.syncRestorePlan(raw)
	}
	if ctx.Err() != nil {
		e = failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	outcome, code := "completed", ""
	if e != nil {
		outcome = "uncertain"
		if action == "restore_reconcile_reservation" {
			outcome = "unresolved"
		}
		code = "OFFLINE_SCOPE_RESTORE_RESERVATION_UNRESOLVED"
	}
	if je := l.appendEvent(Event{OperationID: id, Action: action, ReferenceOperationID: reference, Outcome: outcome, Code: code, Restore: &record}); je != nil {
		return l.restoreReservationResult(), je
	}
	return l.restoreReservationResult(), e
}

func (l *locked) syncRestorePlan(expected []byte) error {
	return l.syncNamedRestorePlan(restorePlanFile, expected)
}

func (l *locked) syncNamedRestorePlan(name string, expected []byte) error {
	if name != restorePlanFile && name != restoreStoresPlanFile && name != controlReplacementPlanFile && name != cumulativeUpgradePlanFile && !validControlReplacementPlanName(name) && !validTLSProjectionPlanName(name) {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_NAME_INVALID")
	}
	before, e := l.root.Lstat(name)
	if e != nil {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_UNAVAILABLE")
	}
	f, e := l.root.OpenFile(name, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_SYNC_UNRESOLVED")
	}
	opened, se := f.Stat()
	if se != nil || !os.SameFile(before, opened) {
		f.Close()
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_CHANGED")
	}
	se = f.Sync()
	ce := f.Close()
	if se != nil || ce != nil {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_SYNC_UNRESOLVED")
	}
	raw, e := regular(l.root, name, l.config.Verification.MaximumMetadataBytes)
	after, ae := l.root.Lstat(name)
	if e != nil || ae != nil || !os.SameFile(before, after) || !bytes.Equal(raw, expected) {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_CHANGED")
	}
	if syncBackupDirectory(l.root, ".") != nil {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_SYNC_UNRESOLVED")
	}
	return nil
}

func validRestorePlan(p RestorePlan, m Marker, events []Event) bool {
	input, outcome, _ := inputsState(events)
	runtime := runtimeState(events)
	if p.FormatVersion != 1 || !identityPattern.MatchString(p.AttemptID) || p.Request.Validate() != nil || restoreTargetIdentityAuthorized(m, p.Source, p.Compatibility) != nil || p.Source.ManifestSHA256 != p.Request.ManifestSHA256 || p.Source.RequestSHA256 != wire.Digest(encode(p.Request.RestoreSourceRequest)) || !wire.ValidSHA256(p.Source.PlanSHA256) || p.Target.BackupManifestSHA256 != p.Request.ManifestSHA256 || p.Source.Status != "verified_independent_backup_source_not_restored" || !p.Source.Source.Valid() || p.Target.Status != "verified_restore_target_lineage_not_restored" || p.Target.SourceScopeID != p.Source.SourceScopeID || p.Target.TargetScopeID != m.ScopeID || !reflect.DeepEqual(p.RequiredStages, restoreRequiredStages()) || input == nil || outcome != "completed" || p.Target.DescriptorSHA256 != input.DescriptorSHA256 || !runtime.completed || runtime.record == nil || p.Target.RuntimeSHA256 != wire.Digest(encode(*runtime.record)) {
		return false
	}
	if (p.Request.CompatibilityFile == "") != (p.Compatibility == nil) || p.Compatibility == nil && p.Target.CompatibilitySHA256 != "" || p.Compatibility != nil && p.Target.CompatibilitySHA256 != wire.Digest(encode(p.Compatibility)) {
		return false
	}
	target := p.Target
	binding := target.BindingSHA256
	target.BindingSHA256 = ""
	return wire.ValidSHA256(binding) && wire.Digest(encode(target)) == binding
}

func (l *locked) verifyRestorePlan() error {
	s := restoreState(l.events)
	info, e := l.root.Lstat(restorePlanFile)
	if s.record == nil {
		if os.IsNotExist(e) {
			return nil
		}
		return failure("OFFLINE_SCOPE_RESTORE_UNRECORDED_PLAN_PRESERVED")
	}
	if os.IsNotExist(e) && (!s.reserved || s.aborted) {
		return nil
	}
	if e != nil || info.Size() > l.config.Verification.MaximumMetadataBytes {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_UNAVAILABLE")
	}
	raw, e := regular(l.root, restorePlanFile, l.config.Verification.MaximumMetadataBytes)
	var p RestorePlan
	if e != nil || wire.Digest(raw) != s.record.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || p.AttemptID != s.record.AttemptID || p.Target.BindingSHA256 != s.record.BindingSHA256 || !l.validRestorePlan(p) {
		return failure("OFFLINE_SCOPE_RESTORE_PLAN_CHANGED")
	}
	return l.verifyRecordedRestoreSQL(p)
}

func (l *locked) abortRestoreReservation(r configuration.RestoreRequest) (RestoreReservationResult, error) {
	if restoreSQLState(l.events).record != nil {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_EXECUTION_CLEANUP_REQUIRED")
	}
	s := restoreState(l.events)
	if s.record == nil {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_NOT_RECORDED")
	}
	p, e := l.readBoundRestorePlan()
	if e != nil || !reflect.DeepEqual(p.Request, r) {
		return l.restoreReservationResult(), failure("OFFLINE_SCOPE_RESTORE_RESERVATION_CONTEXT_CHANGED")
	}
	if s.aborted {
		return l.restoreReservationResult(), nil
	}
	if e = admitProcessJournal(l, 2); e != nil {
		return l.restoreReservationResult(), e
	}
	id, e := nonce()
	if e != nil {
		return l.restoreReservationResult(), e
	}
	event := Event{OperationID: id, Action: "restore_abort_reservation", ReferenceOperationID: s.pending, Outcome: "started", Restore: s.record}
	if s.action == "restore_abort_reservation" {
		event.OperationID = s.pending
		event.ReferenceOperationID = s.abortReference
	} else if e = l.appendEvent(event); e != nil {
		return l.restoreReservationResult(), e
	}
	event.Outcome = "completed"
	if e = l.appendEvent(event); e != nil {
		return l.restoreReservationResult(), e
	}
	return l.restoreReservationResult(), nil
}
