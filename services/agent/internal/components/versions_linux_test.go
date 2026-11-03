//go:build linux && component_integration && maintenance_integration

package components

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"testing"
	"time"
)

func TestM05ActualApprovedVersionsAndBoundedFlags(t *testing.T) {
	for _, version := range []string{"1.12.0", "1.12.1"} {
		t.Run(version, func(t *testing.T) {
			root := integrationRoot(t)
			archive := os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE")
			if version == "1.12.0" {
				archive = os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE_V120")
			}
			if archive == "" {
				t.Fatal("APPROVED_VERSION_ASSET_REQUIRED")
			}
			if _, e := extractArchiveVersion(archive, root, version); e != nil {
				t.Fatal("approved archive extraction failed", e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(root, "node_exporter"), "--web.listen-address=127.0.0.1:9100", "--collector.disable-defaults", "--collector.cpu", "--collector.meminfo", "--collector.filesystem", "--collector.uname", "--collector.filesystem.mount-points-include=^/$", "--collector.filesystem.fs-types-exclude=^(autofs|binfmt_misc|bpf|cgroup2?|configfs|debugfs|devpts|devtmpfs|fusectl|hugetlbfs|mqueue|proc|pstore|rpc_pipefs|securityfs|sysfs|tracefs)$", "--log.level=warn", "--web.max-requests=1")
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { cancel(); cmd.Wait() }()
			var metrics wire.ComponentMetrics
			var e error
			for i := 0; i < 40; i++ {
				metrics, e = ScrapeVersion(ctx, version)
				if e == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if e != nil || metrics.MemoryTotalBytes < 1 || metrics.FilesystemTotalBytes < 1 {
				t.Fatal("real configured version metrics unavailable", e)
			}
			other := "1.12.0"
			if version == other {
				other = "1.12.1"
			}
			if _, e = ScrapeVersion(ctx, other); e == nil {
				t.Fatal("wrong actual exporter version accepted")
			}
			t.Log("Official binary and bounded flags verified under explicit amd64 emulation; maintenance/systemd qualification remains separate.")
		})
	}
}

func TestM05VersionedOwnershipAndConfiguredUnitWithSyntheticManager(t *testing.T) {
	m, _ := maintenanceFixture(t)
	m.scrapeVersion = func(context.Context, string) (wire.ComponentMetrics, error) {
		return ParseMetrics([]byte(syntheticMetrics))
	}
	archive := os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE_V120")
	if archive == "" {
		t.Fatal("PREVIOUS_APPROVED_ASSET_REQUIRED")
	}
	download := func(ctx context.Context, destination string) error {
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		return DownloadToVersion(destination, f, "1.12.0")
	}
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	if e := m.installVersion(context.Background(), "1.12.0", &configuration, download); e != nil {
		t.Fatal("versioned owned install failed", e)
	}
	o := m.Observe(context.Background())
	if !o.Managed || o.State != "running" || o.Version != "1.12.0" || o.Config == nil || *o.Config != configuration {
		t.Fatal("versioned owned metadata lost")
	}
	j, e := m.readJournal()
	if e != nil {
		t.Fatal(e)
	}
	unit, e := os.ReadFile(m.unit)
	if e != nil || !bytes.Contains(unit, []byte("--log.level=warn --web.max-requests=2")) {
		t.Fatal("bounded applied flags missing", e)
	}
	if e = m.cleanup(context.Background(), j); e != nil {
		t.Fatal("versioned ownership removal failed", e)
	}
	if m.hasInstalledPaths() {
		t.Fatal("owned version files survived removal")
	}
}
