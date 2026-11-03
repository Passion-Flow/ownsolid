package scope

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreSourceResult struct {
	Status                string                                `json:"status"`
	RequestSHA256         string                                `json:"request_sha256"`
	ManifestSHA256        string                                `json:"manifest_sha256"`
	PlanSHA256            string                                `json:"plan_sha256"`
	SourceScopeID         string                                `json:"source_scope_id"`
	SourceAttemptID       string                                `json:"source_attempt_id"`
	ReleaseID             string                                `json:"release_id"`
	Profile               string                                `json:"profile"`
	Source                engine.PostgresBackupSource           `json:"source"`
	Stores                []string                              `json:"stores"`
	Files                 int                                   `json:"files"`
	Bytes                 int64                                 `json:"bytes"`
	Qualification         string                                `json:"qualification"`
	ReleaseManifestSHA256 string                                `json:"release_manifest_sha256,omitempty"`
	NativePostgreSQL      *wire.NativePostgreSQLBackupReference `json:"native_postgresql,omitempty"`
}

// No Engine client, scope lock, journal mutation or source configuration values
// are needed to establish this independent immutable backup input boundary.
func InspectRestoreSource(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest) (RestoreSourceResult, error) {
	result := RestoreSourceResult{}
	if e := r.Validate(); e != nil {
		return result, e
	}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if !mounted(c.Verification.MountRoot) || !contained(c.Verification.MountRoot, r.BackupRoot) || !canonical(r.BackupRoot) {
		return result, failure("OFFLINE_SCOPE_RESTORE_MOUNTED_BACKUP_REQUIRED")
	}
	for _, target := range []string{c.ScopeRoot, c.DestinationRoot} {
		if target != "" && (target == r.BackupRoot || contained(target, r.BackupRoot) || contained(r.BackupRoot, target)) {
			return result, failure("OFFLINE_SCOPE_RESTORE_SOURCE_TARGET_OVERLAP")
		}
	}
	if native, handled, e := inspectMVPNativeRestoreSource(ctx, c, r); handled || e != nil {
		return native, e
	}
	root, e := openBackupDirectory(r.BackupRoot)
	if e != nil {
		return result, e
	}
	defer root.Close()
	manifest, e := regular(root, backupManifestFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(manifest) != r.ManifestSHA256 {
		return result, failure("OFFLINE_SCOPE_RESTORE_MANIFEST_DIGEST_MISMATCH")
	}
	files, bytes, e := admitRestoreSourceTree(ctx, root, r)
	if e != nil {
		return result, e
	}
	var p BackupPlan
	raw, e := regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.StrictJSON(raw, &p) != nil || !validRestoreSourcePlan(p) {
		return result, failure("OFFLINE_SCOPE_RESTORE_SOURCE_PLAN_INVALID")
	}
	if e = readBackupReleasePolicy(root, p, c.Verification.MaximumMetadataBytes); e != nil {
		return result, e
	}
	// Preserve the canonical original plan. Never let its embedded limits grant
	// more work than the independent current reader has explicitly admitted.
	if p.Request.MaximumBytes > r.MaximumBytes || p.Request.MaximumEntries > r.MaximumEntries {
		return result, failure("OFFLINE_SCOPE_RESTORE_SOURCE_DECLARED_LIMIT_EXCEEDS_READER")
	}
	m, hash, e := inspectBackupFiles(ctx, root, p)
	if e != nil {
		return result, e
	}
	if hash.SHA256 != r.ManifestSHA256 || m.PlanSHA256 != wire.Digest(raw) {
		return result, failure("OFFLINE_SCOPE_RESTORE_MANIFEST_CHANGED")
	}
	if !m.Source.Valid() || m.Source.Database != p.Jobs["postgres"].Postgres.Database || m.Source.Operator != p.Jobs["postgres"].Postgres.User {
		return result, failure("OFFLINE_SCOPE_RESTORE_SOURCE_METADATA_INVALID")
	}
	return RestoreSourceResult{Status: "verified_independent_backup_source_not_restored", RequestSHA256: wire.Digest(encode(r)), ManifestSHA256: r.ManifestSHA256, PlanSHA256: m.PlanSHA256, SourceScopeID: m.ScopeID, SourceAttemptID: m.AttemptID, ReleaseID: m.ReleaseID, Profile: m.Profile, Source: m.Source, Stores: m.Stores, Files: files, Bytes: bytes, Qualification: "readonly_complete_backup_inventory_only_not_restored_runtime_archive_import_or_target_qualification"}, nil
}

func admitRestoreSourceTree(ctx context.Context, root *os.Root, r configuration.RestoreSourceRequest) (int, int64, error) {
	entries, files, bytes := 0, 0, int64(0)
	e := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, e error) error {
		if ctx.Err() != nil {
			return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		if e != nil {
			return failure("OFFLINE_SCOPE_RESTORE_SOURCE_TREE_UNAVAILABLE")
		}
		entries++
		if entries > r.MaximumEntries {
			return failure("OFFLINE_SCOPE_RESTORE_SOURCE_ENTRY_LIMIT")
		}
		info, e := entry.Info()
		if e != nil || !owned(info) {
			return failure("OFFLINE_SCOPE_RESTORE_PRIVATE_SOURCE_REQUIRED")
		}
		if entry.IsDir() && info.Mode() == os.ModeDir|0700 {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode() != 0600 || !ok || st.Nlink != 1 || info.Size() < 1 {
			return failure("OFFLINE_SCOPE_RESTORE_PRIVATE_SOURCE_REQUIRED")
		}
		if info.Size() > r.MaximumBytes-bytes {
			return failure("OFFLINE_SCOPE_RESTORE_SOURCE_BYTE_LIMIT")
		}
		bytes += info.Size()
		files++
		return nil
	})
	return files, bytes, e
}

func validRestoreSourcePlan(p BackupPlan) bool {
	if hasRecordedReleasePolicy(p.Profile) {
		if len(p.Jobs) != 5 {
			return false
		}
		for _, role := range []string{"models", "datasets", "training", "preparations"} {
			if p.Jobs[role].Store == nil {
				return false
			}
		}
		if p.ReleaseViewBinding == nil || p.ReleaseViewBinding.Validate() != nil || p.ReleaseViewBinding.ScopeID != p.ScopeID || p.ReleaseViewBinding.ConfigurationSHA256 != p.ConfigurationSHA256 {
			return false
		}
	} else if p.ReleaseViewBinding != nil {
		return false
	}

	if p.FormatVersion != 1 || !identityPattern.MatchString(p.ScopeID) || !identityPattern.MatchString(p.AttemptID) || p.ReleaseID == "" || len(p.ReleaseID) > 128 || p.Profile == "" || len(p.Profile) > 128 || !wire.ValidSHA256(p.ConfigurationSHA256) || !wire.ValidSHA256(p.ManifestSHA256) || !wire.ValidSHA256(p.DescriptorSHA256) || !wire.ValidSHA256(p.RuntimeSHA256) || p.Request.Validate() != nil || len(p.Jobs) < 2 || len(p.Jobs) > 5 || len(p.ConfigurationFiles) < 1 || len(p.ConfigurationFiles) > 1024 {
		return false
	}
	// Source admission is provenance only, never authority on the new target.
	if p.RestoredAdmission != nil && (!validBackupAdmissionShape(p.RestoredAdmission) || p.RuntimeSHA256 != p.RestoredAdmission.Dependency.EffectiveRuntimeSHA256) {
		return false
	}
	if p.RestoredAdmission == nil && p.EffectiveControlPayloadSHA256 != "" {
		return false
	}
	storeKinds := map[string]string{"models": engine.ModelFilesExport, "datasets": engine.DatasetFilesExport, "training": engine.TrainingFilesExport, "preparations": engine.PreparationFilesExport}
	for role, job := range p.Jobs {
		if role == "postgres" {
			if job.Postgres == nil || job.Store != nil || job.Postgres.Restore != nil || job.Postgres.Owner.ScopeID != p.ScopeID || job.Postgres.Owner.OperationID != p.AttemptID || job.Postgres.Owner.Role != "database-"+engine.PostgresBackupKind {
				return false
			}
		} else if kind := storeKinds[role]; kind == "" || job.Postgres != nil || job.Store == nil || job.Store.Kind != kind || job.Store.Owner.ScopeID != p.ScopeID || job.Store.Owner.OperationID != p.AttemptID || job.Store.Owner.Role != "database-"+kind {
			return false
		}
	}
	if p.Jobs["postgres"].Postgres == nil {
		return false
	}
	seen := map[string]bool{}
	control := []InputFile{}
	for _, f := range p.ConfigurationFiles {
		if !strings.HasSuffix(f.Role, "-config") || roleServices[f.Role] == "" || !wire.SafePath(f.File.Name) || filepath.Base(f.File.Name) != f.File.Name || f.File.Bytes < 1 || !wire.ValidSHA256(f.File.SHA256) || seen[f.Role+"/"+f.File.Name] {
			return false
		}
		seen[f.Role+"/"+f.File.Name] = true
		if f.Role == "control-config" {
			control = append(control, f.File)
		}
	}
	if p.RestoredAdmission != nil && (!wire.ValidSHA256(p.EffectiveControlPayloadSHA256) || wire.Digest(encode(control)) != p.EffectiveControlPayloadSHA256) {
		return false
	}
	return true
}
