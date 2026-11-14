//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

type restoreFileLimitChildIntent struct {
	ParentPID        int    `json:"parent_pid"`
	ScopeID          string `json:"scope_id"`
	RequestSHA256    string `json:"request_sha256"`
	ControlSHA256    string `json:"control_sha256"`
	MaximumFileBytes uint64 `json:"maximum_file_bytes"`
}

// This child lowers only its own Linux limit, then replaces itself with the
// unchanged actual Control executable. It never fabricates a restore response.
// It is selected only by its parent case, not counted as an independent pass.
func TestNativeRestoreFileLimitChild(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Geteuid() != 65534 {
		t.Fatal("exact native non-root file-limit child required")
	}
	var intent restoreFileLimitChildIntent
	if _, e := readPrivateDocument("/fault/file-limit-child-intent.json", 16<<10, &intent); e != nil || intent.ParentPID != os.Getppid() || intent.MaximumFileBytes < 128<<10 || intent.MaximumFileBytes > 1<<20 {
		t.Fatal("exact existing parent/file-limit intent required", e)
	}
	r, requestSHA, e := LoadRestoreRequest("/fault/request-restore.json", "")
	if e != nil || r.TargetScopeID != intent.ScopeID || requestSHA != intent.RequestSHA256 || r.Policy.ControlSHA256 != intent.ControlSHA256 {
		t.Fatal("exact prepared request/executable binding", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = trustedTool(ctx, r.Policy.ControlFile, intent.ControlSHA256); e != nil {
		t.Fatal(e)
	}
	limit := syscall.Rlimit{Cur: intent.MaximumFileBytes, Max: intent.MaximumFileBytes}
	if e = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); e != nil {
		t.Fatal("per-child file limit", e)
	}
	var actual syscall.Rlimit
	if e = syscall.Getrlimit(syscall.RLIMIT_FSIZE, &actual); e != nil || actual != limit {
		t.Fatal("actual kernel file limit", e)
	}
	if e = fixtureJSON("/fault/file-limit-applied.json", map[string]any{"pid": os.Getpid(), "parent_pid": os.Getppid(), "scope": intent.ScopeID, "maximum_file_bytes": actual.Cur, "control_sha256": intent.ControlSHA256}); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Exec(r.Policy.ControlFile, []string{r.Policy.ControlFile, "restore-kubernetes-postgres", "--config", "/fault/operator.yaml", "--request", "/fault/request-restore.json"}, []string{"LC_ALL=C", "TZ=UTC", "TMPDIR=/fault/scratch", "GOMAXPROCS=2", "GOMEMLIMIT=256MiB"}); e != nil {
		t.Fatal("execute actual unchanged Control", e)
	}
}

func TestActualPostgresRestorePartialFileFailureRetainsSQLAndRefusesReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	f := newNativeRestoreFaultFixture(t, ctx, "file_limit")
	if f.manifest.Stores[0].Tree.Bytes <= int64(f.inputs.MaximumFileBytes) {
		t.Fatal("real nonempty model payload beyond fault limit required")
	}
	var parentLimit syscall.Rlimit
	if e := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &parentLimit); e != nil {
		t.Fatal(e)
	}
	intent := restoreFileLimitChildIntent{os.Getpid(), f.request.TargetScopeID, restoreRequestDigest(f.request), f.request.Policy.ControlSHA256, f.inputs.MaximumFileBytes}
	if e := fixtureJSON("/fault/file-limit-child-intent.json", intent); e != nil {
		t.Fatal(e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	output := &limitedBuffer{maximum: 1 << 20}
	diagnostic, e := os.OpenFile("/fault/file-failure-control.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer diagnostic.Close()
	command := exec.Command(executable, "-test.run=^TestNativeRestoreFileLimitChild$")
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
		t.Fatal("file-fault Control deadline; preserve all original state", ctx.Err())
	}
	if e == nil || command.ProcessState.ExitCode() != 1 || output.Len() != 0 {
		t.Fatal("actual Control must fail normally without success DTO", e)
	}
	var afterLimit syscall.Rlimit
	if e = syscall.Getrlimit(syscall.RLIMIT_FSIZE, &afterLimit); e != nil || parentLimit != afterLimit {
		t.Fatal("parent/shared process limits must remain unchanged", e)
	}
	var applied struct {
		PID              int    `json:"pid"`
		ParentPID        int    `json:"parent_pid"`
		Scope            string `json:"scope"`
		MaximumFileBytes uint64 `json:"maximum_file_bytes"`
		ControlSHA256    string `json:"control_sha256"`
	}
	if _, e = readPrivateDocument("/fault/file-limit-applied.json", 16<<10, &applied); e != nil || applied.PID != command.Process.Pid || applied.ParentPID != os.Getpid() || applied.Scope != f.request.TargetScopeID || applied.MaximumFileBytes != f.inputs.MaximumFileBytes || applied.ControlSHA256 != f.request.Policy.ControlSHA256 {
		t.Fatal("actual same-PID kernel limit and Control binding", e)
	}
	lease, e := f.pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	var held bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held); e != nil || !held {
		t.Fatal("failed Control must release the exact installation lock", e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		var released bool
		if lease.QueryRow(cleanup, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
			t.Error("file-fault observation lease release")
		}
	}()
	readState := func() (DatabaseReadback, []TreeReadback, TreeReadback) {
		t.Helper()
		data, e := readDatabase(ctx, lease, targetContext(f.manifest, f.request))
		if e != nil || data.ContentSHA256 != f.manifest.Database.ContentSHA256 || data.Rows != f.manifest.Database.Rows || data.Tables != 86 || data.SchemaVersion != 22 {
			t.Fatal("full imported SQL remains original before authority mutation", e)
		}
		trees, e := targetStoreTrees(ctx, f.runtime, f.request)
		if e != nil || len(trees) != 4 || trees[0].Bytes == 0 {
			t.Fatal("actual partial model files must remain", e)
		}
		for _, tree := range trees[1:] {
			if tree.Bytes != 0 || tree.Files != 0 {
				t.Fatal("later store imports must not start")
			}
		}
		for _, name := range []string{restoreCompleteName, restoreFilesName, "independent-files-readback.json", "authority-intent.json", "runtime-context.json"} {
			if _, e = os.Lstat(filepath.Join(f.request.RestoreRoot, name)); !os.IsNotExist(e) {
				t.Fatal("no fabricated full-file/authority/completion publication", name, e)
			}
		}
		var audits, backends int
		if e = lease.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='operator.kubernetes_data_restored'),(SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND application_name='projectone-m18-matched-backup')`, f.request.TargetDatabase).Scan(&audits, &backends); e != nil || audits != 0 || backends != 0 {
			t.Fatal("no authority commit or leaked native backend", e)
		}
		record, e := TreeDigest(ctx, f.request.RestoreRoot, 8<<20, 30, 1<<20)
		if e != nil {
			t.Fatal(e)
		}
		return data, trees, record
	}
	data, trees, record := readState()
	partialPath, sourcePath, prefixSHA := actualPartialModelPrefix(t, ctx, f)
	var unlocked bool
	if e = lease.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&unlocked); e != nil || !unlocked {
		t.Fatal(e)
	}
	for _, action := range []string{"restore-kubernetes-postgres", "verify-kubernetes-postgres-restore"} {
		if e = runNative(ctx, f.request.Policy, f.request.Policy.ControlFile, []string{action, "--config", f.operatorPath, "--request", f.requestPath}, nil, nil, &limitedBuffer{maximum: 1 << 20}); e == nil {
			t.Fatal("partial target must refuse replay/committed reconciliation", action)
		}
		if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held); e != nil || !held {
			t.Fatal("refused action must release installation lock", e)
		}
		currentData, currentTrees, currentRecord := readState()
		if currentData.ContentSHA256 != data.ContentSHA256 || valueDigest(currentTrees) != valueDigest(trees) || valueDigest(currentRecord) != valueDigest(record) {
			t.Fatal("refused action must preserve exact SQL/partial-files/intent state", action)
		}
		if action == "restore-kubernetes-postgres" {
			if e = lease.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&unlocked); e != nil || !unlocked {
				t.Fatal(e)
			}
		}
	}
	if _, e = loadRestoreSource(ctx, f.request); e != nil {
		t.Fatal("complete original read-only source remains unchanged", e)
	}
	if e = diagnostic.Sync(); e != nil {
		t.Fatal(e)
	}
	if e = fixtureJSON("/fault/actual-file-failure-receipt.json", map[string]any{"status": "passed_actual_partial_file_failure_and_exact_no_resend", "scope": f.request.TargetScopeID, "database": f.request.TargetDatabase, "manifest_sha256": f.request.SourceManifestSHA256, "control_sha256": f.request.Policy.ControlSHA256, "control_exit": 1, "maximum_file_bytes": f.inputs.MaximumFileBytes, "partial_path": partialPath, "source_path": sourcePath, "partial_prefix_sha256": prefixSHA, "SQL": data, "target_trees": trees, "record": record, "parent_limits_unchanged": true, "exact_state_rechecked_after_each_refusal": true, "authority_commit_absent": true, "source_readonly_unchanged": true, "limitations": "Actual per-child Linux file-size write failure. Lost-COMMIT/Kubernetes/CSI/current packages/remaining M18 remain separate."}); e != nil {
		t.Fatal(e)
	}
}

func actualPartialModelPrefix(t *testing.T, ctx context.Context, f nativeRestoreFaultFixture) (string, string, string) {
	t.Helper()
	var partial string
	e := filepath.WalkDir(f.runtime.Models.ArtifactRoot, func(path string, entry fs.DirEntry, e error) error {
		if e != nil || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("partial target path invalid")
		}
		if !entry.IsDir() {
			info, e := entry.Info()
			if e != nil || !info.Mode().IsRegular() || info.Size() > int64(f.inputs.MaximumFileBytes) {
				return errors.New("partial file size contract violated")
			}
			if info.Size() == int64(f.inputs.MaximumFileBytes) {
				partial = path
			}
		}
		return nil
	})
	if e != nil || partial == "" {
		t.Fatal("actual kernel-capped nonempty partial file required", e)
	}
	raw, e := os.ReadFile(partial)
	if e != nil {
		t.Fatal(e)
	}
	prefixSHA := digest(raw)
	var matched string
	sourceRoot := filepath.Join(f.request.BackupRoot, "stores/models")
	e = filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, e error) error {
		if e != nil || ctx.Err() != nil || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("verified source path unavailable")
		}
		if entry.IsDir() || entry.Name() != filepath.Base(partial) {
			return nil
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() <= int64(len(raw)) {
			return nil
		}
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		prefix, e := io.ReadAll(io.LimitReader(file, int64(len(raw))))
		file.Close()
		if e != nil {
			return e
		}
		if digest(prefix) == prefixSHA {
			matched, e = filepath.Rel(sourceRoot, path)
		}
		return e
	})
	if e != nil || matched == "" {
		t.Fatal("partial file must be an exact prefix of actual larger source model", e)
	}
	relative, e := filepath.Rel(f.runtime.Models.ArtifactRoot, partial)
	if e != nil {
		t.Fatal(e)
	}
	return relative, matched, prefixSHA
}
