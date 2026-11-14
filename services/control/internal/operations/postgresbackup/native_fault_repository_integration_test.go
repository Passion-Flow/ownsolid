//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

type nativeRestoreFaultInputs struct {
	Format                 string `json:"format"`
	Mode                   string `json:"mode"`
	ScopeID                string `json:"scope_id"`
	SourceManifestSHA256   string `json:"source_manifest_sha256"`
	SourceCompletionSHA256 string `json:"source_completion_sha256"`
	ControlSHA256          string `json:"control_sha256"`
	MaximumFileBytes       uint64 `json:"maximum_file_bytes,omitempty"`
	ProxyAddress           string `json:"proxy_address,omitempty"`
}

type nativeRestoreFaultFixture struct {
	request                   release.PostgreSQLRestoreRequest
	runtime                   config.Config
	operatorPath, requestPath string
	pool                      *pgxpool.Pool
	manifest                  Manifest
	inputs                    nativeRestoreFaultInputs
}

// Only a new private target is created. The retained source is mounted read-only;
// no backup, source authority mutation, previous restore or HTTP case is rerun.
func newNativeRestoreFaultFixture(t *testing.T, ctx context.Context, mode string) nativeRestoreFaultFixture {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Geteuid() != 65534 {
		t.Fatal("exact native Linux arm64 non-root fault fixture required")
	}
	var in nativeRestoreFaultInputs
	if _, e := readPrivateDocument("/fault/inputs.json", 16<<10, &in); e != nil || in.Format != "projectone.m18.native-restore-fault.v1" || in.Mode != mode || mode != "sql_cancel" && mode != "file_limit" && mode != "commit_unknown" || !view.ValidScopeID(in.ScopeID) {
		t.Fatal("protected exact fault input required", e)
	}
	if mode != "file_limit" && in.MaximumFileBytes != 0 || mode == "file_limit" && (in.MaximumFileBytes < 128<<10 || in.MaximumFileBytes > 1<<20) {
		t.Fatal("explicit per-child fault size contract required")
	}
	if mode == "commit_unknown" && !validCommitFaultProxyAddress(in.ProxyAddress) || mode != "commit_unknown" && in.ProxyAddress != "" {
		t.Fatal("explicit isolated loopback proxy contract required")
	}
	for _, sha := range []string{in.SourceManifestSHA256, in.SourceCompletionSHA256, in.ControlSHA256} {
		if !release.ValidSHA256(sha) {
			t.Fatal("fault input fingerprint required")
		}
	}
	old, _, e := LoadRestoreRequest("/fixture/request-restore.json", "")
	if e != nil || old.SourceManifestSHA256 != in.SourceManifestSHA256 || old.Policy.ControlSHA256 != in.ControlSHA256 {
		t.Fatal("original source binding", e)
	}
	var complete RestoreCompletion
	raw, e := readPrivateDocument("/fixture/retained-original-restore-complete.json", 1<<20, &complete)
	if e != nil || digest(raw) != in.SourceCompletionSHA256 || complete.ManifestSHA256 != in.SourceManifestSHA256 {
		t.Fatal("original native completion binding", e)
	}
	var originalIntent RestoreIntent
	if _, e = readPrivateDocument(filepath.Join(old.RestoreRoot, restoreIntentName), 1<<20, &originalIntent); e != nil || originalIntent.Request.TargetScopeID != old.TargetScopeID {
		t.Fatal("original server identity", e)
	}
	m, e := loadRestoreSource(ctx, old)
	if e != nil {
		t.Fatal("retained complete source readback", e)
	}
	operator, e := config.LoadProtected("/fixture/operator.yaml")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"scratch", "targets", "restores"} {
		if e = os.Mkdir(filepath.Join("/fault", name), 0700); e != nil {
			t.Fatal("exclusive fresh target setup", e)
		}
	}
	configuration := "/fault/configuration"
	if _, e = copyTree(ctx, m.Request.ConfigurationRoot, configuration, old.Policy.MaximumConfigurationBytes, old.Policy.MaximumStoreFiles, old.Policy.MaximumManifestBytes, old.Policy.MinimumFreeBytes); e != nil {
		t.Fatal(e)
	}
	raw, e = yaml.Marshal(operator)
	if e != nil {
		t.Fatal(e)
	}
	operator, e = config.Decode([]byte(strings.ReplaceAll(string(raw), m.Request.ConfigurationRoot, configuration)))
	if e != nil {
		t.Fatal(e)
	}
	r := old
	r.RestoreID = strings.ReplaceAll(uuid.NewString(), "-", "")
	r.TargetScopeID = in.ScopeID
	r.TargetDatabase = "m18_restore_" + in.ScopeID + "_test"
	r.RestoreRoot = filepath.Join("/fault/restores", in.ScopeID)
	r.ConfigurationRoot = configuration
	r.RuntimeConfigurationFile = filepath.Join(configuration, "runtime.yaml")
	r.RuntimeEnvironmentFile = ""
	r.Policy.ScratchRoot = "/fault/scratch"
	r.Reason = "Actual isolated native restore " + mode + ", preserve failed target"
	if mode == "sql_cancel" {
		r.Reason = "Actual isolated native SQL cancellation, preserve failed target"
	}
	operator.Database.DSNFile = "/fault/operator-dsn"
	operator.Models.ArtifactRoot = "/fault/targets/models"
	operator.Datasets.ArtifactRoot = "/fault/targets/datasets"
	operator.Training.ArtifactRoot = "/fault/targets/training"
	operator.ModelPreparation.ArtifactRoot = "/fault/targets/preparations"
	for _, path := range storeRoots(operator) {
		if e = os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	raw, e = readPrivateBytes("/fixture/source-dsn", 16<<10)
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil || u.Host != "postgres:5432" || u.User.Username() != r.TargetOperator || u.Query().Get("sslmode") != "disable" {
		t.Fatal("exact isolated existing fixture endpoint required")
	}
	u.Path = "/" + r.TargetDatabase
	if mode == "commit_unknown" {
		// This explicitly selected target endpoint is bound into both DSNs and
		// immutable configuration/intent, not a transparent endpoint substitution.
		u.Host = in.ProxyAddress
	}
	if e = fixtureWrite(operator.Database.DSNFile, []byte(u.String())); e != nil {
		t.Fatal(e)
	}
	runtimeConfig := operator
	runtimeConfig.Database.DSNFile = "/fault/runtime-dsn"
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u.User = url.UserPassword("m18_restored_"+in.ScopeID, credential)
	if e = fixtureWrite(runtimeConfig.Database.DSNFile, []byte(u.String())); e != nil {
		t.Fatal(e)
	}
	writeYAML := func(path string, value config.Config) {
		t.Helper()
		raw, e := yaml.Marshal(value)
		if e != nil {
			t.Fatal("fixture configuration serialization", e)
		}
		if e = fixtureWrite(path, raw); e != nil {
			t.Fatal("exclusive protected fixture configuration", path, e)
		}
	}
	opPath, requestPath := "/fault/operator.yaml", "/fault/request-restore.json"
	writeYAML(opPath, operator)
	// Preserve the copied source YAML outside the new projection before creating
	// the target-specific YAML exclusively. The original read-only copy is intact.
	if e = os.Rename(r.RuntimeConfigurationFile, "/fault/copied-source-runtime.yaml"); e != nil {
		t.Fatal("retain copied source configuration before target binding", e)
	}
	writeYAML(r.RuntimeConfigurationFile, runtimeConfig)
	operator, e = config.LoadProtected(opPath)
	if e != nil {
		t.Fatal(e)
	}
	runtimeConfig, e = config.LoadProtected(r.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = fixtureJSON(requestPath, r); e != nil {
		t.Fatal(e)
	}
	if e = AdmitRestore(ctx, operator, runtimeConfig, opPath, r, true); e != nil {
		t.Fatal("all native inputs before new SQL effects", e)
	}
	if _, e = databaseaccess.LoadRuntime(r.RuntimeConfigurationFile); e != nil {
		t.Fatal(e)
	}
	raw, e = readPrivateBytes(operator.Database.DSNFile, 16<<10)
	if e != nil {
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	pc.MaxConns, pc.MinConns = 3, 0
	adminConfig := pc.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, e := pgxpool.NewWithConfig(ctx, adminConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	var system string
	if e = admin.QueryRow(ctx, `SELECT system_identifier::text FROM pg_catalog.pg_control_system()`).Scan(&system); e != nil || system != originalIntent.Target.SystemIdentifier {
		t.Fatal("same exact owned fixture server, no substitute", e)
	}
	if e = fixtureJSON("/fault/database-create-intent.json", map[string]any{"database": r.TargetDatabase, "scope": in.ScopeID, "system_identifier": system, "native_inputs_admitted": true}); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec(ctx, `CREATE DATABASE "`+r.TargetDatabase+`" TEMPLATE template0`); e != nil {
		t.Fatal("new target create outcome; no retry", e)
	}
	if _, e = admin.Exec(ctx, `COMMENT ON DATABASE "`+r.TargetDatabase+`" IS 'projectone:kubernetes-restore:`+in.ScopeID+`'`); e != nil {
		t.Fatal("new target marker; retain uncertain setup", e)
	}
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return nativeRestoreFaultFixture{request: r, runtime: runtimeConfig, operatorPath: opPath, requestPath: requestPath, pool: pool, manifest: m, inputs: in}
}

func TestActualPostgresRestoreSQLCancellationJoinsNativeAndRefusesReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newNativeRestoreFaultFixture(t, ctx, "sql_cancel")
	blocker, e := f.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(context.Background())
	// AccessShare catalog readers still pass. Real CREATE EXTENSION/TABLE inside
	// pg_restore's single transaction must wait for its RowExclusive catalog lock.
	if _, e = blocker.Exec(ctx, `LOCK TABLE pg_catalog.pg_class IN SHARE MODE`); e != nil {
		t.Fatal(e)
	}
	var blockerPID int
	if e = blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); e != nil {
		t.Fatal(e)
	}
	output := &limitedBuffer{maximum: f.request.Policy.MaximumManifestBytes}
	diagnostic, e := os.OpenFile("/fault/cancel-control.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer diagnostic.Close()
	command := exec.Command(f.request.Policy.ControlFile, "restore-kubernetes-postgres", "--config", f.operatorPath, "--request", f.requestPath)
	command.Env = []string{"LC_ALL=C", "TZ=UTC", "TMPDIR=/fault/scratch", "GOMAXPROCS=2", "GOMEMLIMIT=256MiB"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout, command.Stderr = output, diagnostic
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	joined := false
	defer func() {
		if !joined {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()
	var nativePID, backendPID int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		nativePID = nativeRestoreChild(command.Process.Pid)
		e = f.pool.QueryRow(ctx, `SELECT pid FROM pg_catalog.pg_stat_activity WHERE datname=$1 AND application_name='projectone-m18-matched-backup' AND wait_event_type='Lock' AND $2::integer=ANY(pg_catalog.pg_blocking_pids(pid))`, f.request.TargetDatabase, blockerPID).Scan(&backendPID)
		if e == nil && nativePID > 0 {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if e != nil || nativePID < 1 || backendPID < 1 {
		t.Fatal("real native child and SQL lock wait must be observed before cancellation", e)
	}
	if e = fixtureJSON("/fault/cancel-signal-intent.json", map[string]any{"control_pid": command.Process.Pid, "native_pid": nativePID, "backend_pid": backendPID, "blocker_pid": blockerPID, "signal": "SIGTERM", "actual_blocked_native_import": true}); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	if e = command.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		joined = true
	case <-time.After(15 * time.Second):
		t.Fatal("native cancellation did not join within bounded grace")
	}
	joinedMilliseconds := time.Since(started).Milliseconds()
	if e == nil || command.ProcessState.ExitCode() != 1 || output.Len() != 0 {
		t.Fatal("cancelled Control must fail without a completion DTO", e)
	}
	if _, e = os.Stat(filepath.Join("/proc", strconv.Itoa(nativePID))); !os.IsNotExist(e) {
		t.Fatal("native child must be reaped before Control exits", e)
	}
	for time.Now().Before(deadline) {
		var count int
		if e = f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE pid=$1`, backendPID).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var remaining int
	if e = f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE pid=$1`, backendPID).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal("native backend joined while blocker remains held", e)
	}
	if e = blocker.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	var clean bool
	if e = f.pool.QueryRow(ctx, "SELECT "+emptyPostgresTarget).Scan(&clean); e != nil || !clean {
		t.Fatal("single native import transaction rolled back to fresh catalog", e)
	}
	for _, path := range storeRoots(f.runtime) {
		entries, e := os.ReadDir(path)
		if e != nil || len(entries) != 0 {
			t.Fatal("cancellation precedes file effects", e)
		}
	}
	for _, name := range []string{restoreCompleteName, restoreFilesName, "sql-import-result.json", "authority-intent.json"} {
		if _, e = os.Lstat(filepath.Join(f.request.RestoreRoot, name)); !os.IsNotExist(e) {
			t.Fatal("no fabricated import/authority/completion after cancellation", name, e)
		}
	}
	before, e := TreeDigest(ctx, f.request.RestoreRoot, 1<<20, 10, 1<<20)
	if e != nil || before.Files != 2 {
		t.Fatal("immutable import intent retained", e)
	}
	for _, action := range []string{"restore-kubernetes-postgres", "verify-kubernetes-postgres-restore"} {
		if e = runNative(ctx, f.request.Policy, f.request.Policy.ControlFile, []string{action, "--config", f.operatorPath, "--request", f.requestPath}, nil, nil, &limitedBuffer{maximum: 1 << 20}); e == nil {
			t.Fatal("partial target replay/committed reconciliation must refuse", action)
		}
		if e = f.pool.QueryRow(ctx, "SELECT "+emptyPostgresTarget).Scan(&clean); e != nil || !clean {
			t.Fatal("refused action must preserve empty SQL catalog", action, e)
		}
		for _, path := range storeRoots(f.runtime) {
			entries, e := os.ReadDir(path)
			if e != nil || len(entries) != 0 {
				t.Fatal("refused action must preserve every empty store", action, path, e)
			}
		}
		for _, name := range []string{restoreCompleteName, restoreFilesName, "sql-import-result.json", "authority-intent.json", "runtime-context.json"} {
			if _, e = os.Lstat(filepath.Join(f.request.RestoreRoot, name)); !os.IsNotExist(e) {
				t.Fatal("refused action must not publish SQL/file/authority/completion proof", action, name, e)
			}
		}
	}
	after, e := TreeDigest(ctx, f.request.RestoreRoot, 1<<20, 10, 1<<20)
	if e != nil || valueDigest(before) != valueDigest(after) {
		t.Fatal("no-resend checks must not rewrite original intent", e)
	}
	if _, e = loadRestoreSource(ctx, f.request); e != nil {
		t.Fatal("read-only retained source unchanged", e)
	}
	if e = diagnostic.Sync(); e != nil {
		t.Fatal(e)
	}
	if e = fixtureJSON("/fault/actual-cancellation-receipt.json", map[string]any{"status": "passed_actual_native_SQL_cancellation_join_rollback_and_no_resend", "scope": f.request.TargetScopeID, "database": f.request.TargetDatabase, "manifest_sha256": f.request.SourceManifestSHA256, "control_sha256": f.request.Policy.ControlSHA256, "control_exit": 1, "expected_failure": true, "native_pid": nativePID, "backend_pid": backendPID, "signal_to_join_milliseconds": joinedMilliseconds, "native_process_absent": true, "native_backend_absent": true, "target_fresh_after_rollback": clean, "stores_empty": true, "SQL_roots_and_publication_rechecked_after_each_refusal": true, "record": before, "source_readonly_unchanged": true, "limitations": "One real blocked SQL import cancellation. Partial file failure, actual lost-COMMIT, Kubernetes/CSI and remaining M18 are separate required cases."}); e != nil {
		t.Fatal(e)
	}
}

func nativeRestoreChild(parent int) int {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return 0
	}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid < 1 {
			continue
		}
		name, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		status, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if strings.TrimSpace(string(name)) != "pg_restore" {
			continue
		}
		for _, line := range strings.Split(string(status), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "PPid:" && fields[1] == strconv.Itoa(parent) {
				return pid
			}
		}
	}
	return 0
}
