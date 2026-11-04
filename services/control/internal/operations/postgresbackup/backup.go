package postgresbackup

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func sourceTrees(ctx context.Context, c config.Config, r release.PostgreSQLBackupRequest) ([]TreeReadback, int64, int, error) {
	out := []TreeReadback{}
	var bytes int64
	files := 0
	for _, path := range storeRoots(c) {
		proof, e := TreeDigest(ctx, path, r.Policy.MaximumStoreBytes-bytes, r.Policy.MaximumStoreFiles-files, r.Policy.MaximumManifestBytes)
		if e != nil {
			return nil, 0, 0, e
		}
		out = append(out, proof)
		bytes += proof.Bytes
		files += proof.Files
	}
	return out, bytes, files, nil
}

func exportAudits(ctx context.Context, s *storeOwners, reason string) error {
	for _, audit := range []func(context.Context, string, string) error{s.models.AuditFileOperation, s.datasets.AuditFileOperation, s.training.AuditStoreOperation, s.preparations.AuditStoreOperation} {
		if e := audit(ctx, "export", reason); e != nil {
			return e
		}
	}
	return nil
}

func archive(ctx context.Context, c connection, r release.PostgreSQLBackupRequest, root *os.Root, snapshot string) (FileReadback, error) {
	if !snapshotPattern.MatchString(snapshot) {
		return FileReadback{}, errors.New("POSTGRES_BACKUP_SNAPSHOT_INVALID")
	}
	file, e := root.OpenFile(archiveName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return FileReadback{}, errors.New("POSTGRES_BACKUP_EXCLUSIVE_CREATE_FAILED")
	}
	writer := &boundedOutput{ctx: ctx, file: file, maximum: r.Policy.MaximumSQLBytes, minimum: r.Policy.MinimumFreeBytes}
	runError := c.withFiles(ctx, r, func(environment []string) error {
		if e := trustedTool(ctx, r.Policy.DumpFile, r.Policy.DumpSHA256); e != nil {
			return e
		}
		return runNative(ctx, r.Policy, r.Policy.DumpFile, []string{"--format=custom", "--no-owner", "--no-acl", "--no-password", "--snapshot=" + snapshot, "--dbname=service=projectone_m18_backup"}, environment, nil, writer)
	})
	// Retain/sync exact partial bytes after the native process has joined.
	if file.Sync() != nil {
		runError = errors.Join(runError, errors.New("POSTGRES_BACKUP_WRITE_UNCERTAIN"))
	}
	if file.Close() != nil {
		runError = errors.Join(runError, errors.New("POSTGRES_BACKUP_WRITE_UNCERTAIN"))
	}
	runError = errors.Join(runError, syncRoot(root))
	if runError != nil {
		return FileReadback{}, runError
	}
	if e = trustedTool(ctx, r.Policy.DumpFile, r.Policy.DumpSHA256); e != nil {
		return FileReadback{}, e
	}
	return fileDigest(ctx, filepath.Join(r.BackupRoot, archiveName), r.Policy.MaximumSQLBytes)
}

