//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Reuse the existing complete input-before-SQL producer and fresh source owner.
// This initial real restore fixture deliberately retains empty business stores;
// the populated artifact/HTTP/Kubernetes cases have separate required evidence.
func TestActualPostgresFreshRestoreNativeCLIAndReadOnlyReconciliation(t *testing.T) {
	TestActualPostgresMatchedBackupNativeCLIAndReadback(t)
	actualPostgresRestoreFromPreparedSource(t, 180*time.Second, false)
}

// A populated caller supplies the already initialized source directly, without
// executing or reclassifying the unchanged empty-store backup case.
func actualPostgresRestoreFromPreparedSource(t *testing.T, deadline time.Duration, populated bool) {
	actualPostgresRestoreWithAuthorityCustody(t, deadline, populated, false)
}

// Only the distinct auth/fault profile retains exact original credentials.
// Historical populated evidence had no raw-token handoff and stays unchanged.
func actualPostgresRestoreWithAuthorityCustody(t *testing.T, deadline time.Duration, populated, retainAuthority bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	root := "/fixture"
	source, _, e := LoadRequest(filepath.Join(root, "request-native.json"), "")
	if e != nil {
		t.Fatal(e)
	}
	opPath := filepath.Join(root, "operator.yaml")
	operator, e := config.LoadProtected(opPath)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := config.LoadProtected(source.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := readPrivateBytes(operator.Database.DSNFile, 16<<10)
	if e != nil {
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil {
		t.Fatal("owned source DSN")
	}
	pc.MaxConns = 3
	pc.MinConns = 0
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var sourceHeld bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&sourceHeld); e != nil || !sourceHeld {
		lease.Release()
		t.Fatal("source exact lease", e)
	}
	defer func() {
		if !sourceHeld {
			return
		}
		var released bool
		if lease.QueryRow(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
			t.Error("source lease release")
		}
		lease.Release()
	}()
	accounts := &identity.Service{DB: pool, Config: runtime}
	var actor string
	var epoch int64
	if e = pool.QueryRow(ctx, "SELECT id::text,auth_epoch FROM public.users WHERE is_platform_admin AND status='active'").Scan(&actor, &epoch); e != nil {
		t.Fatal(e)
	}
	tokens := map[string]string{}
	for _, audience := range []string{"portal", "admin"} {
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		issued, e := accounts.IssueSession(ctx, tx, actor, epoch, audience, "", uuid.NewString(), nil)
		if e != nil {
			tx.Rollback(ctx)
			t.Fatal("real original session issuance", e)
		}
		if retainAuthority {
			record := retainedOriginalAuthority{Audience: audience, Token: issued.Token, SessionID: issued.SessionID, UserID: actor, Epoch: epoch, ExpiresAt: issued.ExpiresAt.UTC().Format(time.RFC3339Nano)}
			if e = fixtureJSON(filepath.Join(root, "original-authority-"+audience+"-private.json"), record); e != nil {
				_ = tx.Rollback(ctx)
				t.Fatal("private original credential custody before commit/backup", e)
			}
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal("original session commit unknown")
		}
		if retainAuthority {
			if e = fixtureJSON(filepath.Join(root, "original-authority-"+audience+"-committed.json"), map[string]any{"audience": audience, "session_id": issued.SessionID, "user_id": actor, "epoch": epoch, "token_sha256": digest([]byte(issued.Token)), "committed_before_backup": true}); e != nil {
				t.Fatal("original credential commit receipt; never resend", e)
			}
		}
		tokens[audience] = issued.Token
		if _, e = accounts.Authenticate(ctx, issued.Token, audience); e != nil {
			t.Fatal("original session current before backup", e)
		}
	}
	source.BackupID = strings.ReplaceAll(uuid.NewString(), "-", "")
	source.BackupRoot = filepath.Join(root, "backups", "authority-source")
	write := func(path string, value any) {
		t.Helper()
		raw, e := canonical(value)
		if e != nil || os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("private fixture document")
		}
	}
	sourceRequest := filepath.Join(root, "request-authority-source.json")
	write(sourceRequest, source)
	var backup Completion
	if source.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile {
		// Seed authority is committed before the actual packaged writer starts.
		// Its own original installation session stays held throughout Backup;
		// this parent's later lease is explicitly an independent source observer.
		var released bool
		if lease.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
			t.Fatal("source seed lease release unconfirmed")
		}
		sourceHeld = false
		lease.Release()
		response := &limitedBuffer{maximum: source.Policy.MaximumManifestBytes}
		if e = runNative(ctx, source.Policy, source.Policy.ControlFile, []string{"backup-kubernetes-postgres", "--config", opPath, "--request", sourceRequest}, nil, nil, response); e != nil {
			t.Fatal("actual packaged MVP native backup", e)
		}
		if e = json.Unmarshal(response.Bytes(), &backup); e != nil || backup.Format != "projectone.postgres.backup-complete.v1" || !release.ValidSHA256(backup.ManifestSHA256) {
			t.Fatal("actual packaged backup completion", e)
		}
		observation, observeError := ObserveBackup(ctx, source, backup.ManifestSHA256)
		if e = observeError; e != nil || observation.NativePostgreSQL == nil || !observation.NativePostgreSQL.Valid() {
			t.Fatal("independent complete packaged backup readback", e)
		}
		if e = fixtureJSON(filepath.Join(root, "observer-native-backup.json"), observation); e != nil {
			t.Fatal("safe native backup observation receipt", e)
		}
		lease, e = pool.Acquire(ctx)
		if e != nil {
			t.Fatal("independent source observer", e)
		}
		var observerHeld bool
		if lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&observerHeld) != nil || !observerHeld {
			lease.Release()
			t.Fatal("independent source observer lease")
		}
		sourceHeld = true
	} else {
		backup, e = Backup(ctx, pool, lease, operator, runtime, opPath, sourceRequest, "", source)
		if e != nil {
			t.Fatal("actual original-authority matched backup", e)
		}
	}
	before, e := readDatabase(ctx, lease, source)
	if e != nil {
		t.Fatal(e)
	}
	targetScope := strings.ReplaceAll(uuid.NewString(), "-", "")
	r := release.PostgreSQLRestoreRequest{FormatVersion: 1, RestoreID: strings.ReplaceAll(uuid.NewString(), "-", ""), TargetScopeID: targetScope, SourceManifestSHA256: backup.ManifestSHA256, BackupRoot: source.BackupRoot, RestoreRoot: filepath.Join(root, "restores", targetScope), TargetDatabase: "m18_restore_" + targetScope + "_test", TargetOperator: source.Operator, ConfigurationRoot: filepath.Join(root, "target-configuration"), Reason: "Actual isolated synthetic fresh-target restoration", Policy: source.Policy}
	r.ApplicationProfile = source.ApplicationProfile
	for _, name := range []string{"target", "restores"} {
		if e = os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = copyTree(ctx, source.ConfigurationRoot, r.ConfigurationRoot, r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
		t.Fatal(e)
	}
	opRaw, e := yaml.Marshal(operator)
	if e != nil {
		t.Fatal(e)
	}
	opRaw = []byte(strings.ReplaceAll(string(opRaw), source.ConfigurationRoot, r.ConfigurationRoot))
	targetOperator, e := config.Decode(opRaw)
	if e != nil {
		t.Fatal(e)
	}
	targetOperator.Database.DSNFile = filepath.Join(root, "target-operator-dsn")
	targetOperator.Models.ArtifactRoot = filepath.Join(root, "target", "models")
	targetOperator.Datasets.ArtifactRoot = filepath.Join(root, "target", "datasets")
	targetOperator.Training.ArtifactRoot = filepath.Join(root, "target", "training")
	targetOperator.ModelPreparation.ArtifactRoot = filepath.Join(root, "target", "preparations")
	for _, path := range storeRoots(targetOperator) {
		if e = os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if r.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile && operator.ImageRegistry != nil {
		registry := *operator.ImageRegistry
		registry.StoreRoot = filepath.Join(root, "target", "registry")
		registry.CAFile = strings.ReplaceAll(registry.CAFile, source.ConfigurationRoot, r.ConfigurationRoot)
		registry.CredentialFile = strings.ReplaceAll(registry.CredentialFile, source.ConfigurationRoot, r.ConfigurationRoot)
		registry.Verification.TrustFile = strings.ReplaceAll(registry.Verification.TrustFile, source.ConfigurationRoot, r.ConfigurationRoot)
		if e = os.Mkdir(registry.StoreRoot, 0700); e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(registry)
		if e != nil || os.WriteFile(targetOperator.ImageRegistryConfigFile, raw, 0600) != nil {
			t.Fatal("fresh typed target registry configuration", e)
		}
	}
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil {
		t.Fatal("owned source URI invalid")
	}
	u.Path = "/" + r.TargetDatabase
	if e = os.WriteFile(targetOperator.Database.DSNFile, []byte(u.String()), 0600); e != nil {
		t.Fatal(e)
	}
	targetOpPath := filepath.Join(root, "target-operator.yaml")
	opRaw, _ = yaml.Marshal(targetOperator)
	if e = os.WriteFile(targetOpPath, opRaw, 0600); e != nil {
		t.Fatal(e)
	}
	targetRuntime := targetOperator
	targetRuntime.Database.DSNFile = filepath.Join(root, "target-runtime-dsn")
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u.User = url.UserPassword("m18_restored_"+targetScope, credential)
	if e = os.WriteFile(targetRuntime.Database.DSNFile, []byte(u.String()), 0600); e != nil {
		t.Fatal(e)
	}
	r.RuntimeConfigurationFile = filepath.Join(r.ConfigurationRoot, "runtime.yaml")
	opRaw, _ = yaml.Marshal(targetRuntime)
	if e = os.WriteFile(r.RuntimeConfigurationFile, opRaw, 0600); e != nil {
		t.Fatal(e)
	}
	targetOperator, e = config.LoadProtected(targetOpPath)
	if e != nil {
		t.Fatal(e)
	}
	targetRuntime, e = config.LoadProtected(r.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = AdmitRestore(ctx, targetOperator, targetRuntime, targetOpPath, r, true); e != nil {
		t.Fatal("all target inputs before database creation", e)
	}
	if _, e = databaseaccess.LoadRuntime(r.RuntimeConfigurationFile); e != nil {
		t.Fatal("existing target runtime producer before SQL", e)
	}
	// This connection belongs exclusively to this fresh, unexposed synthetic
	// fixture server. No real/customer database or source database is modified.
	adminConfig := pc.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	adminConfig.MaxConns = 1
	admin, e := pgxpool.NewWithConfig(ctx, adminConfig)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec(ctx, `CREATE DATABASE "`+r.TargetDatabase+`" TEMPLATE template0`); e != nil {
		admin.Close()
		t.Fatal("fresh target create outcome", e)
	}
	if _, e = admin.Exec(ctx, `COMMENT ON DATABASE "`+r.TargetDatabase+`" IS 'projectone:kubernetes-restore:`+targetScope+`'`); e != nil {
		admin.Close()
		t.Fatal("fresh target marker", e)
	}
	admin.Close()
	requestPath := filepath.Join(root, "request-restore.json")
	write(requestPath, r)
	response := &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, []string{"restore-kubernetes-postgres", "--config", targetOpPath, "--request", requestPath}, nil, nil, response); e != nil {
		t.Fatal("actual current native fresh restore", e)
	}
	var complete RestoreCompletion
	if json.Unmarshal(response.Bytes(), &complete) != nil || complete.RestoreID != r.RestoreID || complete.CurrentLockHeld {
		t.Fatal("restore completion DTO")
	}
	if r.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile {
		assertActualMVPRestoreReference(t, ctx, r, targetOperator, targetRuntime, targetOpPath, requestPath, true)
	}
	response = &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	verifyArgs := []string{"verify-kubernetes-postgres-restore", "--config", targetOpPath, "--request", requestPath}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, verifyArgs, nil, nil, response); e != nil {
		t.Fatal("new native PID read-only reconciliation", e)
	}
	var observation RestoreObservation
	if json.Unmarshal(response.Bytes(), &observation) != nil || !observation.ReadOnly || !observation.OriginalCompletionPresent || observation.PostDataSHA256 != complete.PostDataSHA256 {
		t.Fatal("read-only observation DTO")
	}
	// Preserve the original durable bytes while testing observation of an
	// absent completion. This is an explicit fixture loss injection, not a
	// claim that the original process was killed at an unknown COMMIT boundary.
	completionPath := filepath.Join(r.RestoreRoot, restoreCompleteName)
	if e = os.Rename(completionPath, filepath.Join(root, "retained-original-restore-complete.json")); e != nil {
		t.Fatal(e)
	}
	response = &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, verifyArgs, nil, nil, response); e != nil {
		t.Fatal("committed data with missing completion observation", e)
	}
	if json.Unmarshal(response.Bytes(), &observation) != nil || observation.OriginalCompletionPresent || !observation.ReadOnly {
		t.Fatal("missing completion fabricated")
	}
	if _, e = os.Lstat(completionPath); !os.IsNotExist(e) {
		t.Fatal("read-only observer republished completion")
	}
	if r.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile {
		assertActualMVPRestoreReference(t, ctx, r, targetOperator, targetRuntime, targetOpPath, requestPath, false)
	}
	// Actual native observer must reject an equal-byte replacement inode.
	modelRoot := targetRuntime.Models.ArtifactRoot
	if e = os.Rename(modelRoot, modelRoot+"-retained-original"); e != nil {
		t.Fatal(e)
	}
	if _, e = copyTree(ctx, modelRoot+"-retained-original", modelRoot, r.Policy.MaximumStoreBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
		t.Fatal(e)
	}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, verifyArgs, nil, nil, &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}); e == nil {
		t.Fatal("native observer accepted equal-byte root replacement")
	}
	if e = os.Rename(modelRoot, modelRoot+"-rejected-copy"); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(modelRoot+"-retained-original", modelRoot); e != nil {
		t.Fatal(e)
	}
	response = &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, verifyArgs, nil, nil, response); e != nil {
		t.Fatal("original inode observation after fixture injection removed", e)
	}
	if e = runNative(ctx, r.Policy, r.Policy.ControlFile, []string{"restore-kubernetes-postgres", "--config", targetOpPath, "--request", requestPath}, nil, nil, &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}); e == nil {
		t.Fatal("restore replay accepted")
	}
	targetPC := pc.Copy()
	targetPC.ConnConfig.Database = r.TargetDatabase
	targetPool, e := pgxpool.NewWithConfig(ctx, targetPC)
	if e != nil {
		t.Fatal(e)
	}
	defer targetPool.Close()
	targetLease, e := targetPool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var held bool
	if targetLease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		targetLease.Release()
		t.Fatal("target lease")
	}
	input, e := databaseaccess.LoadRuntime(r.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	status, e := databaseaccess.PrepareRestored(ctx, targetPool, targetLease, input, complete.RuntimeContext)
	if e != nil || status != databaseaccess.Verified {
		t.Fatal("real existing restored-role owner", status, e)
	}
	if status, e = databaseaccess.Check(ctx, targetPool, targetLease, input); e != nil || status != databaseaccess.Verified {
		t.Fatal("actual target role/grants", status, e)
	}
	if targetLease.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		t.Fatal("target lease release")
	}
	targetLease.Release()
	runtimeRaw, e := readPrivateBytes(targetRuntime.Database.DSNFile, 16<<10)
	if e != nil {
		t.Fatal(e)
	}
	runtimePC, e := pgxpool.ParseConfig(strings.TrimSpace(string(runtimeRaw)))
	if e != nil {
		t.Fatal("owned target runtime DSN invalid")
	}
	runtimePC.MaxConns = 2
	runtimePC.MinConns = 0
	runtimePool, e := pgxpool.NewWithConfig(ctx, runtimePC)
	if e != nil {
		t.Fatal(e)
	}
	defer runtimePool.Close()
	restoredAccounts := &identity.Service{DB: runtimePool, Config: targetRuntime}
	// Compare source before the explicit authentication probes, whose ordinary
	// session-touch policy can legitimately update last_seen_at after its delay.
	after, e := readDatabase(ctx, lease, source)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("source changed during target restore", e)
	}
	for audience, token := range tokens {
		if _, e = accounts.Authenticate(ctx, token, audience); e != nil {
			t.Fatal("source original session changed", e)
		}
		if _, e = restoredAccounts.Authenticate(ctx, token, audience); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("restored restricted role did not return exact original-session refusal", audience, e)
		}
	}
	scope := "Linux arm64 PG18.6/releases22/86 initialized empty business stores; two original actual sessions still valid on source and rejected by restored restricted-role account owner"
	if populated {
		scope = "Linux arm64 PG18.6/releases22/86 nonempty approved base and derived models, dataset source/publication, checkpoint plus adapter and preparation report/model; synthetic metadata with retained actual CPU bytes, current role and exact original-session rejection"
	}
	if r.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile {
		scope = "Native Linux PG18.6/MVP29/108 initialized four business stores plus real locally signed tiny OCI supplementary files, fresh SQL/CAS, original role and exact original-session rejection; no populated models/current installed image/HTTP qualification"
	}
	write(filepath.Join(root, "actual-restore-receipt.json"), map[string]any{"status": "actual_native_fresh_restore_readonly_reconciliation_existing_role_and_old_sessions_rejected", "restore_id": r.RestoreID, "target_scope": targetScope, "database": r.TargetDatabase, "manifest_sha256": r.SourceManifestSHA256, "post_data_sha256": complete.PostDataSHA256, "scope": scope, "limitations": "Populated artifacts, served HTTP/cookies, cancellation/lost commit, Kubernetes/CSI and whole M18 remain required."})
}
