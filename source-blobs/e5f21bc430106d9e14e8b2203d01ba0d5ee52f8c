package modelpreparations

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// The bounded Unix HTTP responder is a named contract fixture, not a real Docker daemon.
func dockerFixture(t *testing.T, handler http.HandlerFunc) *Docker {
	t.Helper()
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "t-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	listener, e := net.Listen("unix", filepath.Join(root, "d.sock"))
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	d, e := NewDocker(filepath.Join(root, "d.sock"), settings.Default())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(d.Close)
	return d
}
func TestUnixDockerAPIActualSerializationOwnershipAndBoundedFailures(t *testing.T) {
	config := fixtureConfig()
	image := fixtureImage(config)
	var mutations atomic.Int64
	var badImage atomic.Bool
	m, j, _ := fixtureLifecycle(t)
	expected, _, e := m.expected(context.Background(), *j)
	if e != nil {
		t.Fatal(e)
	}
	d := dockerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, EngineAPI+"/images/"):
			out := image
			if badImage.Load() {
				out.Architecture = "arm64"
			}
			json.NewEncoder(w).Encode(out)
		case r.URL.Path == EngineAPI+"/info":
			json.NewEncoder(w).Encode(struct {
				inference.DockerInfo
				DriverStatus  [][]string
				CgroupVersion string
			}{DockerInfo: inference.DockerInfo{OSType: "linux", Architecture: "x86_64", MemoryLimit: true, SwapLimit: true, PidsLimit: true, CPUCfsQuota: true, Runtimes: map[string]any{"nvidia": map[string]any{}}}, DriverStatus: [][]string{{"driver-type", "io.containerd.snapshotter.v1"}}, CgroupVersion: "2"})
		case r.URL.Path == EngineAPI+"/containers/create":
			var actual ContainerConfig
			if r.Method != "POST" || r.URL.Query().Get("name") != wire.ContainerName(j.Command.AttemptID) || json.NewDecoder(r.Body).Decode(&actual) != nil || actual.Image != config.ImageID || actual.HostConfig.NetworkMode != "none" || actual.HostConfig.MemorySwap != actual.HostConfig.Memory || actual.HostConfig.DeviceRequests[0].DeviceIDs[0] != j.Command.GPUUUID || actual.User == "0" {
				t.Error("unapproved serialized preparation create")
				w.WriteHeader(400)
				return
			}
			mutations.Add(1)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"Id": strings.Repeat("1", 64)})
		case strings.HasSuffix(r.URL.Path, "/start") || strings.HasSuffix(r.URL.Path, "/kill"):
			mutations.Add(1)
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/stop"):
			if r.URL.Query().Get("t") != "15" {
				t.Error("stop grace was not bounded")
			}
			mutations.Add(1)
			w.WriteHeader(304)
		case r.Method == "DELETE":
			if r.URL.Query().Get("force") != "false" || r.URL.Query().Get("v") != "false" {
				t.Error("forced or volume removal forbidden")
			}
			mutations.Add(1)
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	if e = d.CheckEngine(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = d.InspectImage(ctx, config); e != nil {
		t.Fatal(e)
	}
	badImage.Store(true)
	if _, e = d.InspectImage(ctx, config); e == nil {
		t.Fatal("host architecture silently substituted")
	}
	badImage.Store(false)
	id, e := d.Create(ctx, wire.ContainerName(j.Command.AttemptID), expected)
	if e != nil {
		t.Fatal(e)
	}
	for _, action := range []func() error{func() error { return d.Start(ctx, id) }, func() error { return d.Stop(ctx, id, 15*time.Second) }, func() error { return d.Kill(ctx, id) }, func() error { return d.Remove(ctx, id) }} {
		if e = action(); e != nil {
			t.Fatal(e)
		}
	}
	before := mutations.Load()
	if e = d.Kill(ctx, "unrelated"); e == nil {
		t.Fatal("foreign object reference accepted")
	}
	expected.Cmd = []string{strings.Repeat("x", MaximumDockerRequestBytes)}
	if _, e = d.Create(ctx, wire.ContainerName(j.Command.AttemptID), expected); e == nil {
		t.Fatal("unbounded create body sent")
	}
	if mutations.Load() != before {
		t.Fatal("invalid intent mutated engine")
	}
	oversize := dockerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", MaximumDockerResponseBytes+1)))
	})
	if _, e = oversize.InspectImage(ctx, config); e == nil {
		t.Fatal("unbounded engine response accepted")
	}
}
func TestPreparationAgentConfigurationBindsManufacturedIdentityAndNondefaultResources(t *testing.T) {
	c := fixtureConfig()
	command := fixtureCommand()
	policy := c.Policy()
	policy.UserID = 12001
	policy.GroupID = 12002
	policy.PidsLimit = 64
	policy.LogMaximumBytes = 2097152
	c.Runtime = &policy
	out, e := ContainerConfiguration(c, fixtureID, fixtureID, fixtureID, command, filepath.Join(c.RootDirectory, "models", command.ModelCatalogSHA256), filepath.Join(c.RootDirectory, "preparations", command.Scope.TenantID, command.Scope.WorkspaceID, command.Scope.PreparationID, command.AttemptID))
	if e != nil || out.User != "12001:12002" || out.HostConfig.PidsLimit != 64 || out.HostConfig.LogConfig.Config["max-size"] != "2097152" {
		t.Fatal("nondefault deployment values not injected", e)
	}
	for name, change := range map[string]func(*Config){"missing_root": func(c *Config) { c.RootDirectory = "" }, "mutable_image": func(c *Config) { c.ImageReference = "projectone/model-lifecycle:latest" }, "missing_source": func(c *Config) { c.WorkerSourceSHA256 = "" }, "root_user": func(c *Config) { p := c.Policy(); p.UserID = 0; c.Runtime = &p }, "lower_memory": func(c *Config) { p := c.Policy(); p.MaximumMemoryBytes = 134217728; c.Runtime = &p }} {
		t.Run(name, func(t *testing.T) {
			c := fixtureConfig()
			change(&c)
			if c.Bind(command) == nil {
				t.Fatal("unsafe or mismatched command admitted")
			}
		})
	}
}
