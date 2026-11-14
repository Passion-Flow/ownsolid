package postgresbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreFilesReadback struct {
	Format            string `json:"format"`
	RestoreID         string `json:"restore_id"`
	RequestSHA256     string `json:"request_sha256"`
	IntentSHA256      string `json:"intent_sha256"`
	FilesSHA256       string `json:"files_sha256"`
	RootsSHA256       string `json:"roots_sha256"`
	TargetTreesSHA256 string `json:"target_trees_sha256"`
	RoundtripSHA256   string `json:"roundtrip_sha256"`
	ObservedAt        string `json:"observed_at"`
	CurrentLockHeld   bool   `json:"current_lock_held"`
}
type RestoreObservation struct {
	Format                    string `json:"format"`
	RestoreID                 string `json:"restore_id"`
	IntentSHA256              string `json:"intent_sha256"`
	FilesSHA256               string `json:"files_sha256"`
	ManifestSHA256            string `json:"manifest_sha256"`
	TargetBindingSHA256       string `json:"target_binding_sha256"`
	PostDataSHA256            string `json:"post_data_sha256"`
	OriginalCompletionPresent bool   `json:"original_completion_present"`
	ReadOnly                  bool   `json:"read_only"`
	CurrentLockHeld           bool   `json:"current_lock_held"`
	ObservedAt                string `json:"observed_at"`
	Qualification             string `json:"qualification"`
}

func valueDigest(value any) string {
	raw, e := canonical(value)
	if e != nil {
		return ""
	}
	return digest(raw)
}
func restoreConfigurationDigest(c config.Config) string {
	raw, e := yaml.Marshal(c)
	if e != nil {
		return ""
	}
	return digest(raw)
}

func readRestoreRecords(r release.PostgreSQLRestoreRequest, m Manifest, runtime config.Config) (i RestoreIntent, f RestoreFiles, intentSHA, filesSHA string, result error) {
	bad := errors.New("POSTGRES_RESTORE_RECORD_BINDING_INVALID")
	raw, e := readPrivateDocument(filepath.Join(r.RestoreRoot, restoreIntentName), r.Policy.MaximumManifestBytes, &i)
	if e != nil {
		return i, f, "", "", bad
	}
	intentSHA = digest(raw)
	if i.Format != "projectone.postgres.restore-intent.v1" || !reflect.DeepEqual(i.Request, r) || i.RequestSHA256 != restoreRequestDigest(r) || i.BackupID != m.Request.BackupID || i.SourceScopeID != m.Request.SourceScopeID || i.ArchiveSHA256 != m.Archive.SHA256 || i.SourceDataSHA256 != m.Database.ContentSHA256 || i.TenantID != m.Database.TenantID || i.ConfigurationSHA256 != restoreConfigurationDigest(runtime) || !release.ValidSHA256(i.ConfigurationSHA256) || len(i.Roots) != 4 || i.Target.Database != r.TargetDatabase || i.Target.Operator != r.TargetOperator || i.Target.Marker != "projectone:kubernetes-restore:"+r.TargetScopeID || i.Target.ServerVersion != 180006 || i.Target.DatabaseOID == 0 || i.Target.BackendPID < 1 || i.Target.SystemIdentifier == "" {
		return i, f, "", "", bad
	}
	if _, e = time.Parse(time.RFC3339Nano, i.CreatedAt); e != nil {
		return i, f, "", "", bad
	}
	if i.RecordRoot.Path != r.RestoreRoot || observeRestoreRoot(i.RecordRoot) != nil {
		return i, f, "", "", errors.New("POSTGRES_RESTORE_RECORD_ROOT_CHANGED")
	}
	for n, path := range storeRoots(runtime) {
		if i.Roots[n].Path != path {
			return i, f, "", "", bad
		}
	}
	raw, e = readPrivateDocument(filepath.Join(r.RestoreRoot, restoreFilesName), r.Policy.MaximumManifestBytes, &f)
	if e != nil || f.Format != "projectone.postgres.restore-files.v1" || f.RestoreID != r.RestoreID || f.IntentSHA256 != intentSHA || !reflect.DeepEqual(f.Roots, i.Roots) || len(f.TargetTrees) != 4 || !reflect.DeepEqual(f.Roundtrip, m.Stores) {
		return i, f, "", "", bad
	}
	return i, f, intentSHA, digest(raw), nil
}

func observeRestoreRoots(expected []RootIdentity) error {
	if len(expected) != 4 {
		return errors.New("POSTGRES_RESTORE_RECORD_BINDING_INVALID")
	}
	for _, id := range expected {
		if e := observeRestoreRoot(id); e != nil {
			return e
		}
	}
	return nil
}