// Backup borrows the selected live pool/installation-lock session. It never
// closes, unlocks, commits, migrates, starts a worker, or claims physical writer
// absence. The caller retains those authorities until this function and every
// process/store cleanup have returned. No callback can manufacture a readback.
func Backup(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator, runtime config.Config, operatorPath, requestPath, policyEnvironment string, r release.PostgreSQLBackupRequest) (out Completion, result error) {
	if ctx == nil || pool == nil || lease == nil || !r.Valid() {
		return out, errors.New("POSTGRES_BACKUP_LOCKED_CONTEXT_REQUIRED")
	}
	if e := RefuseAmbient(); e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	effective, e := configurationSnapshot(operator, runtime, operatorPath, r)
	if e != nil {
		return out, e
	}
	if r.EffectiveApplicationProfile() == database.LineageName {
		if e = registryConfigurationClosure(ctx, runtime, r); e != nil {
			return out, e
		}
	}
	if _, e := os.Lstat(r.BackupRoot); !os.IsNotExist(e) {
		return out, errors.New("POSTGRES_BACKUP_DESTINATION_EXISTS")
	}
	parent, e := openPrivateRoot(filepath.Dir(r.BackupRoot))
	if e != nil {
		return out, e
	}
	if e = parent.Close(); e != nil {
		return out, errors.New("POSTGRES_BACKUP_ROOT_CLOSE_FAILED")
	}
	scratch, e := openPrivateRoot(r.Policy.ScratchRoot)
	if e != nil {
		return out, e
	}
	if e = scratch.Close(); e != nil {
		return out, errors.New("POSTGRES_BACKUP_ROOT_CLOSE_FAILED")
	}
	pc, lc := pool.Config().ConnConfig, lease.Conn().Config()
	if pc.Host != lc.Host || pc.Port != lc.Port || pc.Database != lc.Database || pc.User != lc.User || pc.Password != lc.Password {
		return out, errors.New("POSTGRES_BACKUP_POOL_SESSION_MISMATCH")
	}
	connection, e := readConnection(operator.Database.DSNFile, r, lc)
	if e != nil {
		return out, e
	}
	if e = verifyMVPPrincipal(ctx, pool, lease, r); e != nil {
		return out, e
	}
	if _, e = readMVPBackupDatabase(ctx, lease, r); e != nil {
		return out, e
	}
	if e = verifyTools(ctx, r.Policy, false); e != nil {
		return out, e
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, r.Policy.ControlSHA256); e != nil {
		return out, e
	}
	owners, e := openOwners(ctx, runtime, &identity.Service{DB: pool, Config: runtime}, r.TenantID)
	if e != nil {
		return out, e
	}
	defer func() {
		if e := owners.Close(); e != nil {
			out = Completion{}
			result = errors.Join(result, e)
		}
	}()
	sources, sourceBytes, sourceFiles, e := sourceTrees(ctx, runtime, r)
	if e != nil {
		return out, e
	}
	registrySource, e := mvpRegistrySourceTree(ctx, runtime, r, sourceBytes, sourceFiles)
	if e != nil {
		return out, e
	}
	var registryBytes int64
	registryFiles := 0
	if registrySource != nil {
		registryBytes = registrySource.Bytes
		registryFiles = registrySource.Files
	}
	configuration, e := TreeDigest(ctx, r.ConfigurationRoot, r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
	if e != nil {
		return out, e
	}
	metadata := int64(models.MaximumSnapshotMetadataBytes) + runtime.Policy().Datasets.SnapshotManifestBytes + runtime.Policy().Training.SnapshotManifestBytes + runtime.Policy().ModelLifecycle.SnapshotManifestBytes
	extraMetadata, extraFiles := int64(0), 0
	if registrySource != nil {
		extraMetadata = r.Policy.MaximumManifestBytes
		extraFiles = 1
	}
	if sourceBytes+registryBytes > r.Policy.MaximumStoreBytes-metadata-extraMetadata || sourceFiles+registryFiles > r.Policy.MaximumStoreFiles-4-extraFiles {
		return out, errors.New("POSTGRES_BACKUP_STORE_LIMIT_REACHED")
	}
	peak := sourceBytes + registryBytes + metadata + configuration.Bytes + int64(len(effective)) + r.Policy.MaximumSQLBytes + 4*r.Policy.MaximumManifestBytes
	if e = requireSpace(filepath.Dir(r.BackupRoot), peak, r.Policy.MinimumFreeBytes); e != nil {
		return out, e
	}
	root, e := newDirectory(r.BackupRoot)
	if e != nil {
		return out, e
	}
	defer func() {
		if root.Close() != nil {
			out = Completion{}
			result = errors.Join(result, errors.New("POSTGRES_BACKUP_ROOT_CLOSE_FAILED"))
		}
	}()
	if root.Mkdir("stores", 0700) != nil {
		return out, errors.New("POSTGRES_BACKUP_EXCLUSIVE_CREATE_FAILED")
	}
	if r.EffectiveApplicationProfile() == database.LineageName {
		if root.Mkdir(mvpArtifactsDirectory, 0700) != nil {
			return out, errors.New("POSTGRES_BACKUP_EXCLUSIVE_CREATE_FAILED")
		}
	}
	if e = syncRoot(root); e != nil {
		return out, e
	}
	if e = owners.Export(ctx, filepath.Join(r.BackupRoot, "stores")); e != nil {
		return out, e
	}
	if e = exportAudits(ctx, owners, r.Reason); e != nil {
		return out, e
	}
	var archiveProof FileReadback
	var snapshot string
	var sourceBinding *MVPSourceBinding
	before, e := withDatabaseSnapshot(ctx, lease, r, func(tx pgx.Tx, _ DatabaseReadback) error {
		var bindError error
		sourceBinding, bindError = readMVPSourceBinding(ctx, tx, r)
		if bindError != nil {
			return bindError
		}
		if sourceBinding != nil {
			sourceBinding.Registry, bindError = exportMVPRegistryWithin(ctx, tx, runtime, r, sourceBinding)
			if bindError != nil {
				return bindError
			}
		}
		if tx.QueryRow(ctx, "SELECT pg_catalog.pg_export_snapshot()").Scan(&snapshot) != nil || !snapshotPattern.MatchString(snapshot) {
			return errors.New("POSTGRES_BACKUP_SNAPSHOT_UNAVAILABLE")
		}
		var dumpError error
		archiveProof, dumpError = archive(ctx, connection, r, root, snapshot)
		return dumpError
	})
	if e != nil {
		return out, e
	}
	copied, e := copyTree(ctx, r.ConfigurationRoot, filepath.Join(r.BackupRoot, "configuration"), r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes)
	if e != nil || copied != configuration {
		return out, errors.New("POSTGRES_BACKUP_CONFIGURATION_CHANGED")
	}
	if e = writeNew(root, effectiveName, effective); e != nil {
		return out, e
	}
	stores, e := snapshotTrees(ctx, r)
	if e != nil {
		return out, e
	}
	m := Manifest{"projectone.postgres.matched-backup.v1", r, requestDigest(r), time.Now().UTC().Format(time.RFC3339Nano), before, snapshot, sources, stores, archiveProof, configuration, FileReadback{digest(effective), int64(len(effective))}, sourceBinding}
	if e = writeDocument(root, candidateName, m, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	raw, _ := canonical(m)
	manifestSHA := digest(raw)
	args := []string{"readback-kubernetes-postgres-backup", "--request", requestPath, "--manifest-sha256", manifestSHA}
	if policyEnvironment != "" {
		args = append(args, "--policy-env-file", policyEnvironment)
	}
	response := &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, args, nil, nil, response); e != nil {
		return out, e
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, r.Policy.ControlSHA256); e != nil {
		return out, e
	}
	var independent Readback
	if strict.DecodeStrict(response.Bytes(), &independent) != nil || independent.Format != "projectone.postgres.independent-readback.v1" || independent.BackupID != r.BackupID || independent.RequestSHA256 != m.RequestSHA256 || independent.ManifestSHA256 != manifestSHA || independent.ArchiveSHA256 != m.Archive.SHA256 || independent.CurrentLockHeld || !release.ValidSHA256(independent.ArchiveDirectorySHA256) {
		return out, errors.New("POSTGRES_BACKUP_INDEPENDENT_READBACK_INVALID")
	}
	if _, e = time.Parse(time.RFC3339Nano, independent.ObservedAt); e != nil {
		return out, errors.New("POSTGRES_BACKUP_INDEPENDENT_READBACK_INVALID")
	}
	currentPayload, e := Verify(ctx, r, manifestSHA, false)
	if e != nil || currentPayload.ArchiveDirectorySHA256 != independent.ArchiveDirectorySHA256 {
		return out, errors.New("POSTGRES_BACKUP_PAYLOAD_CHANGED")
	}
	after, e := readDatabase(ctx, lease, r)
	if e != nil || after != before {
		return out, errors.New("POSTGRES_BACKUP_DATABASE_CHANGED")
	}
	if e = recheckMVPSourceBinding(ctx, lease, r, sourceBinding); e != nil {
		return out, e
	}
	if e = verifyMVPRegistrySource(ctx, lease, runtime, r, sourceBinding, registrySource); e != nil {
		return out, e
	}
	if e = owners.Verify(ctx); e != nil {
		return out, e
	}
	currentSources, _, _, e := sourceTrees(ctx, runtime, r)
	if e != nil || !reflect.DeepEqual(currentSources, sources) {
		return out, errors.New("POSTGRES_BACKUP_SOURCE_CHANGED")
	}
	currentConfiguration, e := TreeDigest(ctx, r.ConfigurationRoot, r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
	if e != nil || currentConfiguration != configuration {
		return out, errors.New("POSTGRES_BACKUP_CONFIGURATION_CHANGED")
	}
	latestRuntime, e := config.LoadProtectedWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_CONFIGURATION_CHANGED")
	}
	latestEffective, e := configurationSnapshot(operator, latestRuntime, operatorPath, r)
	if e != nil || !reflect.DeepEqual(latestEffective, effective) {
		return out, errors.New("POSTGRES_BACKUP_CONFIGURATION_CHANGED")
	}
	currentRequest, sha, e := LoadRequest(requestPath, policyEnvironment)
	if e != nil || sha != m.RequestSHA256 || !reflect.DeepEqual(currentRequest, r) {
		return out, errors.New("POSTGRES_BACKUP_REQUEST_CHANGED")
	}
	if e = verifyMVPPrincipal(ctx, pool, lease, r); e != nil {
		return out, e
	}
	if ctx.Err() != nil {
		return out, errors.New("POSTGRES_BACKUP_INTERRUPTED")
	}
	if e = writeDocument(root, readbackName, independent, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	readbackRaw, _ := canonical(independent)
	out = Completion{"projectone.postgres.backup-complete.v1", r.BackupID, manifestSHA, digest(readbackRaw), time.Now().UTC().Format(time.RFC3339Nano), "Historical matched SQL/four-store/configuration bytes under one continuous installation lock. Physical/Kubernetes authority and fresh restored-runtime qualification are separate."}
	if e = writeDocument(root, completeName, out, r.Policy.MaximumManifestBytes); e != nil {
		return Completion{}, e
	}
	return out, nil
}
