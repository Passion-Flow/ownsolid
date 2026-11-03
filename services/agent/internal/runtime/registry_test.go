package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/imageregistry"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func registryConfigurationFixture(t *testing.T) (Config, string, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c := Config{StateDirectory: filepath.Join(root, "state"), DiskPath: root, ControlOrigin: "https://admin.example.invalid", ExecutionProfile: wire.DockerInferenceProfile}
	if e = os.Mkdir(c.StateDirectory, 0700); e != nil {
		t.Fatal(e)
	}
	c.Inference.RootDirectory = filepath.Join(root, "inference")
	c.Inference.DockerSocket = "/run/docker.sock"
	component := imageregistry.Defaults()
	component.Root = filepath.Join(root, "registry")
	component.DockerDataRoot = filepath.Join(root, "docker-data")
	component.Verification.MountRoot = root
	component.Verification.TrustFile = filepath.Join(root, "manufacturer-trust.json")
	component.OperationSeconds = 7199
	c.ImageRegistry = &component
	base, componentFile := filepath.Join(root, "agent.json"), filepath.Join(root, "registry.json")
	for p, value := range map[string]any{base: c, componentFile: component} {
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return c, base, componentFile
}

func TestRegistryRootExplicitConfiguration(t *testing.T) {
	c, base, component := registryConfigurationFixture(t)
	t.Setenv(registryConfigurationKey, "/must/not/be/read")
	omitted, e := LoadConfigWithEnvironment(base, "")
	if e != nil || omitted.ImageRegistry != nil {
		t.Fatal("ambient component adopted", e)
	}
	environment := filepath.Join(filepath.Dir(base), "agent.env")
	for _, value := range []string{component, "'" + component + "'", string(mustJSON(t, component))} {
		if e = os.WriteFile(environment, []byte("# explicit fixture only\n"+registryConfigurationKey+"="+value+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		loaded, e := LoadConfigWithEnvironment(base, environment)
		if e != nil || !reflect.DeepEqual(loaded.ImageRegistry, c.ImageRegistry) || loaded.ImageRegistry.Verification.AllowTestFixture {
			t.Fatal("explicit nondefault/false lost", e)
		}
	}
	for _, raw := range []string{"", "UNKNOWN=false", registryConfigurationKey + "=", registryConfigurationKey + "=false", registryConfigurationKey + "=$HOME/file", registryConfigurationKey + "=`command`", registryConfigurationKey + "=\"unterminated", registryConfigurationKey + "=null", registryConfigurationKey + "=" + component + "\n" + registryConfigurationKey + "=" + component} {
		if e = os.WriteFile(environment, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = LoadConfigWithEnvironment(base, environment); e == nil {
			t.Fatalf("invalid explicit environment accepted: %q", raw)
		}
	}
	if e = os.WriteFile(environment, []byte(registryConfigurationKey+"="+component), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(base, []byte(`{"unknown":false}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadConfigWithEnvironment(base, environment); e == nil {
		t.Fatal("env repaired invalid base")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestRegistryRootConfigurationPreservesProfilesAndDisjointRoots(t *testing.T) {
	c, _, _ := registryConfigurationFixture(t)
	if e := validateRegistryConfiguration(c); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"profile", "inference", "socket", "state", "training", "preparation", "fixture"} {
		t.Run(name, func(t *testing.T) {
			changed := c
			component := *c.ImageRegistry
			changed.ImageRegistry = &component
			switch name {
			case "profile":
				changed.ExecutionProfile = wire.ReadOnlyProfile
			case "inference":
				changed.Inference.RootDirectory = ""
			case "socket":
				changed.Inference.DockerSocket = ""
			case "state":
				component.Root = c.StateDirectory
			case "training":
				changed.Training.RootDirectory = component.Root
			case "preparation":
				changed.Preparation.RootDirectory = filepath.Join(component.Root, "child")
			case "fixture":
				component.Verification.AllowTestFixture = true
			}
			if validateRegistryConfiguration(changed) == nil {
				t.Fatal("unsafe component accepted")
			}
		})
	}
	loader, closeLoader, e := registryLoader(Config{}, nil)
	if e != nil || loader != nil || closeLoader == nil {
		t.Fatal("omitted loader acquired resources", e)
	}
	closeLoader()
}

// This double verifies orchestration arguments only; no Registry/Docker proof.
type explicitTaskImageDouble struct {
	calls             int
	node, task, lease string
	command           wire.InferenceCommand
	failure           error
}

func (d *explicitTaskImageDouble) EnsureTask(ctx context.Context, node, task, lease string, c wire.InferenceCommand) (wire.RegistryManifest, error) {
	if _, ok := ctx.Deadline(); !ok {
		return wire.RegistryManifest{}, errors.New("missing deadline")
	}
	d.calls++
	d.node = node
	d.task = task
	d.lease = lease
	d.command = c
	return wire.RegistryManifest{}, d.failure
}
func TestRegistryTaskDispatchRequiresBoundIntentAndDeadline(t *testing.T) {
	node := "12345678-1234-1234-1234-123456789abc"
	c := wire.InferenceCommand{Action: "deploy", DeploymentID: node, ModelID: "22345678-1234-1234-1234-123456789abc", CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Revision: 1}
	lease := wire.Lease{Operation: c.Operation(), TaskID: "32345678-1234-1234-1234-123456789abc", LeaseToken: strings.Repeat("A", 43), Inference: &c, Attempt: 1}
	double := &explicitTaskImageDouble{}
	if e := ensureRegistryTaskImage(context.Background(), node, lease, double); e != nil || double.calls != 0 {
		t.Fatal("ordinary deploy contacted registry", e)
	}
	c.RegistryPinID = "42345678-1234-1234-1234-123456789abc"
	if ensureRegistryTaskImage(context.Background(), node, lease, double) == nil || double.calls != 0 {
		t.Fatal("unfenced loader call")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if ensureRegistryTaskImage(ctx, node, lease, nil) == nil {
		t.Fatal("missing loader accepted")
	}
	if e := ensureRegistryTaskImage(ctx, node, lease, double); e != nil || double.calls != 1 || double.node != node || double.task != lease.TaskID || double.lease != lease.LeaseToken || !reflect.DeepEqual(double.command, c) {
		t.Fatal("fenced intent changed", e)
	}
	double.failure = errors.New("private synthetic provider cause")
	if e := ensureRegistryTaskImage(ctx, node, lease, double); e == nil || e.Error() != "REGISTRY_RECOVERY_REQUIRED" {
		t.Fatal("failed loader promoted or leaked cause", e)
	}
	cancel()
	before := double.calls
	if ensureRegistryTaskImage(ctx, node, lease, double) == nil || double.calls != before {
		t.Fatal("cancelled loader dispatched")
	}
}

func TestRegistryRuntimeLoadsOnlyInsideTheRenewedPhysicalLease(t *testing.T) {
	for _, denied := range []bool{true, false} {
		t.Run(map[bool]string{true: "renewal_denied", false: "loader_failure_before_executor"}[denied], func(t *testing.T) {
			const node = "12345678-1234-1234-1234-123456789abc"
			c := wire.InferenceCommand{Action: "deploy", DeploymentID: node, ModelID: "22345678-1234-1234-1234-123456789abc", CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Revision: 1, RegistryPinID: "42345678-1234-1234-1234-123456789abc"}
			lease := wire.Lease{Operation: c.Operation(), TaskID: "32345678-1234-1234-1234-123456789abc", LeaseToken: strings.Repeat("A", 43), Inference: &c, Attempt: 1}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, "/renewals") {
					t.Error("failure acknowledged success or contacted unexpected endpoint")
					w.WriteHeader(500)
					return
				}
				if denied {
					w.WriteHeader(409)
					json.NewEncoder(w).Encode(map[string]any{"code": "AGENT_LEASE_STALE"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"data": wire.RenewedLease{LeaseExpiresAt: time.Now().Add(time.Minute), ValidForMilliseconds: 1000}})
			}))
			defer server.Close()
			connection := &client.Client{Origin: server.URL, HTTP: server.Client(), Credential: strings.Repeat("B", 43)}
			defer connection.Close()
			double := &explicitTaskImageDouble{failure: errors.New("explicit image transport failure")}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// A nil executor would panic if called. Both failure branches must stop
			// before executor effects and join their renewal/watchdog goroutine.
			e := executeInference(ctx, connection, "/agent/v1/nodes/"+node, node, lease, nil, double)
			if e == nil || double.calls != map[bool]int{true: 0, false: 1}[denied] {
				t.Fatal("physical lease/image fence lost", e, double.calls)
			}
		})
	}
}

func TestRegistryObservationCapabilityRequiresConstructedHandler(t *testing.T) {
	original := wire.InferenceObservation{ProbeErrorCode: wire.InferenceImageMissing, ObservedAt: time.Now().UTC(), GPUs: []wire.NVIDIAGPU{}}
	with := registryObservation(original, &explicitTaskImageDouble{})
	if !with.RegistryImageDelivery || with.ProbeErrorCode != original.ProbeErrorCode || with.Deployment != nil {
		t.Fatal("capability fabricated successful image state")
	}
	without := registryObservation(with, nil)
	if without.RegistryImageDelivery || without.ProbeErrorCode != original.ProbeErrorCode {
		t.Fatal("omitted handler retained capability")
	}
}
