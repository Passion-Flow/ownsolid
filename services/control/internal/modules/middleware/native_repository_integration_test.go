//go:build repository_integration && middleware_native_integration

package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/Passion-Flow/ownsolid/deploy/offline/pkg/maintenance"
)

// This is the actual compiled PostgreSQL migration catalog. Requiring both M26
// owners before effects prevents an old schema16 package from qualifying M26.
func nativeCurrentSchema(t *testing.T) int {
	t.Helper()
	files, e := migrations.Files.ReadDir("postgresql")
	if e != nil {
		t.Fatal("compiled current schema catalog required")
	}
	version := 0
	instances, operations := false, false
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(file.Name(), "_")
		n, e := strconv.Atoi(prefix)
		if !ok || e != nil || n != version+1 {
			t.Fatal("exact ordered current catalog required")
		}
		version = n
		raw, e := migrations.Files.ReadFile("postgresql/" + file.Name())
		if e != nil {
			t.Fatal(e)
		}
		lower := strings.ToLower(string(raw))
		instances = instances || strings.Contains(lower, "middleware_instances")
		operations = operations || strings.Contains(lower, "middleware_operations")
	}
	if !instances || !operations {
		t.Fatal("actual M26 owning migrations must be integrated before native effects")
	}
	return version
}

type nativeEvidence struct {
	FormatVersion            int                  `json:"format_version"`
	Status                   string               `json:"status"`
	Engine                   string               `json:"engine"`
	ConfigurationFingerprint string               `json:"configuration_fingerprint"`
	SourceScopeID            string               `json:"source_scope_id"`
	TargetScopeID            string               `json:"target_scope_id"`
	SchemaVersion            int                  `json:"schema_version"`
	InstanceID               string               `json:"instance_id"`
	Backup                   Operation            `json:"backup"`
	Restore                  Operation            `json:"restore"`
	SourceProgress           maintenance.Progress `json:"source_progress"`
	TargetProgress           maintenance.Progress `json:"target_progress"`
	IndependentArchive       maintenance.Result   `json:"independent_archive"`
	Authority                string               `json:"authority"`
	Qualification            string               `json:"qualification"`
}

func sameNativeProjection(a, b any) bool {
	x, xe := json.Marshal(a)
	y, ye := json.Marshal(b)
	return xe == nil && ye == nil && bytes.Equal(x, y)
}

