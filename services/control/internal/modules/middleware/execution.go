package middleware

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/deploy/offline/pkg/maintenance"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type executionEvidence struct {
	FormatVersion       int                  `json:"format_version"`
	Kind                string               `json:"kind"`
	Observation         *Observation         `json:"observation,omitempty"`
	MySQLObservation    *MySQLObservation    `json:"mysql_observation,omitempty"`
	Stages              []maintenance.Result `json:"stages,omitempty"`
	OriginalOperationID string               `json:"original_operation_id,omitempty"`
}

func completedBackupResult(r maintenance.Result) bool {
	return (r.Command == maintenance.Backup || r.Command == maintenance.InspectBackup || r.Command == maintenance.ReconcileBackup) && !r.Pending && r.Status == "complete_selected_profile_backup_restore_pending" && scopeIdentity.MatchString(r.ScopeID) && wire.ValidSHA256(r.ManifestSHA256) && wire.ValidSHA256(r.EvidenceSHA256) && completedNativeBackupReference(r)
}

// Source inspection completes only the independent inventory check. Its exact
// original status must never be promoted to SQL restore or target qualification.
func completedSourceResult(r maintenance.Result, manifest string) bool {
	return r.Command == maintenance.InspectRestoreSource && !r.Pending && r.Status == "verified_independent_backup_source_not_restored" && scopeIdentity.MatchString(r.SourceScopeID) && r.Source != nil && r.Source.Valid() && wire.ValidSHA256(manifest) && r.ManifestSHA256 == manifest && wire.ValidSHA256(r.PlanSHA256) && wire.ValidSHA256(r.EvidenceSHA256) && completedNativeSourceReference(r)
}

func backupReceipt(v Operation) (executionEvidence, string, error) {
	var e executionEvidence
	if v.State != "succeeded" || v.Kind != "backup" && v.Kind != "reconcile" || wire.StrictJSON(v.Result, &e) != nil || e.FormatVersion != 1 || e.Kind != "backup" || e.Observation != nil || e.MySQLObservation != nil || len(e.Stages) != 2 || !wire.ValidSHA256(e.Stages[0].ManifestSHA256) {
		return executionEvidence{}, "", fail("MIDDLEWARE_COMPLETED_BACKUP_REQUIRED")
	}
	first, second := e.Stages[0], e.Stages[1]
	if !completedBackupResult(first) || !completedSourceResult(second, first.ManifestSHA256) || second.SourceScopeID != first.ScopeID || !sameOriginalNativeBackup(first, second) {
		return executionEvidence{}, "", fail("MIDDLEWARE_BACKUP_EVIDENCE_INVALID")
	}
	root := v.ID
	if v.Kind == "reconcile" {
		if !validID(e.OriginalOperationID) {
			return executionEvidence{}, "", fail("MIDDLEWARE_BACKUP_EVIDENCE_INVALID")
		}
		root = e.OriginalOperationID
	} else if e.OriginalOperationID != "" {
		return executionEvidence{}, "", fail("MIDDLEWARE_BACKUP_EVIDENCE_INVALID")
	}
	return e, root, nil
}

// The original M17 reason is also its exact request identity. Binding the actual
// M26 operation UUID prevents a different control intent adopting that journal.
func boundReason(id, reason string) (string, error) {
	bound := "M26 operation " + id + ": " + reason
	if !validID(id) || !workspaces.ValidReason(bound) {
		return "", fail("MIDDLEWARE_REASON_TOO_LONG")
	}
	return bound, nil
}

