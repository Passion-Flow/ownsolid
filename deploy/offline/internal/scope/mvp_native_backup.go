package scope

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Reservation retains the original owning inputs and signed source bytes. It
// neither starts a task nor creates the native child: the original native writer
// must exclusively create that child inside its own actual artifact volume.
func reserveMVPNativeBackupFiles(p mvpNativeBackupPlan, files map[string][]engine.ConfigurationFile, release, signature, descriptor []byte) error {
	if !validMVPNativeBackupPlan(p) || wire.Digest(release) != p.ReleaseManifestSHA256 || len(signature) == 0 || wire.Digest(descriptor) != p.DescriptorSHA256 || !privateDirectory(filepath.Dir(p.Request.DestinationRoot)) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_RESERVATION_INVALID")
	}
	expected := map[string]InputFile{}
	for _, f := range p.ConfigurationFiles {
		expected[f.Role+"/"+f.File.Name] = f.File
	}
	count := 0
	for role, items := range files {
		for _, f := range items {
			want, ok := expected[role+"/"+f.Name]
			if !ok || want.Bytes != int64(len(f.Contents)) || want.SHA256 != wire.Digest(f.Contents) {
				return failure("OFFLINE_SCOPE_NATIVE_BACKUP_SOURCE_CONFIGURATION_CHANGED")
			}
			count++
		}
	}
	if count != len(expected) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_SOURCE_CONFIGURATION_CHANGED")
	}
	if e := os.Mkdir(p.Request.DestinationRoot, 0700); e != nil {
		return failure("OFFLINE_SCOPE_BACKUP_DESTINATION_OCCUPIED")
	}
	parent, e := openPrivateDirectory(filepath.Dir(p.Request.DestinationRoot))
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = syncBackupDirectory(parent, "."); e != nil {
		return e
	}
	root, e := openBackupDirectory(p.Request.DestinationRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	for name, raw := range map[string][]byte{backupPlanFile: encode(p), mvpNativeReleaseFile: release, mvpNativeSignatureFile: signature, mvpNativeSourceDescriptorFile: descriptor} {
		if e = writeBackupFile(root, name, raw); e != nil {
			return e
		}
	}
	if e = root.Mkdir("configuration", 0700); e != nil {
		return e
	}
	if e = syncBackupDirectory(root, "."); e != nil {
		return e
	}
	roles := []string{}
	for role, items := range files {
		if len(items) > 0 {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	for _, role := range roles {
		directory := "configuration/" + role
		if e = root.Mkdir(directory, 0700); e != nil {
			return e
		}
		if e = syncBackupDirectory(root, "configuration"); e != nil {
			return e
		}
		for _, f := range files[role] {
			if e = writeBackupFile(root, directory+"/"+f.Name, f.Contents); e != nil {
				return e
			}
		}
	}
	return syncBackupDirectory(root, ".")
}

func verifyMVPNativeBackupReservation(ctx context.Context, root *os.Root, p mvpNativeBackupPlan, c configuration.Config) error {
	if ctx == nil || ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	raw, e := regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
	if e != nil || !validMVPNativeBackupPlan(p) || wire.Digest(raw) != wire.Digest(encode(p)) {
		return failure("OFFLINE_SCOPE_BACKUP_PLAN_CHANGED")
	}
	if e = verifyMVPNativeSourceRelease(root, c, p); e != nil {
		return e
	}
	for _, f := range p.ConfigurationFiles {
		raw, e := regular(root, "configuration/"+f.Role+"/"+f.File.Name, c.Verification.MaximumMetadataBytes)
		if e != nil || int64(len(raw)) != f.File.Bytes || wire.Digest(raw) != f.File.SHA256 {
			return failure("OFFLINE_SCOPE_BACKUP_CONFIGURATION_CHANGED")
		}
		if ctx.Err() != nil {
			return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
	}
	if e = verifyMVPNativeSourceDescriptor(root, c, p); e != nil {
		return e
	}
	if e = readMVPNativeReleasePolicy(root, c, p); e != nil {
		return e
	}
	return verifyMVPNativeOriginalDescriptor(root, c, p)
}

func verifyMVPNativeOriginalDescriptor(root *os.Root, c configuration.Config, p mvpNativeBackupPlan) error {
	bad := failure("OFFLINE_SCOPE_NATIVE_BACKUP_DESCRIPTOR_BINDING_INVALID")
	name := "configuration/operator-config/" + configuration.NativePostgreSQLRecoveryDescriptorFile
	raw, e := regular(root, name, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return bad
	}
	d, e := configuration.LoadNativePostgreSQLRecoveryDescriptor(raw)
	if e != nil {
		return bad
	}
	bound, e := d.BindBackup(p.ScopeID, p.AttemptID, p.NativeRequest.MaintenanceSHA256, p.ReleaseManifestSHA256, p.NativeRequest.ControlImageSHA256, p.NativeRequest.PostgresImageSHA256, p.Request.Reason)
	if e != nil || !reflect.DeepEqual(bound, p.NativeRequest) {
		return bad
	}
	return nil
}

// Called only after the original network-none full verifier has actually exited
// successfully under the existing Scope task journal. Exact private stdout is
// retained as a separate file; decoding it does not grant execution authority.
func publishMVPNativeBackupManifest(ctx context.Context, root *os.Root, p mvpNativeBackupPlan, c configuration.Config, original []byte) (engine.BackupArchiveFile, error) {
	if e := verifyMVPNativeBackupReservation(ctx, root, p, c); e != nil {
		return engine.BackupArchiveFile{}, e
	}
	candidate, e := regular(root, mvpNativeDirectory+"/candidate.json", c.Verification.MaximumMetadataBytes)
	if e != nil {
		return engine.BackupArchiveFile{}, e
	}
	observed, e := engine.DecodeNativePostgreSQLBackupObservation(original, c.Verification.MaximumMetadataBytes, p.AttemptID, wire.Digest(encode(p.NativeRequest)), wire.Digest(candidate), p.ReleaseManifestSHA256)
	if e != nil || observed.NativePostgreSQL == nil || !observed.NativePostgreSQL.RegistryConfigured || observed.NativePostgreSQL.Registry == nil {
		return engine.BackupArchiveFile{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ORIGINAL_VERIFICATION_REQUIRED")
	}
	if _, _, e = readMVPNativeCandidate(root, c, p, *observed.NativePostgreSQL); e != nil {
		return engine.BackupArchiveFile{}, e
	}
	if e = confirmMVPNativePublication(root, mvpNativeObservationFile, observed.Original, c.Verification.MaximumMetadataBytes); e != nil {
		return engine.BackupArchiveFile{}, e
	}
	entries, total, e := inventoryMVPNativeSource(ctx, root, p.Request.MaximumBytes, p.Request.MaximumEntries, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return engine.BackupArchiveFile{}, e
	}
	if _, e = readMVPNativeArchive(ctx, root, c, p, entries); e != nil {
		return engine.BackupArchiveFile{}, e
	}
	if e = verifyMVPNativePayloadInventory(root, c, entries); e != nil {
		return engine.BackupArchiveFile{}, e
	}
	m := mvpNativeBackupManifest{FormatVersion: 2, Status: "complete_selected_profile_backup_restore_pending", ScopeID: p.ScopeID, AttemptID: p.AttemptID, PlanSHA256: wire.Digest(encode(p)), ReleaseID: p.ReleaseID, Profile: p.Profile, ReleaseManifestSHA256: p.ReleaseManifestSHA256, NativePostgreSQL: *observed.NativePostgreSQL, ObservationSHA256: observed.OriginalSHA256, Entries: entries}
	raw := encode(m)
	if int64(len(raw)) > c.Verification.MaximumMetadataBytes || total > p.Request.MaximumBytes-int64(len(raw)) || len(entries)+2 > p.Request.MaximumEntries {
		return engine.BackupArchiveFile{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_INVENTORY_LIMIT")
	}
	if e = confirmMVPNativePublication(root, backupManifestFile, raw, c.Verification.MaximumMetadataBytes); e != nil {
		return engine.BackupArchiveFile{}, e
	}
	// Publication is not a journal completion. Caller must independently reopen
	// this exact inventory and durably complete the original final backup step.
	return engine.BackupArchiveFile{Name: backupManifestFile, Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}, nil
}

func (l *locked) inspectMVPNativeBackup(ctx context.Context, r configuration.BackupRequest) (BackupResult, error) {
	record := completedBackupForRequest(l.events, r)
	if record == nil {
		return l.backupResult(), failure("OFFLINE_SCOPE_COMPLETED_BACKUP_REQUIRED")
	}
	sourceRequest := configuration.RestoreSourceRequest{FormatVersion: 1, BackupRoot: r.DestinationRoot, ManifestSHA256: record.ArtifactSHA256, MaximumBytes: r.MaximumBytes, MaximumEntries: r.MaximumEntries}
	source, e := readMVPNativeRestoreSource(ctx, l.config, sourceRequest)
	if e != nil {
		return l.backupResult(), e
	}
	if source.Plan.ScopeID != l.marker.ScopeID || source.Plan.AttemptID != record.AttemptID || source.Result.PlanSHA256 != record.PlanSHA256 || !reflect.DeepEqual(source.Plan.Request, r) {
		return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_PLAN_CHANGED")
	}
	root, e := openBackupDirectory(r.DestinationRoot)
	if e != nil {
		return l.backupResult(), e
	}
	defer root.Close()
	raw, e := regular(root, backupManifestFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || int64(len(raw)) != record.ArtifactBytes {
		return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_MANIFEST_CHANGED")
	}
	return BackupResult{Status: "complete_selected_profile_backup_restore_pending", ScopeID: l.marker.ScopeID, AttemptID: record.AttemptID, CompletedSteps: record.TotalSteps, TotalSteps: record.TotalSteps, ManifestSHA256: record.ArtifactSHA256, Qualification: "retained_original_verified_native_source_inventory_only_not_new_native_execution_or_target_qualification", Profile: source.Result.Profile, ReleaseManifestSHA256: source.Result.ReleaseManifestSHA256, NativePostgreSQL: source.Result.NativePostgreSQL}, nil
}

// Explicit publication/reconciliation only: a fully visible exact original file
// is synced again before journal confirmation; a partial/changed file is retained
// and refused. Readonly inspection never calls this function.
func confirmMVPNativePublication(root *os.Root, name string, raw []byte, maximum int64) error {
	if int64(len(raw)) > maximum || len(raw) == 0 {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PUBLICATION_LIMIT")
	}
	if _, e := root.Lstat(name); os.IsNotExist(e) {
		return writeBackupFile(root, name, raw)
	} else if e != nil {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PUBLICATION_UNAVAILABLE")
	}
	retained, e := regular(root, name, maximum)
	if e != nil || wire.Digest(retained) != wire.Digest(raw) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PUBLICATION_CHANGED")
	}
	parent, parents, e := pinPrivateParent(root, name)
	if e != nil {
		return e
	}
	defer parent.Close()
	before, e := parent.Lstat(path.Base(name))
	if e != nil {
		return e
	}
	f, e := openPrivateSnapshot(root, parent, parents, path.Base(name), before, maximum)
	if e != nil {
		return e
	}
	defer f.Close()
	if f.Sync() != nil {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PUBLICATION_SYNC_FAILED")
	}
	after, e := f.Stat()
	named, ne := parent.Lstat(path.Base(name))
	if e != nil || ne != nil || !samePrivateSnapshot(before, after, maximum) || !samePrivateSnapshot(before, named, maximum) || !privateParentsUnchanged(root, parent, parents) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PUBLICATION_CHANGED")
	}
	return syncBackupDirectory(root, path.Dir(name))
}

func verifyMVPNativeSourceDescriptor(root *os.Root, c configuration.Config, p mvpNativeBackupPlan) error {
	bad := failure("OFFLINE_SCOPE_NATIVE_SOURCE_DESCRIPTOR_CHANGED")
	raw, e := regular(root, mvpNativeSourceDescriptorFile, c.Verification.MaximumMetadataBytes)
	var d InputsDescriptor
	if e != nil || wire.Digest(raw) != p.DescriptorSHA256 || wire.StrictJSON(raw, &d) != nil || d.FormatVersion != 1 || d.ScopeID != p.ScopeID || d.ConfigurationSHA256 != p.ConfigurationSHA256 || d.StorageProfile != namedVolumeStorage {
		return bad
	}
	seen := map[string]bool{}
	nativeRole := false
	for _, role := range d.Roles {
		if seen[role.Key] {
			return bad
		}
		seen[role.Key] = true
		if role.Key == "operator-config" {
			nativeRole = role.UID == p.NativeUID && role.GID == p.NativeGID
		}
	}
	if !nativeRole {
		return bad
	}
	return nil
}