func observeRestoreRoot(id RootIdentity) error {
	root, e := openPrivateRoot(id.Path)
	if e != nil {
		return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
	}
	info, e := root.Stat(".")
	ce := root.Close()
	if e != nil || ce != nil {
		return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
	}
	actual, e := rootIdentity(id.Path, info)
	if e != nil || actual != id {
		return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
	}
	return nil
}

func finalRestoreFiles(ctx context.Context, roots *freshRoots, runtime config.Config, r release.PostgreSQLRestoreRequest, expected []TreeReadback) error {
	if e := roots.Check(false); e != nil {
		return e
	}
	trees, e := targetStoreTrees(ctx, runtime, r)
	if e != nil || !reflect.DeepEqual(trees, expected) {
		return errors.New("POSTGRES_RESTORE_TARGET_FILES_CHANGED")
	}
	return roots.Check(false)
}

func validRestoreFilesReadback(b RestoreFilesReadback, i RestoreIntent, intentSHA, filesSHA string) bool {
	_, e := time.Parse(time.RFC3339Nano, b.ObservedAt)
	return e == nil && b.Format == "projectone.postgres.restore-files-readback.v1" && b.RestoreID == i.Request.RestoreID && b.RequestSHA256 == i.RequestSHA256 && b.IntentSHA256 == intentSHA && b.FilesSHA256 == filesSHA && b.RootsSHA256 == valueDigest(i.Roots) && release.ValidSHA256(b.TargetTreesSHA256) && release.ValidSHA256(b.RoundtripSHA256) && !b.CurrentLockHeld
}

// This entrypoint opens no SQL connection, starts no store owner and takes no
// lock from its parent. It independently checks every current byte and exact
// original directory identity through the protected immutable records.
func ReadbackRestoreFiles(ctx context.Context, r release.PostgreSQLRestoreRequest) (out RestoreFilesReadback, result error) {
	if ctx == nil || !r.Valid() {
		return out, errors.New("POSTGRES_RESTORE_REQUEST_INVALID")
	}
	if e := RefuseAmbient(); e != nil {
		return out, e
	}
	m, e := loadRestoreSource(ctx, r)
	if e != nil {
		return out, e
	}
	runtime, e := config.LoadProtectedWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil {
		return out, errors.New("POSTGRES_RESTORE_RUNTIME_CONFIGURATION_INVALID")
	}
	i, f, intentSHA, filesSHA, e := readRestoreRecords(r, m, runtime)
	if e != nil {
		return out, e
	}
	if e = readbackMVPRegistryImport(ctx, runtime, m, i, f); e != nil {
		return out, e
	}
	if e = validateStoreRoots(runtime, r.BackupRoot, r.RestoreRoot, r.ConfigurationRoot, r.Policy.ScratchRoot); e != nil {
		return out, e
	}
	if e = observeRestoreRoots(i.Roots); e != nil {
		return out, e
	}
	trees, e := targetStoreTrees(ctx, runtime, r)
	if e != nil || !reflect.DeepEqual(trees, f.TargetTrees) {
		return out, errors.New("POSTGRES_RESTORE_TARGET_FILES_CHANGED")
	}
	var bytes int64
	files := 0
	for _, store := range f.Roundtrip {
		tree, e := TreeDigest(ctx, filepath.Join(r.RestoreRoot, "roundtrip", store.Kind), r.Policy.MaximumStoreBytes-bytes, r.Policy.MaximumStoreFiles-files, r.Policy.MaximumManifestBytes)
		if e != nil || tree != store.Tree {
			return out, errors.New("POSTGRES_RESTORE_ROUNDTRIP_MISMATCH")
		}
		bytes += tree.Bytes
		files += tree.Files
	}
	if e = observeRestoreRoots(i.Roots); e != nil {
		return out, e
	}
	// Re-read immutable proof bytes after the potentially long inventory.
	j, g, sha, fs, e := readRestoreRecords(r, m, runtime)
	if e != nil || sha != intentSHA || fs != filesSHA || !reflect.DeepEqual(i, j) || !reflect.DeepEqual(f, g) {
		return out, errors.New("POSTGRES_RESTORE_RECORD_CHANGED")
	}
	if ctx.Err() != nil {
		return out, errors.New("POSTGRES_RESTORE_INTERRUPTED")
	}
	return RestoreFilesReadback{"projectone.postgres.restore-files-readback.v1", r.RestoreID, i.RequestSHA256, intentSHA, filesSHA, valueDigest(i.Roots), valueDigest(trees), valueDigest(f.Roundtrip), time.Now().UTC().Format(time.RFC3339Nano), false}, nil
}