func (s *Service) original(ctx context.Context, c *claimed) (*claimed, error) {
	if c.Kind != "reconcile" {
		return c, nil
	}
	store, e := s.repository()
	if e != nil {
		return nil, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	var out *claimed
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, c.actor, c.WorkspaceID, true)
		if e != nil {
			return e
		}
		out, e = scanClaim(q.QueryRow(ctx, statement(q, "SELECT "+workPG+" FROM middleware_operations WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE", "SELECT "+workMy+" FROM middleware_operations WHERE tenant_id=? AND workspace_id=? AND id=? FOR SHARE", w.TenantID, w.ID, c.input.OriginalOperationID)))
		if e != nil {
			return e
		}
		if out.InstanceID != c.InstanceID || out.Kind != "backup" && out.Kind != "isolated_restore" || out.State != "unknown" && out.State != "failed" || out.fingerprint != c.fingerprint || out.instanceVersion != c.instanceVersion {
			return fail("MIDDLEWARE_ORIGINAL_UNCERTAIN_OPERATION_REQUIRED")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

func (s *Service) execute(ctx context.Context, c *claimed) ([]byte, error) {
	v, e := s.Get(ctx, c.actor, c.WorkspaceID, c.InstanceID)
	if e != nil {
		return nil, e
	}
	if v.State != "active" || v.Version != c.instanceVersion || v.Fingerprint != c.fingerprint {
		return nil, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	p, e := s.profile(v.ProfileKey)
	if e != nil {
		return nil, e
	}
	captured, e := Capture(p)
	if e != nil {
		return nil, e
	}
	if captured.fingerprint != c.fingerprint {
		return nil, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	if c.Kind == "health" {
		if captured.mysql != nil {
			observation, e := ProbeMySQL(ctx, captured, s.configuration.Policy)
			if e != nil {
				return nil, e
			}
			return json.Marshal(executionEvidence{FormatVersion: 1, Kind: "health", MySQLObservation: &observation})
		}
		observation, e := Probe(ctx, captured, s.configuration.Policy)
		if e != nil {
			return nil, e
		}
		return json.Marshal(executionEvidence{FormatVersion: 1, Kind: "health", Observation: &observation})
	}
	if p.Kind != PlatformOwned {
		return nil, fail("MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
	}
	origin, e := s.original(ctx, c)
	if e != nil {
		return nil, e
	}
	reason, e := boundReason(origin.ID, origin.input.Reason)
	if e != nil {
		return nil, e
	}
	budget, e := maintenance.LoadBackupRequest(captured.backupRequest, nil)
	if e != nil {
		return nil, e
	}
	if budget.DestinationRoot != p.ArtifactRoot {
		return nil, fail("MIDDLEWARE_ARTIFACT_ROOT_MISMATCH")
	}
	configuration, e := maintenance.LoadBound(captured.operator, nil, p.ExpectedScopeID)
	if e != nil {
		return nil, e
	}
	budget.Reason = reason
	var stages []maintenance.Result
	if origin.Kind == "backup" {
		budget.DestinationRoot = filepath.Join(p.ArtifactRoot, origin.ID)
		stages, e = s.backup(ctx, c, origin, captured, configuration, budget)
	} else {
		stages, e = s.restore(ctx, c, origin, captured, configuration, budget, reason)
	}
	if e != nil {
		return nil, e
	}
	if !captured.Matches() {
		return nil, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	evidence := executionEvidence{FormatVersion: 1, Kind: origin.Kind, Stages: stages}
	if c.Kind == "reconcile" {
		evidence.OriginalOperationID = origin.ID
	}
	return json.Marshal(evidence)
}

func (s *Service) backup(ctx context.Context, c, origin *claimed, input *CapturedInputs, configuration *maintenance.Configuration, request maintenance.BackupRequest) ([]maintenance.Result, error) {
	command := maintenance.Backup
	if c.Kind == "reconcile" {
		progress, e := maintenance.ReadProgress(ctx, configuration)
		if e != nil {
			return nil, e
		}
		if progress.Backup == nil {
			return nil, fail("MIDDLEWARE_ORIGINAL_OPERATOR_INTENT_REQUIRED")
		}
		if progress.Backup.Status == "complete_selected_profile_backup_restore_pending" {
			command = maintenance.InspectBackup
		} else {
			command = maintenance.ReconcileBackup
		}
	}
	result, e := maintenance.Run(ctx, configuration, command, maintenance.Request{Backup: &request})
	if e != nil {
		return nil, e
	}
	if !completedBackupResult(result) || result.Command != command || result.ScopeID != input.profile.ExpectedScopeID {
		return nil, fail("MIDDLEWARE_OPERATOR_RECONCILIATION_REQUIRED")
	}
	source := maintenance.RestoreSourceRequest{FormatVersion: 1, BackupRoot: request.DestinationRoot, ManifestSHA256: result.ManifestSHA256, MaximumBytes: request.MaximumBytes, MaximumEntries: request.MaximumEntries}
	verified, e := maintenance.Run(ctx, configuration, maintenance.InspectRestoreSource, maintenance.Request{Source: &source})
	if e != nil {
		return nil, e
	}
	if !completedSourceResult(verified, source.ManifestSHA256) || !sameOriginalNativeBackup(result, verified) {
		return nil, fail("MIDDLEWARE_OPERATOR_RECONCILIATION_REQUIRED")
	}
	if verified.SourceScopeID != input.profile.ExpectedScopeID || verified.Source == nil || verified.Source.TenantID != input.profile.ExpectedTenantID || verified.Source.Database != input.profile.Database || verified.Source.ServerVersionNumber/10000 != input.profile.ExpectedMajor {
		return nil, fail("MIDDLEWARE_PLATFORM_SCOPE_MISMATCH")
	}
	return []maintenance.Result{result, verified}, nil
}

func (s *Service) restore(ctx context.Context, c, origin *claimed, input *CapturedInputs, sourceConfiguration *maintenance.Configuration, budget maintenance.BackupRequest, reason string) ([]maintenance.Result, error) {
	targetKey := origin.input.TargetKey
	var target *RestoreTarget
	for i := range input.profile.RestoreTargets {
		t := &input.profile.RestoreTargets[i]
		if t.Key == targetKey {
			target = t
		}
	}
	if target == nil || !origin.input.ConfirmIsolated {
		return nil, fail("MIDDLEWARE_ISOLATED_CONFIRMATION_REQUIRED")
	}
	// A backup accepted by the original instance is resolved again under current
	// scope; its actual manifest hash is never supplied by a browser or guessed.
	accepted, e := s.Operation(ctx, c.actor, c.WorkspaceID, origin.input.BackupID)
	if e != nil {
		return nil, e
	}
	if accepted.InstanceID != c.InstanceID {
		return nil, fail("MIDDLEWARE_COMPLETED_BACKUP_REQUIRED")
	}
	evidence, root, e := backupReceipt(accepted)
	if e != nil {
		return nil, e
	}
	source := maintenance.RestoreSourceRequest{FormatVersion: 1, BackupRoot: filepath.Join(input.profile.ArtifactRoot, root), ManifestSHA256: evidence.Stages[0].ManifestSHA256, MaximumBytes: budget.MaximumBytes, MaximumEntries: budget.MaximumEntries}
	configuration, e := maintenance.LoadBound(input.targets[targetKey], nil, target.ExpectedScopeID)
	if e != nil {
		return nil, e
	}
	if configuration.ScopeRoot() == sourceConfiguration.ScopeRoot() {
		return nil, fail("MIDDLEWARE_ISOLATED_TARGET_INVALID")
	}
	progress, e := maintenance.ReadProgress(ctx, configuration)
	if e != nil {
		return nil, e
	}
	if c.Kind != "reconcile" && progress.Reservation != nil {
		return nil, fail("MIDDLEWARE_FRESH_TARGET_REQUIRED")
	}
	if c.Kind == "reconcile" && progress.Reservation == nil {
		return nil, fail("MIDDLEWARE_ORIGINAL_OPERATOR_INTENT_REQUIRED")
	}
	verified, e := maintenance.Run(ctx, configuration, maintenance.InspectRestoreSource, maintenance.Request{Source: &source})
	if e != nil {
		return nil, e
	}
	if !completedSourceResult(verified, source.ManifestSHA256) || !sameOriginalNativeBackup(evidence.Stages[1], verified) {
		return nil, fail("MIDDLEWARE_OPERATOR_RECONCILIATION_REQUIRED")
	}
	if verified.SourceScopeID != input.profile.ExpectedScopeID || verified.Source == nil || verified.Source.TenantID != input.profile.ExpectedTenantID || verified.Source.Database != input.profile.Database || verified.Source.ServerVersionNumber/10000 != input.profile.ExpectedMajor {
		return nil, fail("MIDDLEWARE_PLATFORM_SCOPE_MISMATCH")
	}
	if requiresNativeReference(verified) {
		// The original MVP fixed full-native action/read-only stage contract is
		// still an upstream implementation dependency. These legacy split calls
		// must never import SQL or invalidate authority before registry import.
		return nil, fail("MIDDLEWARE_OPERATOR_RECONCILIATION_REQUIRED")
	}
	stages := []maintenance.Result{verified}
	request := maintenance.RestoreRequest{RestoreSourceRequest: source, Reason: reason}
	for _, stage := range []struct {
		initial, reconcile maintenance.Command
		recorded           bool
		status             string
	}{
		{maintenance.ReserveRestore, maintenance.ReconcileReservation, progress.Reservation != nil, ""},
		{maintenance.RestoreSQL, maintenance.ReconcileSQL, progress.SQL != nil, "sql_restored_other_qualification_pending"},
		{maintenance.VerifyRestoredDatabase, maintenance.ReconcileDatabase, progress.Database != nil, "restored_database_qualified_stores_activation_pending"},
		{maintenance.RestoreStores, maintenance.ReconcileStores, progress.Stores != nil, "selected_stores_verified_activation_pending"},
	} {
		command := stage.initial
		if c.Kind == "reconcile" && stage.recorded {
			command = stage.reconcile
		}
		result, e := maintenance.Run(ctx, configuration, command, maintenance.Request{Restore: &request})
		if e != nil {
			return nil, e
		}
		if result.ScopeID != target.ExpectedScopeID {
			return nil, fail("MIDDLEWARE_PLATFORM_SCOPE_MISMATCH")
		}
		if stage.initial == maintenance.ReserveRestore {
			if !wire.ValidSHA256(result.PlanSHA256) {
				return nil, fail("MIDDLEWARE_OPERATOR_RECONCILIATION_REQUIRED")
			}
		} else if result.Pending || result.Status != stage.status || !wire.ValidSHA256(result.ProofSHA256) {
			return nil, fail("MIDDLEWARE_OPERATOR_RECONCILIATION_REQUIRED")
		}
		stages = append(stages, result)
	}
	return stages, nil
}
