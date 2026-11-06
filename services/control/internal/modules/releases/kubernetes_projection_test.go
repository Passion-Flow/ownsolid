package releases

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func TestKubernetesProjectionUsesActualDistinctUnprivilegedProcess(t *testing.T) {
	c, s := readerFixture(t)
	s.Format = view.KubernetesFormat
	s.Profile = "mvp-postgresql-cpu-v1-linux-amd64"
	s.Events[0].Action = "preparation_prepare_installation"
	s.Events[0].Outcome = "recorded"
	s.Kubernetes = &view.KubernetesJournalBinding{IndexFormat: view.KubernetesIndexFormat, ApplicationProfile: "mvp-postgresql-cpu-v1", SchemaVersion: 40, PreparedPlanSHA256: strings.Repeat("1", 64), LastSource: view.KubernetesSourceReference{Kind: "preparation", EventKind: "prepare_installation", OperationID: s.Events[0].OperationID, PlanSHA256: strings.Repeat("1", 64), Sequence: 1, SHA256: strings.Repeat("2", 64)}}
	if s.Validate() != nil {
		t.Fatal("invalid fixture")
	}
	if os.Geteuid() != 0 {
		// No credential elevation or new account. This process is the publisher;
		// production must refuse it. Native Linux root CI exercises the real reader.
		raw, _ := json.Marshal(s)
		os.WriteFile(filepath.Join(c.Directory, view.SnapshotFile), raw, 0444)
		if VerifyProjection(c) == nil {
			t.Fatal("publisher read its own view")
		}
		return
	}
	root, e := os.MkdirTemp("/var/tmp", "projectone-release-view-process-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		resolved, e := filepath.EvalSymlinks(root)
		if e != nil || resolved != root || !strings.HasPrefix(filepath.Base(root), "projectone-release-view-process-") {
			t.Error("owned temporary root changed")
			return
		}
		if e = os.RemoveAll(root); e != nil {
			t.Error(e)
		}
	})
	if e = os.Chmod(root, 0755); e != nil {
		t.Fatal(e)
	}
	public := filepath.Join(root, "public")
	if e = os.Mkdir(public, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(public, 0755); e != nil {
		t.Fatal(e)
	}
	c.Directory = public
	raw, _ := json.Marshal(s)
	if e = os.WriteFile(filepath.Join(public, view.SnapshotFile), raw, 0444); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Join(public, view.SnapshotFile), 0444); e != nil {
		t.Fatal(e)
	}
	policy := filepath.Join(root, "reader.json")
	raw, _ = json.Marshal(c)
	if e = os.WriteFile(policy, raw, 0444); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(policy, 0444); e != nil {
		t.Fatal(e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	source, e := os.Open(executable)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	copyPath := filepath.Join(root, "reader-test")
	target, e := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0555)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(target, source)
	closeError := target.Close()
	if e != nil || closeError != nil {
		t.Fatal(e, closeError)
	}
	if e = os.Chmod(copyPath, 0555); e != nil {
		t.Fatal(e)
	}
	run := func(wantSuccess bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, copyPath, "-test.run=^TestKubernetesProjectionProcessHelper$")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PROJECT_ONE_KUBERNETES_VIEW_READER="+policy)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{}}}
		output, e := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatal("reader helper deadline", e)
		}
		if wantSuccess && e != nil {
			t.Fatal("actual unprivileged reader refused", e, string(output))
		}
		if !wantSuccess && e == nil {
			t.Fatal("dirty view accepted by actual reader")
		}
	}
	run(true)
	if e = os.WriteFile(filepath.Join(public, view.DirtyFile), []byte("owned publication in progress\n"), 0444); e != nil {
		t.Fatal(e)
	}
	run(false)
}
func TestKubernetesProjectionProcessHelper(t *testing.T) {
	path := os.Getenv("PROJECT_ONE_KUBERNETES_VIEW_READER")
	if path == "" {
		return
	}
	raw, e := os.ReadFile(path)
	var c Config
	if e != nil || json.Unmarshal(raw, &c) != nil {
		t.Fatal("helper policy")
	}
	if os.Geteuid() != 65534 || c.PublisherUID == os.Geteuid() {
		t.Fatal("actual process identity mismatch")
	}
	e = VerifyProjection(c)
	if errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if e != nil {
		t.Fatal(e)
	}
}
