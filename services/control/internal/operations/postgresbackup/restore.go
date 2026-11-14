package postgresbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func importPostgresArchive(ctx context.Context, c connection, r release.PostgreSQLRestoreRequest, m Manifest) error {
	return privateArchive(ctx, filepath.Join(r.BackupRoot, archiveName), r.Policy.MaximumSQLBytes, m.Archive.SHA256, func(file *os.File) error {
		return c.withFiles(ctx, targetContext(m, r), func(environment []string) error {
			if e := trustedTool(ctx, r.Policy.RestoreFile, r.Policy.RestoreSHA256); e != nil {
				return e
			}
			e := runNative(ctx, r.Policy, r.Policy.RestoreFile, []string{"--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges", "--no-password", "--dbname=service=projectone_m18_backup"}, environment, file, &limitedBuffer{maximum: r.Policy.MaximumManifestBytes})
			if e != nil {
				return e
			}
			return trustedTool(ctx, r.Policy.RestoreFile, r.Policy.RestoreSHA256)
		})
	})
}

func targetStoreTrees(ctx context.Context, c config.Config, r release.PostgreSQLRestoreRequest) ([]TreeReadback, error) {
	out := []TreeReadback{}
	var bytes int64
	files := 0
	for _, path := range storeRoots(c) {
		tree, e := TreeDigest(ctx, path, r.Policy.MaximumStoreBytes-bytes, r.Policy.MaximumStoreFiles-files, r.Policy.MaximumManifestBytes)
		if e != nil {
			return nil, e
		}
		out = append(out, tree)
		bytes += tree.Bytes
		files += tree.Files
	}
	return out, nil
}

