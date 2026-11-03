package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	platform "runtime"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Explicit orchestration double. It does not verify image signatures or load an
// image, and cannot establish positive Registry/Docker/target acceptance.
type registryOperatorDouble struct {
	calls               int
	operation, argument string
	failure             error
}

func (d *registryOperatorDouble) ImportOffline(_ context.Context, _ wire.RegistryOfflineManifest, p string) error {
	d.calls++
	d.operation = RegistryImportOffline
	d.argument = p
	return d.failure
}
func (d *registryOperatorDouble) ReconcileOffline(_ context.Context, _ wire.RegistryOfflineManifest) error {
	d.calls++
	d.operation = RegistryReconcileOffline
	return d.failure
}
func (d *registryOperatorDouble) ReconcileStoredTask(_ context.Context, p string) error {
	d.calls++
	d.operation = RegistryReconcileTask
	d.argument = p
	return d.failure
}

func TestRegistryOperatorSelectionAndRefusals(t *testing.T) {
	r := wire.ApprovedInferenceRecipe()
	m := wire.RegistryOfflineManifest{ReferenceID: "12345678-1234-1234-1234-123456789abc", Image: wire.RegistryImageIdentity{ReleaseProof: json.RawMessage(`{"explicit_test":true}`), PinID: "22345678-1234-1234-1234-123456789abc", ImageManifestDigest: r.ImageManifestDigest, ImageConfigDigest: r.ImageConfigDigest, ArchiveSHA256: strings.Repeat("1", 64), ArchiveBytes: 1, ExpandedBytes: 1, SourceArchiveSHA256: strings.Repeat("2", 64), SourceManifestSHA256: strings.Repeat("3", 64), OS: r.OS, Architecture: r.Architecture}}
	for _, o := range []RegistryOptions{
		{Operation: RegistryImportOffline, ManifestFile: "/private/manifest.json", ArchiveFile: "/private/image.tar"},
		{Operation: RegistryReconcileOffline, ManifestFile: "/private/manifest.json"},
		{Operation: RegistryReconcileTask, TaskID: "32345678-1234-1234-1234-123456789abc"},
	} {
		t.Run(o.Operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			d := &registryOperatorDouble{}
			if e := executeRegistryOperation(ctx, o, m, d); e != nil || d.calls != 1 || d.operation != o.Operation {
				t.Fatal("operator route changed", e, d)
			}
			d.failure = errors.New("REGISTRY_LOAD_OUTCOME_UNKNOWN")
			if e := executeRegistryOperation(ctx, o, m, d); e != d.failure {
				t.Fatal("unknown outcome lost", e)
			}
			cancel()
			before := d.calls
			if executeRegistryOperation(ctx, o, m, d) == nil || d.calls != before {
				t.Fatal("cancelled operation reached loader")
			}
			if executeRegistryOperation(context.Background(), o, m, d) == nil || d.calls != before {
				t.Fatal("unbounded operation reached loader")
			}
		})
	}
	for _, o := range []RegistryOptions{{}, {Operation: "unknown"}, {Operation: RegistryImportOffline}, {Operation: RegistryReconcileTask, TaskID: "../task"}, {Operation: RegistryReconcileTask, TaskID: m.ReferenceID, ArchiveFile: "/private/image.tar"}, {Operation: RegistryReconcileOffline, ManifestFile: "relative"}, {Operation: RegistryReconcileOffline, ManifestFile: "/private/manifest.json", ArchiveFile: "/private/image.tar"}} {
		if o.Validate() == nil {
			t.Fatal("contradictory or invalid operation accepted", o.Operation)
		}
	}
}

func TestRegistryOperationsShareAgentLockAndPlatform(t *testing.T) {
	c, _, _ := registryConfigurationFixture(t)
	o := RegistryOptions{Operation: RegistryReconcileTask, TaskID: "12345678-1234-1234-1234-123456789abc"}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if e := RunRegistryOperation(cancelled, c, o); e == nil || e.Error() != "INFERENCE_CANCELLED" {
		t.Fatal("cancelled operator admitted", e)
	}
	lock, e := state.Lock(c.StateDirectory)
	if e != nil {
		t.Fatal(e)
	}
	if e = RunRegistryOperation(context.Background(), c, o); e == nil || e.Error() != "AGENT_ALREADY_RUNNING" {
		t.Fatal("ordinary agent lock bypassed", e)
	}
	if e = lock.Close(); e != nil {
		t.Fatal(e)
	}
	e = RunRegistryOperation(context.Background(), c, o)
	if platform.GOOS != "linux" || platform.GOARCH != "amd64" || os.Geteuid() != 0 {
		if e == nil || e.Error() != "INFERENCE_PLATFORM_UNSUPPORTED" {
			t.Fatal("target restriction bypassed", e)
		}
	} else if e == nil {
		t.Fatal("nonexistent protected socket gained an operation")
	}
	// The lock must be released even after a refused operation.
	lock, e = state.Lock(c.StateDirectory)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
}
