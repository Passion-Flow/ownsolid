package kubernetes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestServerJoinHoldsCanalUntilTheOfflineDefaultExists(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	f.startServer()
	if e := writeBytes(f.manager.path(canalManifest), []byte("packaged manifest")); e != nil {
		t.Fatal(e)
	}
	const schema = "stock Felix CRD"
	chart := base64.StdEncoding.EncodeToString(tarball(t, map[string]string{felixCRDPath: schema}, nil))
	var apiReady, crdCreated, defaultCreated bool
	f.host.onRun = func(name string, args []string) ([]byte, error, bool) {
		if name == "systemctl" && args[0] == "start" {
			target, e := os.Readlink(f.manager.path(canalHold))
			if e != nil || target != f.manager.journal {
				t.Fatalf("RKE2 started without the owned Canal hold: %q %v", target, e)
			}
		}
		if name != "kubectl" {
			return nil, nil, false
		}
		if !slices.Contains(args, "--request-timeout=5s") {
			if !defaultCreated {
				t.Fatal("checked the node before persisting the Canal policy")
			}
			if _, e := os.Lstat(f.manager.path(canalHold)); !errors.Is(e, os.ErrNotExist) {
				t.Fatalf("hold remained after policy was persisted: %v", e)
			}
			return nil, nil, false
		}
		if !apiReady {
			return nil, errors.New("API is starting"), true
		}
		switch {
		case slices.Contains(args, canalScanName):
			if slices.Contains(args, "delete") {
				return nil, nil, true
			}
			return readScanChecksum(t, f.manager), nil, true
		case slices.Contains(args, "crd"):
			return nil, nil, true
		case slices.Contains(args, "--dry-run=client"):
			return []byte(chart), nil, true
		case slices.Contains(args, "--local"):
			file := args[slices.Index(args, "-f")+1]
			content, e := os.ReadFile(file)
			if e != nil || string(content) != schema || !slices.Contains(args, felixHelmOwnership) {
				t.Fatalf("CRD stage did not preserve the stock schema and attach Helm ownership: %q %v", content, e)
			}
			return []byte("Helm-owned CRD"), nil, true
		case slices.Contains(args, "create"):
			file := args[slices.Index(args, "-f")+1]
			content, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			switch filepath.Base(file) {
			case "felix-crd.yaml":
				if string(content) != "Helm-owned CRD" {
					t.Fatalf("unexpected staged CRD: %q", content)
				}
				crdCreated = true
			case "felix-default.json":
				owned, e := f.manager.ownsCanalHold()
				if e != nil || !owned || !crdCreated || string(content) != offlineFelixDefault {
					t.Fatalf("invalid offline default or bootstrap order: %q %v", content, e)
				}
				defaultCreated = true
			default:
				t.Fatalf("unexpected bootstrap file %s", file)
			}
			return nil, nil, true
		case slices.Contains(args, felixResource) && slices.Contains(args, "default"):
			if !crdCreated {
				t.Fatal("read Felix before creating its CRD")
			}
			return nil, nil, true
		default:
			t.Fatalf("unexpected command %s %v", name, args)
			return nil, nil, true
		}
	}
	clock := time.Now()
	f.manager.now = func() time.Time { clock = clock.Add(20 * time.Second); return clock }
	observation, e := f.execute(wire.KubernetesJoinOperation, server())
	if e == nil || observation.ErrorCode != "KUBERNETES_NOT_READY" || observation.State != wire.KubernetesNotReady {
		t.Fatalf("unready bootstrap did not fail closed: %+v %v", observation, e)
	}
	if owned, e := f.manager.ownsCanalHold(); e != nil || !owned {
		t.Fatalf("failed bootstrap lost its hold: %v", e)
	}
	apiReady = true
	f.host.activeState = "active"
	if observation, e = f.execute(wire.KubernetesJoinOperation, server()); e != nil || observation.State != wire.KubernetesReady {
		t.Fatalf("retry did not resume: %+v %v", observation, e)
	}
	if f.downloads["rke2.linux-amd64.tar.gz"] != 1 || f.host.called("systemctl restart") != 0 {
		t.Fatal("retry reinstalled or restarted an active server")
	}
}

