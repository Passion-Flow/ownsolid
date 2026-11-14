//go:build linux && inference_integration && derived_inference_integration

package inference

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestLinuxActualDerivedCacheInterruptedDownloadAckLossRestartAndFencedCleanup(t *testing.T) {
	m, engine, gpu := inferenceFixture(t)
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_DERIVED_COMMAND"))
	var c wire.InferenceCommand
	if e != nil || json.Unmarshal(raw, &c) != nil || c.Validate(wire.DerivedInferenceOperation) != nil {
		t.Fatal("source-bound actual model catalog required", e)
	}
	c.GPUUUID = gpu
	m.gpu = func(context.Context) ([]wire.NVIDIAGPU, []string, error) {
		return []wire.NVIDIAGPU{{UUID: gpu, Name: "Explicit synthetic BF16 test GPU", DriverVersion: "580.65.06", MemoryTotalMiB: 16384, MemoryFreeMiB: 16000, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, nil, nil
	}
	node := "00000000-0000-0000-0000-000000000001"
	task := "00000000-0000-0000-0000-000000000005"
	source := os.Getenv("PROJECT_ONE_DERIVED_DIRECTORY")
	interrupted := true
	downloads := 0
	download := func(ctx context.Context, f wire.ModelFile, destination string) error {
		downloads++
		input, e := os.Open(filepath.Join(source, f.Path))
		if e != nil {
			return e
		}
		defer input.Close()
		output, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		if interrupted {
			interrupted = false
			output.Write([]byte("synthetic interrupted transfer"))
			output.Close()
			return fail("INFERENCE_MODEL_INVALID")
		}
		// Match the real HTTP download reader. A local File-to-File copy can
		// select copy_file_range/sendfile under emulation instead of bounded streaming.
		_, e = io.CopyBuffer(output, struct{ io.Reader }{input}, make([]byte, 32*1024))
		if e == nil {
			e = output.Sync()
		}
		closed := output.Close()
		if e != nil {
			return e
		}
		return closed
	}
	if _, e = m.Execute(context.Background(), node, task, c, download); e == nil || engine.creates != 0 {
		t.Fatal("interrupted bytes adopted", e)
	}
	j, e := m.read()
	if e != nil || len(j.Stages) != 1 {
		t.Fatal("interrupted stage not retained", e)
	}
	firstStage := j.Stages[0]
	engine.loseCreateAck = true
	if _, e = m.Execute(context.Background(), node, task, c, download); e == nil || e.Error() != "INFERENCE_DOCKER_UNAVAILABLE" || engine.creates != 1 {
		t.Fatal("lost creation acknowledgment", e)
	}
	if _, e = os.Stat(firstStage); e != nil {
		t.Fatal("partial evidence was pruned", e)
	}
	fresh := *m
	out, e := fresh.Execute(context.Background(), node, task, c, download)
	if e != nil || out.Deployment == nil || !out.Deployment.Ready || !c.MatchesServingIdentity(*out.Deployment) || engine.creates != 1 || engine.starts != 1 {
		t.Fatal("private derived journal restart", e)
	}
	if e = fresh.verifyCommandCache(context.Background(), c); e != nil {
		t.Fatal("actual nine-file cache hash", e)
	}
	if _, e = os.Stat(m.modelPath()); !os.IsNotExist(e) {
		t.Fatal("base cache namespace overwritten")
	}
	if !strings.Contains(strings.Join(engine.spec.Cmd, " "), "--dtype bfloat16") || engine.spec.Labels["projectone.serving_recipe_sha256"] != c.Derived.ServingRecipeSHA256 {
		t.Fatal("base engine spec substituted")
	}
	prior := engine.container.Config.Labels["projectone.serving_recipe_sha256"]
	engine.container.Config.Labels["projectone.serving_recipe_sha256"] = strings.Repeat("f", 64)
	if observed := fresh.Observe(context.Background(), node); observed.Deployment == nil || observed.Deployment.Ready || observed.Deployment.ErrorCode != "INFERENCE_OWNERSHIP_CONFLICT" {
		t.Fatal("changed recipe ownership accepted")
	}
	engine.container.Config.Labels["projectone.serving_recipe_sha256"] = prior
	// Actual file corruption must fail independently of a correct owning receipt.
	config := filepath.Join(fresh.commandModelPath(c), "config.json")
	content, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	if os.Chmod(config, 0600) != nil || os.WriteFile(config, append([]byte("x"), content[1:]...), 0600) != nil {
		t.Fatal("owned corruption fixture")
	}
	if e = fresh.verifyCommandCache(context.Background(), c); e == nil {
		t.Fatal("corrupted cache accepted")
	}
	if os.WriteFile(config, content, 0600) != nil || os.Chmod(config, 0444) != nil {
		t.Fatal("owned repair fixture")
	}
	if e = fresh.verifyCommandCache(context.Background(), c); e != nil {
		t.Fatal("repaired bytes refused", e)
	}
	// Stop/remove require neither a healthy driver nor new-allocation host memory.
	fresh.gpu = func(context.Context) ([]wire.NVIDIAGPU, []string, error) {
		return nil, nil, fail("INFERENCE_NVIDIA_UNAVAILABLE")
	}
	fresh.hostAvailable = func() error { return fail("INFERENCE_HOST_MEMORY_LOW") }
	receipt, e := fresh.RemoveFenced(context.Background(), node, c.DeploymentID)
	if e != nil || receipt.Validate() != nil || receipt.Derived == nil || !receipt.ModelCachePresent || engine.exists {
		t.Fatal("typed positive fenced cleanup", e)
	}
	if _, e = os.Stat(firstStage); !os.IsNotExist(e) {
		t.Fatal("owned incomplete stage not cleaned")
	}
	if _, e = os.Stat(fresh.commandModelPath(c)); e != nil {
		t.Fatal("verified model cache removed", e)
	}
	if downloads != 10 {
		t.Fatal("unexpected model downloads", downloads)
	}
	t.Log("Actual nine-file native BF16 bytes, independent hashes/root modes/single-link protection/private journal/publication/restart/corruption and retained typed cleanup passed in explicit Linux amd64 emulation. Catalog approval, GPU, Docker engine, generation and physical stop are explicit test doubles; not target qualification.")
}