func writeNativeEvidence(t *testing.T, v nativeEvidence) {
	t.Helper()
	name := os.Getenv("PROJECT_ONE_MIDDLEWARE_NATIVE_EVIDENCE")
	if !pathValid(name) {
		t.Fatal("explicit private evidence output required")
	}
	root, e := os.OpenRoot(filepath.Dir(name))
	if e != nil {
		t.Fatal("owned evidence parent required")
	}
	defer root.Close()
	info, e := root.Stat(".")
	if e != nil || info.Mode() != os.ModeDir|0700 {
		t.Fatal("private owned evidence parent required")
	}
	f, e := root.OpenFile(filepath.Base(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("exclusive native evidence output required")
	}
	raw, e := json.MarshalIndent(v, "", "  ")
	raw = append(raw, '\n')
	if e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeError := f.Close()
	if e != nil || closeError != nil {
		t.Fatal("native evidence durability failed")
	}
	d, e := root.Open(".")
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if d.Sync() != nil {
		t.Fatal("native evidence directory durability failed")
	}
	t.Log("M26_NATIVE_RECEIPT", hash(raw))
}

func TestActualPairedMiddlewareNativeFlow(t *testing.T) {
	t.Run("actual_backup_and_fresh_isolated_restore", func(t *testing.T) {
		current := nativeCurrentSchema(t)
		configuration, e := LoadFiles(os.Getenv("PROJECT_ONE_MIDDLEWARE_NATIVE_CONFIGURATION"), "")
		if e != nil || len(configuration.Profiles) != 1 || configuration.Profiles[0].Kind != PlatformOwned || len(configuration.Profiles[0].RestoreTargets) != 1 {
			t.Fatal("exact reviewed protected native profile required")
		}
		profile := configuration.Profiles[0]
		target := profile.RestoreTargets[0]
		captured, e := Capture(profile)
		if e != nil {
			t.Fatal("actual native input capture required")
		}
		sourceConfiguration, e := maintenance.LoadBound(captured.operator, nil, profile.ExpectedScopeID)
		if e != nil {
			t.Fatal("actual bound source configuration required")
		}
		targetConfiguration, e := maintenance.LoadBound(captured.targets[target.Key], nil, target.ExpectedScopeID)
		if e != nil {
			t.Fatal("actual bound target configuration required")
		}
		if sourceConfiguration.ScopeRoot() == targetConfiguration.ScopeRoot() {
			t.Fatal("fresh isolated different target required")
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Duration(2*configuration.Policy.OperationSeconds+10*configuration.Policy.RequestSeconds)*time.Second)
		defer cancel()
		before, e := maintenance.ReadProgress(ctx, targetConfiguration)
		if e != nil || before.ScopeID != target.ExpectedScopeID || before.Reservation != nil || before.SQL != nil || before.Database != nil || before.Stores != nil {
			t.Fatal("actual fresh target original journal required")
		}
		// This helper uses an actual newly isolated selected control-plane database,
		// current native source tenant and actual signed License. It skips candidate
		// DDL because this profile requires real current activated owning migrations.
		p := pairedSetupWithConfiguration(t, &configuration)
		instance, _ := p.register(t)
		backup, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, instance.ID, OperationInput{Kind: "backup", Reason: "Actual synthetic M26 native consistent backup", IdempotencyKey: uuid.NewString(), Version: instance.Version}, uuid.NewString())
		if e != nil {
			t.Fatal("actual native backup admission failed", e)
		}
		if e = p.s.Pass(p.ctx); e != nil {
			t.Fatal("actual native backup owner failed", e)
		}
		backup, e = p.s.Operation(p.ctx, p.owner, p.f.Workspace, backup.ID)
		if e != nil || backup.State != "succeeded" {
			t.Fatal("actual backup not accepted; retain original journal and inspect exact intent", e)
		}
		accepted, archiveID, e := backupReceipt(backup)
		if e != nil || archiveID != backup.ID || accepted.Stages[1].Source == nil || accepted.Stages[1].Source.SchemaVersion != current {
			t.Fatal("current exact full schema backup evidence required")
		}
		wantStores := []string{"datasets", "models", "preparations", "training"}
		if !reflect.DeepEqual(accepted.Stages[1].Stores, wantStores) {
			t.Fatal("all four actual stores required; no omitted-store qualification")
		}
		budget, e := maintenance.LoadBackupRequest(captured.backupRequest, nil)
		if e != nil {
			t.Fatal(e)
		}
		source := maintenance.RestoreSourceRequest{FormatVersion: 1, BackupRoot: filepath.Join(profile.ArtifactRoot, backup.ID), ManifestSHA256: accepted.Stages[0].ManifestSHA256, MaximumBytes: budget.MaximumBytes, MaximumEntries: budget.MaximumEntries}
		independent, e := maintenance.Run(ctx, sourceConfiguration, maintenance.InspectRestoreSource, maintenance.Request{Source: &source})
		if e != nil || independent.Source == nil || independent.Source.SchemaVersion != current || !reflect.DeepEqual(independent.Stores, wantStores) || independent.ManifestSHA256 != accepted.Stages[0].ManifestSHA256 {
			t.Fatal("independent actual full archive inspection failed", e)
		}
		input := OperationInput{Kind: "isolated_restore", Reason: "Actual synthetic M26 isolated native restore", IdempotencyKey: uuid.NewString(), Version: instance.Version, BackupID: backup.ID, TargetKey: target.Key, ConfirmIsolated: false}
		denied, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, instance.ID, input, uuid.NewString())
		if e == nil || e.Error() != "MIDDLEWARE_ISOLATED_CONFIRMATION_REQUIRED" || denied.ID != "" {
			t.Fatal("false restore did not refuse with zero output")
		}
		still, e := maintenance.ReadProgress(ctx, targetConfiguration)
		if e != nil || !reflect.DeepEqual(still, before) {
			t.Fatal("false restore touched native target")
		}
		input.ConfirmIsolated = true
		restored, e := p.s.Enqueue(p.ctx, p.admin, p.f.Workspace, instance.ID, input, uuid.NewString())
		if e != nil {
			t.Fatal("actual isolated restore admission failed", e)
		}
		if e = p.s.Pass(p.ctx); e != nil {
			t.Fatal("actual isolated restore owner failed", e)
		}
		restored, e = p.s.Operation(p.ctx, p.admin, p.f.Workspace, restored.ID)
		if e != nil || restored.State != "succeeded" {
			t.Fatal("actual restore not accepted; preserve original target/journal/unknown intent", e)
		}
		var evidence executionEvidence
		if json.Unmarshal(restored.Result, &evidence) != nil || evidence.Kind != "isolated_restore" || len(evidence.Stages) != 5 {
			t.Fatal("exact original restore stages required")
		}
		progress, e := maintenance.ReadProgress(ctx, targetConfiguration)
		if e != nil || progress.SQL == nil || progress.Database == nil || progress.Stores == nil || progress.SQL.Pending || progress.Database.Pending || progress.Stores.Pending || progress.SQL.ProofSHA256 != evidence.Stages[2].ProofSHA256 || progress.Database.ProofSHA256 != evidence.Stages[3].ProofSHA256 || progress.Stores.ProofSHA256 != evidence.Stages[4].ProofSHA256 {
			t.Fatal("independent original SQL/role/authority/store proof readback failed", e)
		}
		sourceProgress, e := maintenance.ReadProgress(ctx, sourceConfiguration)
		if e != nil || sourceProgress.Backup == nil || sourceProgress.Backup.Pending || sourceProgress.Backup.ManifestSHA256 != accepted.Stages[0].ManifestSHA256 {
			t.Fatal("actual original backup journal readback failed", e)
		}
		for _, job := range []Operation{backup, restored} {
			if p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.operation_succeeded' AND target_id=$1", "SELECT count(*) FROM audit_events WHERE action='middleware.operation_succeeded' AND target_id=?", job.ID) != 1 {
				t.Fatal("actual terminal audit absent or repeated")
			}
		}
		reopened, e := New(p.accounts, &workspaces.Service{Identity: p.accounts}, configuration)
		if e != nil || reopened.VerifyStored(p.ctx) != nil {
			t.Fatal("actual selected reopened native history integrity failed")
		}
		if !captured.Matches() {
			t.Fatal("native protected input drift")
		}
		writeNativeEvidence(t, nativeEvidence{FormatVersion: 1, Status: "passed_actual_M26_native_flow_original_proofs_retained", Engine: string(p.f.Store.Dialect()), ConfigurationFingerprint: captured.fingerprint, SourceScopeID: profile.ExpectedScopeID, TargetScopeID: target.ExpectedScopeID, SchemaVersion: current, InstanceID: instance.ID, Backup: backup, Restore: restored, SourceProgress: sourceProgress, TargetProgress: progress, IndependentArchive: independent, Authority: "original_actual_M17_runtime_role_and_restore_invalidation_stage", Qualification: "actual_selected_control_plane_and_original_PostgreSQL_native_backup_isolated_restore_four_stores; application_activation_browser_customer_arbitrary_database_and_native_target_pending"})
	})
}