// Restore is one explicit new-target operation. An existing record root refuses
// every replay. A partial import remains retained for read-only diagnosis or a
// separately provisioned new scope; no source/live directory is overwritten.
func Restore(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator, runtime config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreRequest) (out RestoreCompletion, result error) {
	if ctx == nil || pool == nil || lease == nil || !r.Valid() {
		return out, errors.New("POSTGRES_RESTORE_CONTEXT_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	if e := RefuseAmbient(); e != nil {
		return out, e
	}
	m, e := loadRestoreSource(ctx, r)
	if e != nil {
		return out, e
	}
	if e = restoreInputClosure(ctx, operator, runtime, operatorPath, m, r); e != nil {
		return out, e
	}
	if e = verifyTools(ctx, r.Policy, false); e != nil {
		return out, e
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, r.Policy.ControlSHA256); e != nil {
		return out, e
	}
	pc, lc := pool.Config().ConnConfig, lease.Conn().Config()
	if pc.Host != lc.Host || pc.Port != lc.Port || pc.Database != lc.Database || pc.User != lc.User || pc.Password != lc.Password {
		return out, errors.New("POSTGRES_RESTORE_POOL_SESSION_MISMATCH")
	}
	c, e := readConnection(operator.Database.DSNFile, targetContext(m, r), lc)
	if e != nil {
		return out, e
	}
	target, e := readRestoreTarget(ctx, lease, r, true)
	if e != nil {
		return out, e
	}
	if e = distinctMVPSourceTarget(m, target); e != nil {
		return out, e
	}
	registryRoot, e := mvpRegistryTargetRoot(runtime, m, r, true)
	if e != nil {
		return out, e
	}
	roots, e := acquireFreshRoots(storeRoots(runtime))
	if e != nil {
		return out, e
	}
	defer func() {
		if e := roots.Close(); e != nil {
			out = RestoreCompletion{}
			result = errors.Join(result, e)
		}
	}()
	if e = matchExpectedRestoreRoots(r.TargetRoots, roots.Identities()); e != nil {
		return out, e
	}
	var sourceBytes int64
	for _, s := range m.Stores {
		sourceBytes += s.Tree.Bytes
	}
	for _, path := range append(mvpRestoreAllocationRoots(runtime, m), filepath.Dir(r.RestoreRoot), r.Policy.ScratchRoot) {
		if e = requireSpace(path, 2*sourceBytes+mvpRegistryRestoreBytes(m)+4*r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
			return out, e
		}
	}
	root, e := newDirectory(r.RestoreRoot)
	if e != nil {
		return out, e
	}
	defer func() {
		if root.Close() != nil {
			out = RestoreCompletion{}
			result = errors.Join(result, errors.New("POSTGRES_RESTORE_ROOT_CLOSE_UNCONFIRMED"))
		}
	}()
	effective, e := configurationSnapshot(operator, runtime, operatorPath, targetContext(m, r))
	if e != nil {
		return out, e
	}
	info, e := root.Stat(".")
	if e != nil {
		return out, errors.New("POSTGRES_RESTORE_RECORD_ROOT_CHANGED")
	}
	record, e := rootIdentity(r.RestoreRoot, info)
	if e != nil {
		return out, e
	}
	i := newRestoreIntent(r, m, target, roots.Identities(), digest(effective), record)
	i.RegistryRoot = registryRoot
	raw, e := canonical(i)
	if e != nil {
		return out, e
	}
	intentSHA := digest(raw)
	if e = roots.Check(true); e != nil {
		return out, e
	}
	if current, e := readRestoreTarget(ctx, lease, r, true); e != nil || current != target {
		return out, errors.New("POSTGRES_RESTORE_TARGET_CHANGED")
	}
	if e = writeDocument(root, restoreIntentName, i, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	if e = writeDocument(root, "sql-import-intent.json", map[string]string{"intent_sha256": intentSHA, "archive_sha256": m.Archive.SHA256}, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	if e = importPostgresArchive(ctx, c, r, m); e != nil {
		return out, e
	}
	if e = roots.Check(true); e != nil {
		return out, e
	}
	imported, e := readDatabase(ctx, lease, targetContext(m, r))
	if e != nil || imported.ContentSHA256 != m.Database.ContentSHA256 || imported.Rows != m.Database.Rows || imported.Tables != m.Database.Tables || imported.Sequences != m.Database.Sequences {
		return out, errors.New("POSTGRES_RESTORE_IMPORTED_DATA_MISMATCH")
	}
	if e = writeDocument(root, "sql-import-result.json", map[string]any{"intent_sha256": intentSHA, "data": imported}, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	accounts := &identity.Service{DB: pool, Config: runtime}
	if e = writeDocument(root, "stores-import-intent.json", map[string]any{"intent_sha256": intentSHA, "roots": roots.Identities(), "source_stores": m.Stores}, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	if e = importRestoreOwners(ctx, runtime, accounts, filepath.Join(r.BackupRoot, "stores")); e != nil {
		return out, e
	}
	if e = roots.Check(false); e != nil {
		return out, e
	}
	registryImport, e := importMVPRegistry(ctx, lease, runtime, m, i)
	if e != nil {
		return out, e
	}
	owners, e := openOwners(ctx, runtime, accounts, m.Database.TenantID)
	if e != nil {
		return out, e
	}
	defer func() {
		if e := owners.Close(); e != nil {
			out = RestoreCompletion{}
			result = errors.Join(result, e)
		}
	}()
	roundtrip := filepath.Join(r.RestoreRoot, "roundtrip")
	round, e := newDirectory(roundtrip)
	if e != nil {
		return out, e
	}
	if e = round.Close(); e != nil {
		return out, errors.New("POSTGRES_RESTORE_ROUNDTRIP_CLOSE_UNCONFIRMED")
	}
	if e = owners.Export(ctx, roundtrip); e != nil {
		return out, e
	}
	// The existing snapshot reader expects stores/, so the fresh record's
	// roundtrip directory itself is the explicit readback root for this check.
	readbacks := []StoreReadback{}
	for _, s := range m.Stores {
		tree, e := TreeDigest(ctx, filepath.Join(roundtrip, s.Kind), r.Policy.MaximumStoreBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
		if e != nil {
			return out, e
		}
		readbacks = append(readbacks, StoreReadback{s.Kind, tree})
	}
	if !reflect.DeepEqual(readbacks, m.Stores) {
		return out, errors.New("POSTGRES_RESTORE_ROUNDTRIP_MISMATCH")
	}
	trees, e := targetStoreTrees(ctx, runtime, r)
	if e != nil {
		return out, e
	}
	f := RestoreFiles{"projectone.postgres.restore-files.v1", r.RestoreID, intentSHA, roots.Identities(), trees, readbacks, registryImport}
	if e = readbackMVPRegistryImport(ctx, runtime, m, i, f); e != nil {
		return out, e
	}
	if e = writeDocument(root, restoreFilesName, f, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	filesRaw, _ := canonical(f)
	filesSHA := digest(filesRaw)
	args := []string{"readback-kubernetes-postgres-restore-files", "--request", requestPath}
	if policyEnvironment != "" {
		args = append(args, "--policy-env-file", policyEnvironment)
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, r.Policy.ControlSHA256); e != nil {
		return out, e
	}
	response := &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, args, nil, nil, response); e != nil {
		return out, e
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, r.Policy.ControlSHA256); e != nil {
		return out, e
	}
	var independent RestoreFilesReadback
	if strict.DecodeStrict(response.Bytes(), &independent) != nil || !validRestoreFilesReadback(independent, i, intentSHA, filesSHA) || independent.TargetTreesSHA256 != valueDigest(f.TargetTrees) || independent.RoundtripSHA256 != valueDigest(f.Roundtrip) {
		return out, errors.New("POSTGRES_RESTORE_INDEPENDENT_READBACK_INVALID")
	}
	if e = writeDocument(root, "independent-files-readback.json", independent, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	if e = writeDocument(root, "authority-intent.json", map[string]string{"intent_sha256": intentSHA, "files_sha256": filesSHA}, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	if _, e = invalidateRestore(ctx, lease, accounts, i, intentSHA, filesSHA); e != nil {
		return out, e
	}
	post, e := readRestoreAuthority(ctx, lease, i, intentSHA, filesSHA)
	if e != nil {
		return out, e
	}
	if e = owners.Verify(ctx); e != nil {
		return out, e
	}
	currentTrees, e := targetStoreTrees(ctx, runtime, r)
	if e != nil || !reflect.DeepEqual(currentTrees, trees) {
		return out, errors.New("POSTGRES_RESTORE_TARGET_FILES_CHANGED")
	}
	current, sha, e := LoadRestoreRequest(requestPath, policyEnvironment)
	if e != nil || sha != i.RequestSHA256 || !reflect.DeepEqual(current, r) {
		return out, errors.New("POSTGRES_RESTORE_REQUEST_CHANGED")
	}
	if e = finalRestoreAdmission(ctx, lease, roots, operator, runtime, operatorPath, operatorEnvironment, m, i); e != nil {
		return out, e
	}
	if e = finalRestoreFiles(ctx, roots, runtime, r, trees); e != nil {
		return out, e
	}
	_, _, currentIntentSHA, currentFilesSHA, e := readRestoreRecords(r, m, runtime)
	if e != nil || currentIntentSHA != intentSHA || currentFilesSHA != filesSHA {
		return out, errors.New("POSTGRES_RESTORE_RECORD_CHANGED")
	}
	currentPost, e := readRestoreAuthority(ctx, lease, i, intentSHA, filesSHA)
	if e != nil || currentPost != post {
		return out, errors.New("POSTGRES_RESTORE_POST_IMAGE_CHANGED")
	}
	if e = readbackMVPRegistryImport(ctx, runtime, m, i, f); e != nil {
		return out, e
	}
	runtimeContext := restoredRuntimeContext(r, m)
	if !runtimeContext.Valid() {
		return out, errors.New("POSTGRES_RESTORE_RUNTIME_CONTEXT_INVALID")
	}
	if e = writeDocument(root, "runtime-context.json", runtimeContext, r.Policy.MaximumManifestBytes); e != nil {
		return out, e
	}
	out = RestoreCompletion{"projectone.postgres.restore-complete.v1", r.RestoreID, intentSHA, filesSHA, r.SourceManifestSHA256, m.Archive.SHA256, targetBinding(target), post, runtimeContext, time.Now().UTC().Format(time.RFC3339Nano), false, "Historical matched data/four-store restoration and committed original authority fencing. Current runtime-role preparation, served old-authority rejection and Kubernetes/CSI qualification are separate required steps."}
	if ctx.Err() != nil {
		return RestoreCompletion{}, errors.New("POSTGRES_RESTORE_INTERRUPTED")
	}
	if e = writeDocument(root, restoreCompleteName, out, r.Policy.MaximumManifestBytes); e != nil {
		return RestoreCompletion{}, e
	}
	return out, nil
}

func restoredRuntimeContext(r release.PostgreSQLRestoreRequest, m Manifest) release.RestoredRuntimeContext {
	return release.RestoredRuntimeContext{FormatVersion: 1, RestoreID: r.RestoreID, SourceScopeID: m.Request.SourceScopeID, TargetScopeID: r.TargetScopeID, BackupManifestSHA256: r.SourceManifestSHA256, SQLArchiveSHA256: m.Archive.SHA256, TenantID: m.Database.TenantID, SchemaVersion: m.Database.SchemaVersion, ApplicationProfile: r.ApplicationProfile, TargetDatabase: r.TargetDatabase, TargetOperator: r.TargetOperator, Reason: r.Reason}
}