func TestCanalBootstrapKeepsSitePolicy(t *testing.T) {
	t.Run("native skip file", func(t *testing.T) {
		f := newFixture(t, tarball(t, binaryContents(), nil))
		f.startServer()
		const site = "# managed by the site\n"
		if e := writeBytes(f.manager.path(canalHold), []byte(site)); e != nil {
			t.Fatal(e)
		}
		if _, e := f.execute(wire.KubernetesJoinOperation, server()); e != nil {
			t.Fatal(e)
		}
		if f.read(canalHold) != site || f.host.called("kubectl --kubeconfig "+f.manager.path(adminKubeconfig)) != 0 {
			t.Fatal("join modified the native Canal skip policy")
		}
	})
	t.Run("concurrent explicit usage setting", func(t *testing.T) {
		f := newFixture(t, tarball(t, binaryContents(), nil))
		if e := writeBytes(f.manager.path(canalManifest), []byte("packaged manifest")); e != nil {
			t.Fatal(e)
		}
		current := `{"metadata":{"resourceVersion":"17"},"spec":{"logSeverityScreen":"Info"}}`
		patches := 0
		f.host.onRun = func(_ string, args []string) ([]byte, error, bool) {
			switch {
			case slices.Contains(args, "crd"):
				return []byte("customresourcedefinition/" + felixResource), nil, true
			case slices.Contains(args, "get"):
				return []byte(current), nil, true
			case slices.Contains(args, "patch"):
				patches++
				patch := args[slices.Index(args, "-p")+1]
				if !strings.Contains(patch, `"resourceVersion":"17"`) || !strings.Contains(patch, `"usageReportingEnabled":false`) ||
					strings.Contains(patch, "logSeverityScreen") {
					t.Fatalf("invalid site policy merge patch: %s", patch)
				}
				current = `{"metadata":{"resourceVersion":"18"},"spec":{"logSeverityScreen":"Info","usageReportingEnabled":true}}`
				return nil, errors.New("Conflict"), true
			default:
				t.Fatalf("unexpected command %v", args)
				return nil, nil, true
			}
		}
		scratch := t.TempDir()
		kubectl := func(args ...string) ([]byte, error) {
			output, e, _ := f.host.onRun("kubectl", args)
			return output, e
		}
		if ready, e := f.manager.configureCanal(kubectl, scratch); ready || e != nil {
			t.Fatalf("conflict should leave the hold pending: %t %v", ready, e)
		}
		if ready, e := f.manager.configureCanal(kubectl, scratch); !ready || e != nil || patches != 1 {
			t.Fatalf("retry did not keep the explicit site field: %t %v, patches=%d", ready, e, patches)
		}
	})
}

func TestServerJoinLeavesTheDisabledCanalChartToRKE2(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	f.startServer()
	if e := writeBytes(f.manager.path(canalManifest), []byte("temporarily staged chart")); e != nil {
		t.Fatal(e)
	}
	queries := 0
	f.host.onRun = func(name string, args []string) ([]byte, error, bool) {
		if name != "kubectl" || !slices.Contains(args, "--request-timeout=5s") {
			return nil, nil, false
		}
		if !slices.Contains(args, canalScanName) {
			t.Fatalf("Agent tried to configure the site's disabled chart: %v", args)
		}
		if slices.Contains(args, "delete") {
			return nil, nil, true
		}
		queries++
		if queries == 1 {
			return []byte("previous scan"), nil, true
		}
		if e := os.Remove(f.manager.path(canalManifest)); e != nil {
			t.Fatal(e)
		}
		return readScanChecksum(t, f.manager), nil, true
	}
	if _, e := f.execute(wire.KubernetesJoinOperation, server()); e != nil {
		t.Fatal(e)
	}
	for _, file := range []string{canalHold, canalScanFile} {
		if _, e := os.Lstat(f.manager.path(file)); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("Agent bootstrap file remained after site-selected CNI: %s %v", file, e)
		}
	}
}

func readScanChecksum(t *testing.T, manager *Manager) []byte {
	t.Helper()
	contents, e := os.ReadFile(manager.path(canalScanFile))
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(contents)
	return []byte(hex.EncodeToString(sum[:]))
}

func TestBundledFelixCRDRefusesMissingSchema(t *testing.T) {
	chart := tarball(t, map[string]string{"rke2-canal/Chart.yaml": "name: rke2-canal"}, nil)
	if _, e := bundledFelixCRD([]byte(base64.StdEncoding.EncodeToString(chart))); e == nil || e.Error() != "KUBERNETES_ASSET_INVALID" {
		t.Fatalf("missing schema accepted: %v", e)
	}
}