// This separate actual root is an explicit readback of the original retained
// proof, never a fresh flow or another control-plane SQL qualification.
func TestActualMiddlewareNativeIndependentReadback(t *testing.T) {
	raw, e := readPrivate(os.Getenv("PROJECT_ONE_MIDDLEWARE_NATIVE_EVIDENCE"))
	if e != nil {
		t.Fatal("exact retained original native receipt required")
	}
	var evidence nativeEvidence
	if json.Unmarshal(raw, &evidence) != nil || evidence.FormatVersion != 1 || evidence.Status != "passed_actual_M26_native_flow_original_proofs_retained" || evidence.Engine != os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") || evidence.SchemaVersion != nativeCurrentSchema(t) {
		t.Fatal("exact original native flow identity required")
	}
	configuration, e := LoadFiles(os.Getenv("PROJECT_ONE_MIDDLEWARE_NATIVE_CONFIGURATION"), "")
	if e != nil || len(configuration.Profiles) != 1 || len(configuration.Profiles[0].RestoreTargets) != 1 {
		t.Fatal("exact original protected native configuration required")
	}
	profile := configuration.Profiles[0]
	target := profile.RestoreTargets[0]
	captured, e := Capture(profile)
	if e != nil || captured.fingerprint != evidence.ConfigurationFingerprint || profile.ExpectedScopeID != evidence.SourceScopeID || target.ExpectedScopeID != evidence.TargetScopeID {
		t.Fatal("original protected native input drift")
	}
	sourceConfiguration, e := maintenance.LoadBound(captured.operator, nil, evidence.SourceScopeID)
	if e != nil {
		t.Fatal(e)
	}
	targetConfiguration, e := maintenance.LoadBound(captured.targets[target.Key], nil, evidence.TargetScopeID)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(configuration.Policy.OperationSeconds)*time.Second)
	defer cancel()
	sourceProgress, e := maintenance.ReadProgress(ctx, sourceConfiguration)
	if e != nil || !sameNativeProjection(sourceProgress, evidence.SourceProgress) {
		t.Fatal("original source journal changed", e)
	}
	targetProgress, e := maintenance.ReadProgress(ctx, targetConfiguration)
	if e != nil || !sameNativeProjection(targetProgress, evidence.TargetProgress) {
		t.Fatal("original target journal/proof changed", e)
	}
	accepted, root, e := backupReceipt(evidence.Backup)
	if e != nil || root != evidence.Backup.ID {
		t.Fatal("original accepted archive receipt invalid")
	}
	budget, e := maintenance.LoadBackupRequest(captured.backupRequest, nil)
	if e != nil {
		t.Fatal(e)
	}
	request := maintenance.RestoreSourceRequest{FormatVersion: 1, BackupRoot: filepath.Join(profile.ArtifactRoot, root), ManifestSHA256: accepted.Stages[0].ManifestSHA256, MaximumBytes: budget.MaximumBytes, MaximumEntries: budget.MaximumEntries}
	archive, e := maintenance.Run(ctx, sourceConfiguration, maintenance.InspectRestoreSource, maintenance.Request{Source: &request})
	if e != nil || archive.EvidenceSHA256 != evidence.IndependentArchive.EvidenceSHA256 || !reflect.DeepEqual(archive.Stores, []string{"datasets", "models", "preparations", "training"}) {
		t.Fatal("actual independent archive/readback no longer matches", e)
	}
	if !captured.Matches() {
		t.Fatal("original native inputs changed during independent readback")
	}
	t.Log("M26_NATIVE_INDEPENDENT_RECEIPT", hash(raw))
}
