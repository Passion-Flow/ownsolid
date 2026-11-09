package training

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
)

const fixtureID = "00000000-0000-4000-8000-000000000001"

func fixtureCommand() wire.Command {
	cfg := wire.DefaultConfiguration()
	digest, _ := cfg.Digest()
	image := "sha256:" + strings.Repeat("a", 64)
	return wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: fixtureID, WorkspaceID: fixtureID, JobID: fixtureID}, AttemptID: fixtureID, AttemptNumber: 1, ModelID: fixtureID, DatasetID: fixtureID, DatasetVersionID: fixtureID, DatasetManifestSHA256: strings.Repeat("b", 64), ModelCatalogSHA256: agentwire.ModelCatalogDigest(), GPUUUID: "GPU-" + fixtureID, Configuration: cfg, ConfigurationSHA256: digest, Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: "projectone/training@sha256:" + strings.Repeat("c", 64), Identity: wire.ExecutionIdentity{Profile: wire.ProductionProfile, Python: wire.PythonVersion, WorkerSourceSHA256: strings.Repeat("d", 64), ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: datawire.Limits{MaximumBytes: 16777216, MaximumRows: 2000, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 200}}, Inputs: wire.InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: strings.Repeat("e", 64)}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("f", 64)}}}
}
func fixtureConfig() Config {
	c := fixtureCommand()
	return Config{RootDirectory: "/var/lib/projectone-training", DockerSocket: "/run/docker.sock", ImageReference: c.Recipe.ImageReference, ImageID: *c.Recipe.Identity.ImageID, WorkerSourceSHA256: c.Recipe.Identity.WorkerSourceSHA256}
}
func fixtureImage(c Config) Image {
	var image Image
	image.ID = c.ImageID
	image.OS = "linux"
	image.Architecture = "amd64"
	image.Config.User = c.Policy().User()
	image.Config.WorkingDir = ImageWorkingDirectory
	image.Config.Entrypoint = []string{"python", WorkerFile}
	image.Config.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	image.Config.Labels = map[string]string{"projectone.training.worker_source_sha256": c.WorkerSourceSHA256, "projectone.training.worker_version": wire.WorkerVersion}
	return image
}
func containerFrom(c ContainerConfig, image Image, name string) Container {
	var out Container
	out.ID = strings.Repeat("1", 64)
	out.Name = "/" + name
	out.Image = c.Image
	out.Config.Image = c.Image
	out.Config.User = c.User
	out.Config.WorkingDir = c.WorkingDir
	out.Config.Entrypoint = append([]string{}, c.Entrypoint...)
	out.Config.Cmd = append([]string{}, c.Cmd...)
	out.Config.Env = append(append([]string{}, image.Config.Env...), c.Env...)
	out.Config.Labels = c.Labels
	out.HostConfig = c.HostConfig
	for _, mount := range c.HostConfig.Mounts {
		out.Mounts = append(out.Mounts, inference.Mount{Type: mount.Type, Source: mount.Source, Destination: mount.Target, RW: !mount.ReadOnly})
	}
	out.NetworkSettings.Networks = map[string]struct{ NetworkID string }{"none": {}}
	out.State.Status = "created"
	return out
}

// faultEngine reports synthetic Docker state. It is never GPU execution evidence.
type faultEngine struct {
	config      Config
	container   *Container
	createError error
	startError  error
	stopError   error
	killError   error
	removeError error
	stayRunning bool
	calls       []string
	beforeStop  func(*Container)
	afterStart  func(*Container)
}

func (d *faultEngine) InspectImage(context.Context, Config) (Image, error) {
	return fixtureImage(d.config), nil
}
func (d *faultEngine) CheckEngine(context.Context) error { return nil }
func (d *faultEngine) InspectContainer(_ context.Context, reference string) (Container, error) {
	if d.container == nil || reference != d.container.ID && "/"+reference != d.container.Name {
		return Container{}, fail("TRAINING_DOCKER_NOT_FOUND")
	}
	raw, _ := json.Marshal(d.container)
	var value Container
	json.Unmarshal(raw, &value)
	return value, nil
}
func (d *faultEngine) Create(_ context.Context, name string, c ContainerConfig) (string, error) {
	d.calls = append(d.calls, "create")
	value := containerFrom(c, fixtureImage(d.config), name)
	d.container = &value
	return value.ID, d.createError
}
func (d *faultEngine) Start(context.Context, string) error {
	d.calls = append(d.calls, "start")
	if d.startError == nil {
		d.container.State.Running = true
		d.container.State.Status = "running"
		if d.afterStart != nil {
			d.afterStart(d.container)
		}
	}
	return d.startError
}
func (d *faultEngine) Stop(context.Context, string, time.Duration) error {
	d.calls = append(d.calls, "stop")
	if d.beforeStop != nil {
		d.beforeStop(d.container)
	}
	if !d.stayRunning {
		d.container.State.Running = false
		d.container.State.Status = "exited"
	}
	return d.stopError
}
func (d *faultEngine) Kill(context.Context, string) error {
	d.calls = append(d.calls, "kill")
	if d.killError == nil {
		d.container.State.Running = false
		d.container.State.Status = "exited"
	}
	return d.killError
}
func (d *faultEngine) Remove(context.Context, string) error {
	d.calls = append(d.calls, "remove")
	if d.removeError == nil {
		d.container = nil
	}
	return d.removeError
}
func fixtureLifecycle(t *testing.T) (*Lifecycle, *Journal, *faultEngine) {
	t.Helper()
	root := t.TempDir()
	os.Chmod(root, 0700)
	c := fixtureConfig()
	p := c.Policy()
	p.PollMilliseconds = 100
	c.Runtime = &p
	engine := &faultEngine{config: c}
	s := JournalStore{Directory: root}
	j, e := s.Begin(fixtureID, fixtureID, strings.Repeat("A", 43), fixtureCommand())
	if e != nil {
		t.Fatal(e)
	}
	return &Lifecycle{Config: c, Engine: engine, Store: s}, &j, engine
}

