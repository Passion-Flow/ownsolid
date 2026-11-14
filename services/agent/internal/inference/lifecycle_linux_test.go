//go:build linux && inference_integration

package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// syntheticEngine/probes exist only in this test. Files/model bytes/journal/UID behavior are real Linux operations.
type syntheticEngine struct {
	container              Container
	exists                 bool
	creates, starts, stops int
	loseCreateAck          bool
	network                string
	node                   string
	spec                   ContainerConfig
}

func (e *syntheticEngine) InspectApprovedImage(context.Context) (Image, error) {
	var value Image
	value.ID = wire.ApprovedInferenceRecipe().ImageManifestDigest
	value.OS = "linux"
	value.Architecture = "amd64"
	value.Config.Env = []string{"CUDA_VERSION=" + wire.ApprovedInferenceRecipe().CUDAVersion}
	return value, nil
}
func (e *syntheticEngine) CheckEngine(context.Context) error { return nil }
func (e *syntheticEngine) EnsureNetwork(_ context.Context, node string) (string, error) {
	e.node = node
	e.network = strings.Repeat("b", 64)
	return e.network, nil
}
func (e *syntheticEngine) VerifyNetworkEndpoint(_ context.Context, node, id, container string) error {
	if e.node != node || e.network != id || e.exists && e.container.ID != container {
		return fail("INFERENCE_NETWORK_CONFLICT")
	}
	return nil
}
func (e *syntheticEngine) RemoveUnusedNetwork(_ context.Context, node string, expected ...string) error {
	if e.exists || node != e.node || len(expected) == 1 && expected[0] != e.network {
		return fail("INFERENCE_NETWORK_CONFLICT")
	}
	e.network = ""
	return nil
}
func (e *syntheticEngine) InspectContainer(_ context.Context, id string) (Container, error) {
	if !e.exists || id != e.container.ID && id != strings.TrimPrefix(e.container.Name, "/") {
		return Container{}, fail("INFERENCE_DOCKER_NOT_FOUND")
	}
	return e.container, nil
}
func (e *syntheticEngine) CreateContainer(_ context.Context, deployment, node, gpu, model, cache, network string) (string, error) {
	if e.exists {
		return "", fail("INFERENCE_CONTAINER_CONFLICT")
	}
	spec, err := ContainerConfiguration(deployment, node, gpu, model, cache, network)
	if err != nil {
		return "", err
	}
	return e.createConfigured(spec, deployment, node, model, cache)
}
func (e *syntheticEngine) CreateDerivedContainer(_ context.Context, c wire.InferenceCommand, node, model, cache, network string) (string, error) {
	if e.exists {
		return "", fail("INFERENCE_CONTAINER_CONFLICT")
	}
	spec, err := DerivedContainerConfiguration(c, node, model, cache, network)
	if err != nil {
		return "", err
	}
	return e.createConfigured(spec, c.DeploymentID, node, model, cache)
}
func (e *syntheticEngine) createConfigured(spec ContainerConfig, deployment, node, model, cache string) (string, error) {
	spec.Image = wire.ApprovedInferenceRecipe().ImageManifestDigest
	e.spec = spec
	var value Container
	value.ID = strings.Repeat("c", 64)
	value.Name = "/projectone-vllm-" + deployment
	value.Image = spec.Image
	value.State.Status = "created"
	value.Config.Image = spec.Image
	value.Config.User = spec.User
	value.Config.WorkingDir = spec.WorkingDir
	value.Config.Cmd = spec.Cmd
	value.Config.Entrypoint = spec.Entrypoint
	value.Config.Labels = spec.Labels
	value.Config.Env = append([]string{"CUDA_VERSION=" + wire.ApprovedInferenceRecipe().CUDAVersion}, spec.Env...)
	value.HostConfig = spec.HostConfig
	value.HostConfig.IpcMode = "private"
	value.Mounts = []Mount{{Type: "bind", Source: model, Destination: "/model", RW: false}, {Type: "bind", Source: cache, Destination: "/home/vllm/.cache", RW: true}}
	value.NetworkSettings.Networks = map[string]struct{ NetworkID string }{"projectone-inference-" + node: {NetworkID: ""}}
	e.container = value
	e.exists = true
	e.creates++
	if e.loseCreateAck {
		e.loseCreateAck = false
		return "", fail("INFERENCE_DOCKER_UNAVAILABLE")
	}
	return value.ID, nil
}
func (e *syntheticEngine) StartContainer(_ context.Context, id string) error {
	if !e.exists || e.container.ID != id {
		return fail("INFERENCE_DOCKER_NOT_FOUND")
	}
	e.container.State.Running = true
	e.container.State.Status = "running"
	e.container.State.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	e.container.NetworkSettings.Networks["projectone-inference-"+e.node] = struct{ NetworkID string }{e.network}
	e.starts++
	return nil
}
func (e *syntheticEngine) StopContainer(_ context.Context, id string) error {
	if !e.exists || e.container.ID != id {
		return fail("INFERENCE_DOCKER_NOT_FOUND")
	}
	e.container.State.Running = false
	e.container.State.Status = "exited"
	e.stops++
	return nil
}
func (e *syntheticEngine) RemoveContainer(_ context.Context, id string) error {
	if !e.exists || e.container.ID != id {
		return fail("INFERENCE_DOCKER_NOT_FOUND")
	}
	if e.container.State.Running {
		return fail("INFERENCE_CONTAINER_CONFLICT")
	}
	e.exists = false
	return nil
}
func (e *syntheticEngine) ContainerFailure(context.Context, string) error {
	return fail("INFERENCE_GPU_OOM")
}

