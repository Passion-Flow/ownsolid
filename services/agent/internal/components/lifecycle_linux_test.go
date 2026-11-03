//go:build linux && component_integration

package components

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"testing"
	"time"
)

// This service-manager double is test-only. It does not establish systemd qualification.
type syntheticSystemd struct {
	state         string
	starts, stops int
	failStart     bool
}

func (s *syntheticSystemd) Reload(context.Context) error { return nil }
func (s *syntheticSystemd) Start(context.Context) error {
	s.starts++
	if s.failStart {
		return errors.New("COMPONENT_SERVICE_FAILED")
	}
	s.state = "running"
	return nil
}
func (s *syntheticSystemd) Stop(context.Context) error            { s.stops++; s.state = "stopped"; return nil }
func (s *syntheticSystemd) Disable(context.Context) error         { return nil }
func (s *syntheticSystemd) State(context.Context) (string, error) { return s.state, nil }
func integrationRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("ISOLATED_ROOT_CONTAINER_REQUIRED")
	}
	root, e := os.MkdirTemp("/opt", "projectone-component-test-")
	if e != nil {
		t.Fatal(e)
	}
	if os.Chmod(root, 0755) != nil {
		t.Fatal("test root permission failed")
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return root
}
func testManager(t *testing.T) (*Manager, *syntheticSystemd, func(context.Context, string) error) {
	t.Helper()
	root := integrationRoot(t)
	journal := filepath.Join(root, "state")
	files := filepath.Join(root, "files")
	units := filepath.Join(root, "units")
	for _, p := range []string{journal, files, units} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	s := &syntheticSystemd{state: "stopped"}
	m := &Manager{config: Config{RootDirectory: files, ServiceUser: "nobody"}, journalDirectory: journal, unit: filepath.Join(units, unitName), service: s, scrape: func(context.Context) (wire.ComponentMetrics, error) { return ParseMetrics([]byte(syntheticMetrics)) }}
	archive := os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE")
	if archive == "" {
		t.Fatal("PINNED_COMPONENT_ARCHIVE_REQUIRED")
	}
	download := func(ctx context.Context, path string) error {
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		return DownloadTo(path, f)
	}
	return m, s, download
}
func TestM04LinuxOwnedLifecycleWithSyntheticServiceManager(t *testing.T) {
	m, s, download := testManager(t)
	ctx := context.Background()
	for _, action := range []string{"install", "install", "stop", "start", "observe", "remove", "remove"} {
		o, e := m.Execute(ctx, wire.Command(action), download)
		if e != nil {
			t.Fatalf("%s failed: %v", action, e)
		}
		if action == "remove" && (o.State != "not_installed" || o.Managed) {
			t.Fatal("removed component still managed")
		}
	}
	if s.starts != 3 || s.stops < 1 {
		t.Fatal("actual test lifecycle not executed")
	}
	if e := os.WriteFile(m.unit, []byte("unowned service sentinel"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Execute(ctx, wire.Command("install"), download); e == nil || e.Error() != "COMPONENT_CONFLICT" {
		t.Fatal("unowned unit adopted", e)
	}
	b, _ := os.ReadFile(m.unit)
	if string(b) != "unowned service sentinel" {
		t.Fatal("unowned unit modified")
	}
}
func TestM04LinuxInterruptedInstallRecoveryAndUnownedData(t *testing.T) {
	m, s, download := testManager(t)
	ctx := context.Background()
	s.failStart = true
	if _, e := m.Execute(ctx, wire.Command("install"), download); e == nil {
		t.Fatal("failed systemd action reported success")
	}
	s.failStart = false
	if e := m.Recover(ctx); e != nil {
		t.Fatal("interrupted install not recovered", e)
	}
	if !os.IsNotExist(unwrapStat(m.final())) || !os.IsNotExist(unwrapStat(m.unit)) {
		t.Fatal("new failed effects survived recovery")
	}
	if _, e := m.Execute(ctx, wire.Command("install"), download); e != nil {
		t.Fatal(e)
	}
	sentinel := filepath.Join(m.final(), "unowned-sentinel")
	if e := os.WriteFile(sentinel, []byte("preserve"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Execute(ctx, wire.Command("remove"), download); e == nil || e.Error() != "COMPONENT_CONFLICT" {
		t.Fatal("unowned added file ignored", e)
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "preserve" {
		t.Fatal("unowned file deleted")
	}
}
func TestM04ActualUpstreamBinaryMetricsInIsolatedLinux(t *testing.T) {
	root := integrationRoot(t)
	archive := os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE")
	if archive == "" {
		t.Fatal("PINNED_COMPONENT_ARCHIVE_REQUIRED")
	}
	files, e := extractArchive(archive, root)
	if e != nil || len(files) != 3 {
		t.Fatal("actual pinned archive extraction failed", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(root, "node_exporter"), "--web.listen-address=127.0.0.1:9100", "--collector.disable-defaults", "--collector.cpu", "--collector.meminfo", "--collector.filesystem", "--collector.uname", "--collector.filesystem.mount-points-include=^/$", "--collector.filesystem.fs-types-exclude=^(autofs|binfmt_misc|bpf|cgroup2?|configfs|debugfs|devpts|devtmpfs|fusectl|hugetlbfs|mqueue|proc|pstore|rpc_pipefs|securityfs|sysfs|tracefs)$")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cancel(); cmd.Wait() }()
	var metrics wire.ComponentMetrics
	for i := 0; i < 40; i++ {
		metrics, e = Scrape(ctx)
		if e == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if e != nil || metrics.MemoryTotalBytes < 1 || metrics.FilesystemTotalBytes < 1 || metrics.CPUSecondsTotal <= 0 {
		t.Fatal("actual upstream metrics unavailable", e)
	}
	t.Log("Actual Node Exporter 1.12.1 amd64 binary and real isolated-kernel metrics verified under Linux emulation; systemd and physical amd64 qualification remain pending.")
}

func TestM04LinuxRecoveryOfPartialDownloadAndStagedUnit(t *testing.T) {
	m, _, _ := testManager(t)
	stage, e := os.MkdirTemp(m.config.RootDirectory, ".node-exporter-stage-")
	if e != nil {
		t.Fatal(e)
	}
	j := journal{Phase: "installing", Staging: stage, UnitStaging: m.unit + ".staging-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Ownership: ownership{Version: wire.NodeExporterVersion, ArchiveSHA256: wire.NodeExporterSHA256, ServiceUser: m.config.ServiceUser, UnitSHA256: bytesHash(m.unitBytes())}}
	if e = m.save(j); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{j.UnitStaging, filepath.Join(stage, "node_exporter"), filepath.Join(m.journalDirectory, ".node-exporter-download.tar.gz")} {
		if e = os.WriteFile(p, []byte("synthetic interrupted write"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = m.Recover(context.Background()); e != nil {
		t.Fatal("partial effects were not safely recovered", e)
	}
	for _, p := range []string{j.UnitStaging, stage, m.journalPath(), filepath.Join(m.journalDirectory, ".node-exporter-download.tar.gz")} {
		if !os.IsNotExist(unwrapStat(p)) {
			t.Fatal("partial owned file survived recovery")
		}
	}
}
func TestM04LinuxCancelledCommandCannotStartNewEffects(t *testing.T) {
	m, s, download := testManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := m.Execute(ctx, wire.Command("install"), download)
	if e == nil || e.Error() != "COMPONENT_CANCELLED" || s.starts != 0 || s.stops != 0 || !os.IsNotExist(unwrapStat(m.journalPath())) {
		t.Fatal("cancelled task started effects", e)
	}
}

// Re-enrollment uses a new private identity directory, retaining a byte-identical
// trusted component journal. The service manager remains an explicit test double.
func TestM04RetainedJournalAcrossDeliberateIdentityDirectoryChange(t *testing.T) {
	m, _, download := testManager(t)
	ctx := t.Context()
	if _, e := m.Execute(ctx, wire.Command("install"), download); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(m.journalPath())
	if e != nil {
		t.Fatal(e)
	}
	next := filepath.Join(integrationRoot(t), "new-identity-state")
	if e = os.Mkdir(next, 0700); e != nil {
		t.Fatal(e)
	}
	clone := *m
	clone.journalDirectory = next
	if e = os.WriteFile(clone.journalPath(), before, 0600); e != nil {
		t.Fatal(e)
	}
	if e = clone.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	observation := clone.Observe(ctx)
	if !observation.Managed || observation.State != "running" {
		t.Fatal("retained ownership not reconciled", observation.ErrorCode)
	}
	unchanged, e := os.ReadFile(m.journalPath())
	if e != nil || string(unchanged) != string(before) {
		t.Fatal("original recovery journal changed", e)
	}
	if _, e = clone.Execute(ctx, wire.Command("remove"), download); e != nil {
		t.Fatal("new identity directory cannot reconcile/remove owned effects", e)
	}
}