// VerifyRestore observes the already committed exact post-image. It never
// imports, invalidates authority, grants a role or creates a missing completion.
func VerifyRestore(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator, runtime config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreRequest) (out RestoreObservation, result error) {
	if ctx == nil || pool == nil || lease == nil || !r.Valid() {
		return out, errors.New("POSTGRES_RESTORE_CONTEXT_REQUIRED")
	}
	if e := AdmitRestore(ctx, operator, runtime, operatorPath, r, false); e != nil {
		return out, e
	}
	m, e := loadRestoreSource(ctx, r)
	if e != nil {
		return out, e
	}
	i, f, intentSHA, filesSHA, e := readRestoreRecords(r, m, runtime)
	if e != nil {
		return out, e
	}
	current, e := readRestoreTarget(ctx, lease, r, false)
	if e != nil || targetBinding(current) != targetBinding(i.Target) {
		return out, errors.New("POSTGRES_RESTORE_TARGET_CHANGED")
	}
	pc, lc := pool.Config().ConnConfig, lease.Conn().Config()
	if pc.Host != lc.Host || pc.Port != lc.Port || pc.Database != lc.Database || pc.User != lc.User || pc.Password != lc.Password {
		return out, errors.New("POSTGRES_RESTORE_POOL_SESSION_MISMATCH")
	}
	roots, e := acquireRestoreRoots(storeRoots(runtime), false)
	if e != nil {
		return out, e
	}
	defer func() {
		if e := roots.Close(); e != nil {
			out = RestoreObservation{}
			result = errors.Join(result, e)
		}
	}()
	if !reflect.DeepEqual(roots.Identities(), i.Roots) {
		return out, errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
	}
	b, e := ReadbackRestoreFiles(ctx, r)
	if e != nil {
		return out, e
	}
	var prior RestoreFilesReadback
	if _, e = readPrivateDocument(filepath.Join(r.RestoreRoot, "independent-files-readback.json"), r.Policy.MaximumManifestBytes, &prior); e != nil || !validRestoreFilesReadback(prior, i, intentSHA, filesSHA) || prior.TargetTreesSHA256 != b.TargetTreesSHA256 || prior.RoundtripSHA256 != b.RoundtripSHA256 {
		return out, errors.New("POSTGRES_RESTORE_INDEPENDENT_READBACK_INVALID")
	}
	owners, e := openVerificationOwners(ctx, runtime, &identity.Service{DB: pool, Config: runtime}, i.TenantID)
	if e != nil {
		return out, e
	}
	defer func() {
		if e := owners.Close(); e != nil {
			out = RestoreObservation{}
			result = errors.Join(result, e)
		}
	}()
	post, e := readRestoreAuthority(ctx, lease, i, intentSHA, filesSHA)
	if e != nil {
		return out, e
	}
	complete := false
	if _, e = os.Lstat(filepath.Join(r.RestoreRoot, restoreCompleteName)); e == nil {
		var c RestoreCompletion
		if _, e = readPrivateDocument(filepath.Join(r.RestoreRoot, restoreCompleteName), r.Policy.MaximumManifestBytes, &c); e != nil || c.Format != "projectone.postgres.restore-complete.v1" || c.RestoreID != r.RestoreID || c.IntentSHA256 != intentSHA || c.FilesSHA256 != filesSHA || c.ManifestSHA256 != r.SourceManifestSHA256 || c.ArchiveSHA256 != m.Archive.SHA256 || c.TargetBindingSHA256 != targetBinding(i.Target) || c.PostDataSHA256 != post || c.CurrentLockHeld || !c.RuntimeContext.Valid() || !reflect.DeepEqual(c.RuntimeContext, restoredRuntimeContext(r, m)) {
			return out, errors.New("POSTGRES_RESTORE_COMPLETION_MISMATCH")
		}
		if _, e = time.Parse(time.RFC3339Nano, c.CompletedAt); e != nil {
			return out, errors.New("POSTGRES_RESTORE_COMPLETION_MISMATCH")
		}
		var rc release.RestoredRuntimeContext
		if _, e = readPrivateDocument(filepath.Join(r.RestoreRoot, "runtime-context.json"), r.Policy.MaximumManifestBytes, &rc); e != nil || !reflect.DeepEqual(rc, c.RuntimeContext) {
			return out, errors.New("POSTGRES_RESTORE_COMPLETION_MISMATCH")
		}
		complete = true
	} else if !os.IsNotExist(e) {
		return out, errors.New("POSTGRES_RESTORE_COMPLETION_UNREADABLE")
	}
	latest, sha, e := LoadRestoreRequest(requestPath, policyEnvironment)
	if e != nil || sha != i.RequestSHA256 || !reflect.DeepEqual(latest, r) {
		return out, errors.New("POSTGRES_RESTORE_REQUEST_CHANGED")
	}
	// Reopen the supplementary target bytes against the original historical
	// import binding before adjusting this observer transaction's current PID.
	if e = readbackMVPRegistryImport(ctx, runtime, m, i, f); e != nil {
		return out, e
	}
	// Only the current local observer PID changes; original bytes stay intact.
	i.Target.BackendPID = current.BackendPID
	if e = finalRestoreAdmission(ctx, lease, roots, operator, runtime, operatorPath, operatorEnvironment, m, i); e != nil {
		return out, e
	}
	if e = owners.Verify(ctx); e != nil {
		return out, e
	}
	if e = finalRestoreFiles(ctx, roots, runtime, r, f.TargetTrees); e != nil {
		return out, e
	}
	_, _, lastIntentSHA, lastFilesSHA, e := readRestoreRecords(r, m, runtime)
	if e != nil || lastIntentSHA != intentSHA || lastFilesSHA != filesSHA {
		return out, errors.New("POSTGRES_RESTORE_RECORD_CHANGED")
	}
	currentPost, e := readRestoreAuthority(ctx, lease, i, intentSHA, filesSHA)
	if e != nil || currentPost != post {
		return out, errors.New("POSTGRES_RESTORE_POST_IMAGE_CHANGED")
	}
	return RestoreObservation{"projectone.postgres.restore-observation.v1", r.RestoreID, intentSHA, filesSHA, r.SourceManifestSHA256, targetBinding(current), post, complete, true, false, time.Now().UTC().Format(time.RFC3339Nano), "Read-only observation of the exact committed data restoration; no mutation replay or missing completion publication. Runtime-role/served authority and Kubernetes qualification remain separate."}, nil
}