type syntheticProbes struct{}

func (syntheticProbes) Health(context.Context) error { return nil }
func (syntheticProbes) Generate(context.Context) (wire.GenerationVerification, error) {
	hash := sha256.Sum256([]byte("SYNTHETIC_TEST_RESPONSE"))
	return wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: hex.EncodeToString(hash[:]), InputTokens: 10, OutputTokens: 2, ObservedAt: time.Now().UTC()}, nil
}
func inferenceFixture(t *testing.T) (*Manager, *syntheticEngine, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("root in the explicitly isolated Linux fixture is required")
	}
	base, e := os.MkdirTemp("/opt/projectone-inference-test", "case-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	root := filepath.Join(base, "runtime")
	private := filepath.Join(base, "state")
	if os.Mkdir(root, 0700) != nil || os.Mkdir(private, 0700) != nil {
		t.Fatal("fixture directories")
	}
	engine := &syntheticEngine{}
	gpu := "GPU-00000000-0000-0000-0000-000000000003"
	m := &Manager{config: Config{RootDirectory: root}, stateDirectory: private, docker: engine, hostAvailable: func() error { return nil }, probes: syntheticProbes{}, gpu: func(context.Context) ([]wire.NVIDIAGPU, []string, error) {
		return []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic test GPU", DriverVersion: "580.65.06", MemoryTotalMiB: 16384, MemoryFreeMiB: 16000, ComputeCapability: 7.5, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, nil, nil
	}}
	return m, engine, gpu
}
func TestLinuxActualModelCacheJournalAckLossAndOwnedCleanupWithSyntheticEngine(t *testing.T) {
	m, engine, gpu := inferenceFixture(t)
	node := "00000000-0000-0000-0000-000000000001"
	deployment := "00000000-0000-0000-0000-000000000002"
	model := "00000000-0000-0000-0000-000000000004"
	task := "00000000-0000-0000-0000-000000000005"
	source := os.Getenv("PROJECT_ONE_MODEL_DIRECTORY")
	if source == "" {
		t.Fatal("actual approved model fixture required")
	}
	download := func(ctx context.Context, descriptor wire.ModelFile, destination string) error {
		input, e := os.Open(filepath.Join(source, descriptor.Path))
		if e != nil {
			return e
		}
		defer input.Close()
		output, e := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(output, input)
		if e == nil {
			e = output.Sync()
		}
		closeError := output.Close()
		if e != nil {
			return e
		}
		return closeError
	}
	command := wire.InferenceCommand{Action: "deploy", DeploymentID: deployment, ModelID: model, CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: gpu, Revision: 1}
	engine.loseCreateAck = true
	if _, e := m.Execute(context.Background(), node, task, command, download); e == nil || e.Error() != "INFERENCE_DOCKER_UNAVAILABLE" {
		t.Fatal("lost create acknowledgment was hidden", e)
	}
	if engine.creates != 1 || engine.starts != 0 {
		t.Fatal("unexpected external effects")
	}
	fresh := *m
	value, e := fresh.Execute(context.Background(), node, task, command, download)
	if e != nil || value.Deployment == nil || !value.Deployment.Ready {
		t.Fatal("fresh manager did not reconcile actual private journal", e)
	}
	if engine.creates != 1 || engine.starts != 1 {
		t.Fatal("create acknowledgment recovery repeated external effects")
	}
	if _, e = fresh.Execute(context.Background(), node, task, command, download); e != nil || engine.creates != 1 || engine.starts != 1 {
		t.Fatal("committed replay repeated external effects", e)
	}
	prior := append([]string(nil), engine.container.Config.Cmd...)
	engine.container.Config.Cmd = append(engine.container.Config.Cmd, "--unapproved")
	changed := fresh.Observe(context.Background(), node)
	if changed.Deployment == nil || changed.Deployment.Ready || changed.Deployment.ErrorCode != "INFERENCE_OWNERSHIP_CONFLICT" {
		t.Fatal("changed owned specification was accepted")
	}
	engine.container.Config.Cmd = prior
	// Resource cleanup must not depend on a healthy GPU driver or a new-allocation memory gate.
	fresh.gpu = func(context.Context) ([]wire.NVIDIAGPU, []string, error) {
		return nil, nil, fail("INFERENCE_NVIDIA_UNAVAILABLE")
	}
	fresh.hostAvailable = func() error { return fail("INFERENCE_HOST_MEMORY_LOW") }
	command.Action = "stop"
	command.Revision = 2
	if _, e = fresh.Execute(context.Background(), node, "00000000-0000-0000-0000-000000000006", command, download); e != nil {
		t.Fatal(e)
	}
	stale := command
	stale.Action = "deploy"
	stale.Revision = 1
	if _, e = fresh.Execute(context.Background(), node, task, stale, download); e == nil || e.Error() != "INFERENCE_REVISION_CONFLICT" {
		t.Fatal("stale revision started a workload", e)
	}
	sentinel := filepath.Join(m.deploymentPath(deployment), "unowned-sentinel")
	if os.WriteFile(sentinel, []byte("PRESERVE_UNOWNED_DATA"), 0600) != nil {
		t.Fatal("sentinel")
	}
	command.Action = "remove"
	command.Revision = 3
	if _, e = fresh.Execute(context.Background(), node, "00000000-0000-0000-0000-000000000007", command, download); e == nil || e.Error() != "INFERENCE_OWNERSHIP_CONFLICT" {
		t.Fatal("unknown private data was deleted", e)
	}
	if _, e = os.Stat(sentinel); e != nil {
		t.Fatal("sentinel lost")
	}
	if e = os.Remove(sentinel); e != nil {
		t.Fatal(e)
	}
	command.Revision = 4
	value, e = fresh.Execute(context.Background(), node, "00000000-0000-0000-0000-000000000008", command, download)
	if e != nil || value.Deployment == nil || value.Deployment.State != "not_installed" || engine.exists {
		t.Fatal("owned cleanup failed", e)
	}
	if _, e = os.Stat(m.deploymentPath(deployment)); !os.IsNotExist(e) {
		t.Fatal("private runtime cache retained unexpectedly")
	}
	if info, e := os.Stat(filepath.Join(m.modelPath(), "model.safetensors")); e != nil || info.Size() != 1503300328 {
		t.Fatal("shared verified model cache was deleted", e)
	}
	receipt, e := fresh.RemoveFenced(context.Background(), node, deployment)
	if e != nil || receipt.Validate() != nil || !receipt.ModelCachePresent {
		t.Fatal("explicit fenced cleanup receipt failed", e)
	}
	if _, e = fresh.RemoveFenced(context.Background(), "00000000-0000-0000-0000-000000000099", deployment); e == nil {
		t.Fatal("foreign node accepted local cleanup")
	}
	t.Log("Actual pinned model bytes, root UID/modes, fsynced journal, fresh-manager ack reconciliation, stale revision and preserved unknown files passed with identified synthetic engine/probes; not GPU qualification.")
}
func TestLinuxCancelledInferenceIntentHasNoNewExternalEffects(t *testing.T) {
	m, engine, gpu := inferenceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := wire.InferenceCommand{Action: "deploy", DeploymentID: "00000000-0000-0000-0000-000000000002", ModelID: "00000000-0000-0000-0000-000000000004", CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: gpu, Revision: 1}
	if _, e := m.Execute(ctx, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000005", command, nil); e == nil || e.Error() != "INFERENCE_CANCELLED" {
		t.Fatal(e)
	}
	if engine.creates != 0 || engine.starts != 0 {
		t.Fatal("cancelled intent created effects")
	}
	if _, e := os.Stat(m.journalPath()); !os.IsNotExist(e) {
		t.Fatal("cancelled fresh intent changed journal")
	}
}