func TestActualPrivateJournalAndSyntheticOwnedContainerStopReplay(t *testing.T) {
	m, j, engine := fixtureLifecycle(t)
	ctx := context.Background()
	if e := m.Start(ctx, j); e != nil {
		t.Fatal(e)
	}
	if j.Phase != "running" || !j.ContainerVerified || engine.container == nil {
		t.Fatal("container ownership was not committed")
	}
	receipt, e := m.Stop(ctx, j)
	if e != nil || receipt.Proof != "owned_container_removed" || receipt.ValidateFor(j.Command, j.NodeID, j.TaskID) != nil {
		t.Fatal("actual lifecycle receipt", e)
	}
	reloaded, e := m.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.Stop(ctx, &reloaded)
	if e != nil || !reflect.DeepEqual(again, receipt) {
		t.Fatal("stop retry changed persisted receipt", e)
	}
	if !reflect.DeepEqual(engine.calls, []string{"create", "start", "stop", "remove"}) {
		t.Fatal("unexpected physical effects", engine.calls)
	}
	const foreign = "00000000-0000-4000-8000-000000000002"
	for _, probe := range []struct {
		name, node, task, lease string
		attempt                 string
		number                  int
	}{
		{"foreign node", foreign, j.TaskID, j.LeaseToken, j.Command.AttemptID, j.Command.AttemptNumber},
		{"foreign task", j.NodeID, foreign, j.LeaseToken, j.Command.AttemptID, j.Command.AttemptNumber},
		{"stale lease", j.NodeID, j.TaskID, strings.Repeat("B", 43), j.Command.AttemptID, j.Command.AttemptNumber},
		{"foreign attempt", j.NodeID, j.TaskID, j.LeaseToken, foreign, j.Command.AttemptNumber},
		{"foreign attempt number", j.NodeID, j.TaskID, j.LeaseToken, j.Command.AttemptID, j.Command.AttemptNumber + 1},
	} {
		t.Run(probe.name, func(t *testing.T) {
			command := j.Command
			command.AttemptID, command.AttemptNumber = probe.attempt, probe.number
			if _, err := command.Digest(); err != nil {
				t.Fatal("expected a valid alternate command", err)
			}
			if _, err := m.Store.Begin(probe.node, probe.task, probe.lease, command); err == nil ||
				err.Error() != "TRAINING_AGENT_PREVIOUS_ATTEMPT_REQUIRES_RECONCILIATION" {
				t.Fatal("mismatched delivery did not require reconciliation", err)
			}
			replay, err := m.Store.Begin(j.NodeID, j.TaskID, j.LeaseToken, j.Command)
			if err != nil || !reflect.DeepEqual(replay, reloaded) {
				t.Fatal("refused delivery changed the persisted journal", err)
			}
		})
	}
}
func TestUncertainCreateAdoptionAndCancellationEscalationRequireFreshOwnership(t *testing.T) {
	t.Run("lost_create_response", func(t *testing.T) {
		m, j, d := fixtureLifecycle(t)
		d.createError = fail("TRAINING_DOCKER_UNAVAILABLE")
		if e := m.Start(context.Background(), j); e != nil {
			t.Fatal("exact deterministic container was not safely adopted", e)
		}
		if _, e := m.Stop(context.Background(), j); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("graceful_timeout_kill", func(t *testing.T) {
		m, j, d := fixtureLifecycle(t)
		if e := m.Start(context.Background(), j); e != nil {
			t.Fatal(e)
		}
		d.stayRunning = true
		d.stopError = fail("TRAINING_DOCKER_UNAVAILABLE")
		if _, e := m.Stop(context.Background(), j); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(d.calls, []string{"create", "start", "stop", "kill", "remove"}) {
			t.Fatal(d.calls)
		}
	})
	t.Run("ownership_changes_during_stop", func(t *testing.T) {
		m, j, d := fixtureLifecycle(t)
		if e := m.Start(context.Background(), j); e != nil {
			t.Fatal(e)
		}
		d.stayRunning = true
		d.beforeStop = func(c *Container) { c.Config.Labels["projectone.journal_id"] = fixtureID }
		if _, e := m.Stop(context.Background(), j); e == nil {
			t.Fatal("foreign changed object accepted")
		}
		if !reflect.DeepEqual(d.calls, []string{"create", "start", "stop"}) || d.container == nil || j.StopReceipt != nil {
			t.Fatal("unowned object killed, removed or released")
		}
	})
	t.Run("remove_failure_retains_retry", func(t *testing.T) {
		m, j, d := fixtureLifecycle(t)
		if e := m.Start(context.Background(), j); e != nil {
			t.Fatal(e)
		}
		d.removeError = fail("TRAINING_DOCKER_UNAVAILABLE")
		if _, e := m.Stop(context.Background(), j); e == nil || j.StopReceipt != nil {
			t.Fatal("uncertain removal became receipt")
		}
		d.removeError = nil
		if _, e := m.Stop(context.Background(), j); e != nil {
			t.Fatal(e)
		}
	})
}
func TestNeverCreatedProofAndCrashAfterCreateRemainDistinct(t *testing.T) {
	m, j, d := fixtureLifecycle(t)
	receipt, e := m.Stop(context.Background(), j)
	if e != nil || receipt.Proof != "never_created" || receipt.ContainerID != nil || len(d.calls) != 0 {
		t.Fatal("pre-create cleanup", e)
	}
	m, j, d = fixtureLifecycle(t)
	j.Phase = "creating"
	if e = m.Store.Save(*j); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Stop(context.Background(), j); e == nil || j.StopReceipt != nil || len(d.calls) != 0 {
		t.Fatal("unknown create disappeared and acquired false proof")
	}
}
func TestContainerAdmissionRejectsPrivilegeMountIdentityAndResourceMutations(t *testing.T) {
	m, j, _ := fixtureLifecycle(t)
	expected, image, e := m.expected(context.Background(), *j)
	if e != nil {
		t.Fatal(e)
	}
	base := containerFrom(expected, image, wire.ContainerName(j.Command.AttemptID))
	for name, change := range map[string]func(*Container){"privilege": func(c *Container) { c.HostConfig.Privileged = true }, "root": func(c *Container) { c.Config.User = "0" }, "foreign_image": func(c *Container) { c.Image = "sha256:" + strings.Repeat("f", 64) }, "network": func(c *Container) { c.HostConfig.NetworkMode = "host" }, "socket_mount": func(c *Container) { c.Mounts[0].Source = "/run/docker.sock" }, "writable_model": func(c *Container) { c.Mounts[0].RW = true }, "memory": func(c *Container) { c.HostConfig.MemorySwap = -1 }, "gpu": func(c *Container) { c.HostConfig.DeviceRequests[0].DeviceIDs = []string{"all"} }, "secret_env": func(c *Container) { c.Config.Env = append(c.Config.Env, "NODE_CREDENTIAL=forbidden") }, "extra_group": func(c *Container) { c.HostConfig.GroupAdd = []string{"0"} }, "restart": func(c *Container) { c.HostConfig.RestartPolicy.Name = "always" }, "foreign_name": func(c *Container) { c.Name = "/unrelated" }} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var c Container
			json.Unmarshal(raw, &c)
			change(&c)
			if VerifyContainer(c, expected, image, wire.ContainerName(j.Command.AttemptID), base.ID) == nil {
				t.Fatal("unsafe container admitted")
			}
		})
	}
}
func TestJournalRejectsHardlinkSymlinkCorruptionAndMissingRequiredField(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink", "corrupt", "missing"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _ := fixtureLifecycle(t)
			path := m.Store.Path()
			switch kind {
			case "hardlink":
				if e := os.Link(path, filepath.Join(m.Store.Directory, "unrelated")); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				raw, _ := os.ReadFile(path)
				os.Rename(path, path+".retained")
				os.WriteFile(path+".foreign", raw, 0600)
				os.Symlink(path+".foreign", path)
			case "corrupt":
				os.WriteFile(path, []byte("{"), 0600)
			case "missing":
				raw, _ := os.ReadFile(path)
				var v map[string]any
				json.Unmarshal(raw, &v)
				delete(v, "phase")
				raw, _ = json.Marshal(v)
				os.WriteFile(path, raw, 0600)
			}
			if _, e := m.Store.Read(); e == nil {
				t.Fatal("unsafe journal accepted")
			}
		})
	}
}