// Completion needs all current bindings again after native SQL/files/recovery.
// Identical bytes under a replacement inode do not preserve a held root identity.
// The historical source/intent is never rewritten to fit later observations.
func finalRestoreAdmission(ctx context.Context, lease *pgxpool.Conn, roots *freshRoots, operator, runtime config.Config, operatorPath, operatorEnvironment string, m Manifest, i RestoreIntent) error {
	if e := roots.Check(false); e != nil {
		return e
	}
	if i.RecordRoot.Path != i.Request.RestoreRoot || observeRestoreRoot(i.RecordRoot) != nil {
		return errors.New("POSTGRES_RESTORE_RECORD_ROOT_CHANGED")
	}
	target, e := readRestoreTarget(ctx, lease, i.Request, false)
	if e != nil || target != i.Target {
		return errors.New("POSTGRES_RESTORE_TARGET_CHANGED")
	}
	currentSource, e := loadRestoreSource(ctx, i.Request)
	if e != nil || !reflect.DeepEqual(currentSource, m) {
		return errors.New("POSTGRES_RESTORE_SOURCE_CHANGED")
	}
	if e = finalRestoreConfiguration(ctx, operator, runtime, operatorPath, operatorEnvironment, currentSource, i); e != nil {
		return e
	}
	if e = roots.Check(false); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return errors.New("POSTGRES_RESTORE_INTERRUPTED")
	}
	return nil
}

func finalRestoreConfiguration(ctx context.Context, operator, runtime config.Config, operatorPath, operatorEnvironment string, m Manifest, i RestoreIntent) error {
	latestOperator, e := config.LoadProtectedWithEnvironment(operatorPath, operatorEnvironment)
	if e != nil || !reflect.DeepEqual(latestOperator, operator) {
		return errors.New("POSTGRES_RESTORE_OPERATOR_CONFIGURATION_CHANGED")
	}
	latestRuntime, e := config.LoadProtectedWithEnvironment(i.Request.RuntimeConfigurationFile, i.Request.RuntimeEnvironmentFile)
	if e != nil || !reflect.DeepEqual(latestRuntime, runtime) {
		return errors.New("POSTGRES_RESTORE_RUNTIME_CONFIGURATION_CHANGED")
	}
	if restoreConfigurationDigest(latestRuntime) != i.ConfigurationSHA256 {
		return errors.New("POSTGRES_RESTORE_RUNTIME_CONFIGURATION_CHANGED")
	}
	if e = restoreInputBindings(ctx, latestOperator, latestRuntime, operatorPath, m, i.Request); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return errors.New("POSTGRES_RESTORE_INTERRUPTED")
	}
	return nil
}
