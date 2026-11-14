//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func TestActualPostgresRestoreLostCommitAcknowledgementAndReadOnlyReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	var in nativeRestoreFaultInputs
	if _, e := readPrivateDocument("/fault/inputs.json", 16<<10, &in); e != nil || in.Mode != "commit_unknown" || !validCommitFaultProxyAddress(in.ProxyAddress) {
		t.Fatal("explicit protected lost-COMMIT fixture required", e)
	}
	proxy, e := newCommitFaultProxy(ctx, in.ProxyAddress)
	if e != nil {
		t.Fatal(e)
	}
	defer proxy.close()
	f := newNativeRestoreFaultFixture(t, ctx, "commit_unknown")
	var synchronous, fsync string
	if e = f.pool.QueryRow(ctx, `SELECT current_setting('synchronous_commit'),current_setting('fsync')`).Scan(&synchronous, &fsync); e != nil || synchronous != "on" || fsync != "on" {
		t.Fatal("ordinary durable COMMIT must remain enabled", e)
	}
	if e = fixtureJSON("/fault/commit-drop-intent.json", map[string]any{"scope": in.ScopeID, "proxy_address": in.ProxyAddress, "upstream": "postgres:5432", "boundary": "Consume successful authority transaction COMMIT CommandComplete without forwarding any commit acknowledgement; close original connection", "synchronous_commit": synchronous, "fsync": fsync, "no_protocol_content_logged": true}); e != nil {
		t.Fatal(e)
	}
	output := &limitedBuffer{maximum: 1 << 20}
	diagnostic, e := os.OpenFile("/fault/commit-unknown-control.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
	select {
	case e = <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-done
		t.Fatal("lost-COMMIT original process deadline; retain state", ctx.Err())
	}
	if e == nil || command.ProcessState.ExitCode() != 1 || output.Len() != 0 {
		t.Fatal("original caller must fail without success DTO after lost acknowledgement", e)
	}
	proof := proxy.snapshot()
	if !proof.AuthorityInsertObserved || !proof.CommitRequestForwarded || !proof.BackendCommitConsumed || proof.BackendPID < 1 || proof.DroppedAcknowledgements != 1 {
		t.Fatal("actual original successful COMMIT acknowledgement must be consumed once", proof)
	}
	if e = fixtureJSON("/fault/actual-commit-drop-proof.json", proof); e != nil {
		t.Fatal(e)
	}
	if e = diagnostic.Sync(); e != nil {
		t.Fatal(e)
	}
	log, e := readPrivateBytes("/fault/commit-unknown-control.log", 1<<20)
	if e != nil || !bytes.Contains(log, []byte("INSTALLATION_LOCK_RELEASE_UNCONFIRMED")) {
		t.Fatal("original lost lock-session cleanup remains an explicit failure", e)
	}
	lease, e := f.pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	var held bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held); e != nil || !held {
		t.Fatal("independent new session must prove original lock is actually absent", e)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			var released bool
			if lease.QueryRow(cleanup, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
				t.Error("read-only observer lock release")
			}
		}
	}()
	var intent RestoreIntent
	intentRaw, e := readPrivateDocument(filepath.Join(f.request.RestoreRoot, restoreIntentName), 1<<20, &intent)
	if e != nil || intent.Target.BackendPID != proof.BackendPID {
		t.Fatal("consumed COMMIT belongs to exact original locked restore session", e)
	}
	var files RestoreFiles
	filesRaw, e := readPrivateDocument(filepath.Join(f.request.RestoreRoot, restoreFilesName), 1<<20, &files)
	if e != nil {
		t.Fatal(e)
	}
	post, e := readRestoreAuthority(ctx, lease, intent, digest(intentRaw), digest(filesRaw))
	if e != nil {
		t.Fatal("actual committed audit and exact full post-image survive lost COMMIT reply", e)
	}
	var audits, epoch, revoked, originalBackend, runtimeRole int
	if e = lease.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='operator.kubernetes_data_restored'),(SELECT auth_epoch FROM users WHERE is_platform_admin),(SELECT count(*) FROM sessions WHERE auth_epoch=1 AND revoked_at IS NOT NULL),(SELECT count(*) FROM pg_stat_activity WHERE pid=$1),(SELECT count(*) FROM pg_roles WHERE rolname=$2)`, proof.BackendPID, "m18_restored_"+in.ScopeID).Scan(&audits, &epoch, &revoked, &originalBackend, &runtimeRole); e != nil || audits != 1 || epoch != 2 || revoked != 2 || originalBackend != 0 || runtimeRole != 0 {
		t.Fatal("one actual authority commit, exact original fencing and no role/replay", e)
	}
	for _, name := range []string{restoreCompleteName, "runtime-context.json"} {
		if _, e = os.Lstat(filepath.Join(f.request.RestoreRoot, name)); !os.IsNotExist(e) {
			t.Fatal("original caller cannot publish missing completion/context after unknown COMMIT", name, e)
		}
	}
	before, e := TreeDigest(ctx, f.request.RestoreRoot, 8<<20, 100, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	trees, e := targetStoreTrees(ctx, f.runtime, f.request)
	if e != nil {
		t.Fatal(e)
	}
	var released bool
	if e = lease.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released); e != nil || !released {
		t.Fatal(e)
	}
	lockHeld = false
	readback := &limitedBuffer{maximum: 1 << 20}
	if e = runNative(ctx, f.request.Policy, f.request.Policy.ControlFile, []string{"verify-kubernetes-postgres-restore", "--config", f.operatorPath, "--request", f.requestPath}, nil, nil, readback); e != nil {
		t.Fatal("new actual native read-only reconciliation of committed unknown outcome", e)
	}
	var observation RestoreObservation
	if json.Unmarshal(readback.Bytes(), &observation) != nil || observation.RestoreID != f.request.RestoreID || observation.PostDataSHA256 != post || observation.OriginalCompletionPresent || !observation.ReadOnly || observation.CurrentLockHeld {
		t.Fatal("exact read-only committed observation, no invented completion")
	}
	if e = fixtureWrite("/fault/actual-readonly-observation.json", readback.Bytes()); e != nil {
		t.Fatal(e)
	}
	if e = runNative(ctx, f.request.Policy, f.request.Policy.ControlFile, []string{"restore-kubernetes-postgres", "--config", f.operatorPath, "--request", f.requestPath}, nil, nil, &limitedBuffer{maximum: 1 << 20}); e == nil {
		t.Fatal("original unknown operation must never resend restore/invalidation")
	}
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held); e != nil || !held {
		t.Fatal("actual observer and refused replay release their locks", e)
	}
	lockHeld = true
	currentPost, e := readRestoreAuthority(ctx, lease, intent, digest(intentRaw), digest(filesRaw))
	if e != nil || currentPost != post {
		t.Fatal("complete committed SQL remains unchanged after observer/refusal", e)
	}
	after, e := TreeDigest(ctx, f.request.RestoreRoot, 8<<20, 100, 1<<20)
	if e != nil || valueDigest(before) != valueDigest(after) {
		t.Fatal("new PID must not rewrite original intent or missing completion", e)
	}
	currentTrees, e := targetStoreTrees(ctx, f.runtime, f.request)
	if e != nil || valueDigest(trees) != valueDigest(currentTrees) || proxy.snapshot() != proof {
		t.Fatal("files/proxy proof unchanged, no additional authority COMMIT", e)
	}
	if _, e = loadRestoreSource(ctx, f.request); e != nil {
		t.Fatal("original read-only source unchanged", e)
	}
	if strings.TrimSpace(string(log)) == "" {
		t.Fatal("retain original failure diagnostics")
	}
	if e = fixtureJSON("/fault/actual-commit-unknown-receipt.json", map[string]any{"status": "passed_actual_lost_commit_ack_and_native_readonly_reconciliation", "scope": in.ScopeID, "database": f.request.TargetDatabase, "manifest_sha256": f.request.SourceManifestSHA256, "control_sha256": f.request.Policy.ControlSHA256, "original_control_exit": 1, "original_diagnostic_sha256": digest(log), "original_lock_release_unconfirmed_retained": true, "proxy_proof": proof, "post_data_sha256": post, "committed_audits": audits, "epoch": epoch, "revoked_original_sessions": revoked, "runtime_role_absent": true, "original_backend_absent": true, "record": before, "target_trees": trees, "readonly_observation": observation, "source_readonly_unchanged": true, "limitations": "Actual local plaintext isolated PostgreSQL commit-ack loss; original caller remains failed. No missing completion publication/replay/current runtime role or Kubernetes/CSI/remaining M18 qualification."}); e != nil {
		t.Fatal(e)
	}
}
