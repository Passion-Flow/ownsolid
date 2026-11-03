//go:build model_lifecycle_docker_integration

package modelpreparations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func actualPreparationDocker(t *testing.T) (*Docker, Config) {
	t.Helper()
	config := fixtureConfig()
	config.DockerSocket = os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	config.ImageID = os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_ID")
	config.ImageReference = os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_REFERENCE")
	config.WorkerSourceSHA256 = os.Getenv("PROJECT_ONE_PREPARATION_WORKER_SOURCE")
	if config.Validate() != nil {
		t.Fatal("verified actual lifecycle image receipt required")
	}
	d, e := NewDocker(config.DockerSocket, settings.Default())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(d.Close)
	image, e := d.InspectImage(context.Background(), config)
	if e != nil || image.ID != config.ImageID {
		t.Fatal("actual manufactured image contract", e)
	}
	return d, config
}
func TestActualManufacturedImageDeviceFreeContainerOwnershipAndGracefulStop(t *testing.T) {
	d, config := actualPreparationDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	resolved, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	config.RootDirectory = resolved
	command := fixtureCommand()
	command.AttemptID, e = newUUID()
	if e != nil {
		t.Fatal(e)
	}
	command.Recipe.ImageReference = config.ImageReference
	command.Recipe.Identity.ImageID = &config.ImageID
	command.Recipe.Identity.WorkerSourceSHA256 = config.WorkerSourceSHA256
	command.Recipe.Resources.MemoryBytes = 134217728
	model := filepath.Join(config.RootDirectory, "models", command.ModelCatalogSHA256)
	attempt := filepath.Join(config.RootDirectory, "preparations", command.Scope.TenantID, command.Scope.WorkspaceID, command.Scope.PreparationID, command.AttemptID)
	for _, path := range []string{model, attempt} {
		if e = os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	id, e := newUUID()
	if e != nil {
		t.Fatal(e)
	}
	expected, e := ContainerConfiguration(config, fixtureID, fixtureID, id, command, model, attempt)
	if e != nil {
		t.Fatal(e)
	}
	// Explicit device-free fixture: only these two fields differ from product rendering. No preparation/GPU claim.
	expected.HostConfig.DeviceRequests = nil
	expected.Entrypoint = []string{"python"}
	expected.Cmd = []string{"-I", "-B", "-c", "import time;time.sleep(60)"}
	expected.Labels["projectone.preparation_device_free_id"] = id
	scope := os.Getenv("PROJECT_ONE_ACTUAL_DOCKER_SCOPE")
	if !datawire.ValidID(scope) {
		t.Fatal("isolated actual Docker scope required")
	}
	expected.Labels["projectone.preparation_device_free_scope"] = scope
	name := wire.ContainerName(command.AttemptID)
	image, e := d.InspectImage(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	container, e := d.Create(ctx, name, expected)
	if e != nil {
		t.Fatal(e)
	}
	cleanup := func() {
		bounded, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		value, e := d.InspectContainer(bounded, container)
		if notFound(e) {
			return
		}
		if e != nil || VerifyContainer(value, expected, image, name, container) != nil {
			t.Error("device-free fixture cleanup ownership uncertain; retained")
			return
		}
		if value.State.Running {
			if e = d.Stop(bounded, container, time.Second); e != nil {
				t.Error(e)
				return
			}
		}
		value, e = d.InspectContainer(bounded, container)
		if e != nil || value.State.Running || VerifyContainer(value, expected, image, name, container) != nil {
			t.Error("device-free fixture stop uncertain; retained")
			return
		}
		if e = d.Remove(bounded, container); e != nil {
			t.Error(e)
		}
	}
	t.Cleanup(cleanup)
	value, e := d.InspectContainer(ctx, container)
	if e != nil || VerifyContainer(value, expected, image, name, container) != nil || value.State.Running {
		t.Fatal("actual never-started owned schema", e)
	}
	if e = d.Start(ctx, container); e != nil {
		t.Fatal(e)
	}
	value, e = d.InspectContainer(ctx, container)
	if e != nil || !value.State.Running || VerifyContainer(value, expected, image, name, container) != nil {
		t.Fatal("actual bounded device-free process did not start", e)
	}
	wrong := expected
	wrong.Labels = map[string]string{"projectone.journal_id": "foreign"}
	if VerifyContainer(value, wrong, image, name, container) == nil {
		t.Fatal("foreign ownership proof accepted")
	}
	if e = d.Stop(ctx, container, time.Second); e != nil {
		t.Fatal(e)
	}
	value, e = d.InspectContainer(ctx, container)
	if e != nil || value.State.Running || VerifyContainer(value, expected, image, name, container) != nil {
		t.Fatal("actual stop did not join exact fixture", e)
	}
	if e = d.Remove(ctx, container); e != nil {
		t.Fatal(e)
	}
	if _, e = d.InspectContainer(ctx, name); !notFound(e) {
		t.Fatal("removed name still exists", e)
	}
	if _, e = d.InspectContainer(ctx, container); !notFound(e) {
		t.Fatal("removed ID still exists", e)
	}
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output != "" {
		raw, _ := json.MarshalIndent(map[string]any{"scope": "Actual manufactured amd64 image, explicit device-free sleep process and real Docker API1.45 ownership/resources/stop/removal. Not preparation or CUDA evidence.", "image_id": image.ID, "container_image_id": value.Image, "memory_bytes": value.HostConfig.Memory, "memory_swap_bytes": value.HostConfig.MemorySwap, "cpus_nano": value.HostConfig.NanoCpus, "user": value.Config.User, "network": value.HostConfig.NetworkMode, "state_after_stop": value.State.Status, "exit_code": value.State.ExitCode, "name_absent": true, "id_absent": true}, "", "  ")
		if e = os.WriteFile(filepath.Join(output, "actual-device-free-container.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestActualManufacturedImageCgroupOOMIsObservedBeforeRemoval(t *testing.T) {
	d, config := actualPreparationDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	config.RootDirectory = root
	command := fixtureCommand()
	command.AttemptID, e = newUUID()
	if e != nil {
		t.Fatal(e)
	}
	command.Recipe.ImageReference = config.ImageReference
	command.Recipe.Identity.ImageID = &config.ImageID
	command.Recipe.Identity.WorkerSourceSHA256 = config.WorkerSourceSHA256
	command.Recipe.Resources.MemoryBytes = 134217728
	model := filepath.Join(root, "models", command.ModelCatalogSHA256)
	attempt := filepath.Join(root, "preparations", command.Scope.TenantID, command.Scope.WorkspaceID, command.Scope.PreparationID, command.AttemptID)
	for _, path := range []string{model, attempt} {
		if e = os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	id, e := newUUID()
	if e != nil {
		t.Fatal(e)
	}
	expected, e := ContainerConfiguration(config, fixtureID, fixtureID, id, command, model, attempt)
	if e != nil {
		t.Fatal(e)
	}
	// Explicit device-free allocation fault; the product worker is never given this command override.
	expected.HostConfig.DeviceRequests = nil
	expected.Entrypoint = []string{"python"}
	expected.Cmd = []string{"-I", "-B", "-c", "x=bytearray(536870912);print(len(x))"}
	expected.Labels["projectone.preparation_device_free_id"] = id
	scope := os.Getenv("PROJECT_ONE_ACTUAL_DOCKER_SCOPE")
	if !datawire.ValidID(scope) {
		t.Fatal("isolated actual Docker scope required")
	}
	expected.Labels["projectone.preparation_device_free_scope"] = scope
	name := wire.ContainerName(command.AttemptID)
	image, e := d.InspectImage(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	container, e := d.Create(ctx, name, expected)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		bounded, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		value, e := d.InspectContainer(bounded, container)
		if notFound(e) {
			return
		}
		if e != nil || VerifyContainer(value, expected, image, name, container) != nil {
			t.Error("OOM fixture cleanup uncertain; retained")
			return
		}
		if value.State.Running {
			if e = d.Stop(bounded, container, time.Second); e != nil {
				t.Error(e)
				return
			}
		}
		value, e = d.InspectContainer(bounded, container)
		if e != nil || value.State.Running || VerifyContainer(value, expected, image, name, container) != nil {
			t.Error("OOM fixture ownership changed; retained")
			return
		}
		if e = d.Remove(bounded, container); e != nil {
			t.Error(e)
		}
	})
	if e = d.Start(ctx, container); e != nil {
		t.Fatal(e)
	}
	var value Container
	for {
		value, e = d.InspectContainer(ctx, container)
		if e != nil || VerifyContainer(value, expected, image, name, container) != nil {
			t.Fatal("actual OOM fixture ownership", e)
		}
		if !value.State.Running && value.State.Status == "exited" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual cgroup OOM did not terminate bounded fixture")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !value.State.OOMKilled || value.State.ExitCode == 0 {
		t.Fatal("real cgroup failure was not observed truthfully")
	}
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output != "" {
		raw, _ := json.MarshalIndent(map[string]any{"scope": "Actual explicit device-free Python allocation under cgroup memory128MiB. No CUDA/preparation or performance claim.", "memory_bytes": value.HostConfig.Memory, "oom_killed": value.State.OOMKilled, "exit_code": value.State.ExitCode, "image_id": value.Image}, "", "  ")
		if e = os.WriteFile(filepath.Join(output, "actual-device-free-oom.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
