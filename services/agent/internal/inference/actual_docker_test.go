//go:build docker_integration

package inference

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"testing"
)

func TestActualPinnedImageMetadataOverLocalDockerUnixAPI(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	if socket == "" {
		t.Fatal("explicit actual local Docker socket required")
	}
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	image, e := client.InspectApprovedImage(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("actual pinned amd64 image metadata verified through API 1.45; image ID %s; Docker Desktop storage/CPU emulation does not establish NVIDIA loading or generation", image.ID)
}

func TestActualDockerCreatedContainerSchemaWithoutDeviceExecution(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	if socket == "" {
		t.Fatal("explicit local Docker socket required")
	}
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	image, e := client.InspectApprovedImage(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	node := fmt.Sprintf("%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:16])
	deployment := node
	base, e := os.MkdirTemp(os.Getenv("TMPDIR"), "engine-schema-")
	if e != nil {
		t.Fatal(e)
	}
	model := filepath.Join(base, "models", wire.ModelCatalogDigest())
	cache := filepath.Join(base, "deployments", deployment, "cache")
	if e = os.MkdirAll(model, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(cache, 0700); e != nil {
		t.Fatal(e)
	}
	network, e := client.EnsureNetwork(context.Background(), node)
	if e != nil {
		t.Fatal(e)
	}
	var created string
	t.Cleanup(func() {
		if created != "" {
			value, e := client.InspectContainer(context.Background(), created)
			if e != nil || value.State.Running || value.Config.Labels["projectone.schema_fixture"] != node {
				t.Error("owned schema fixture cleanup uncertain; preserved")
				return
			}
			if e = client.RemoveContainer(context.Background(), created); e != nil {
				t.Error(e)
				return
			}
		}
		if e := client.RemoveUnusedNetwork(context.Background(), node); e != nil {
			t.Error(e)
			return
		}
		os.RemoveAll(base)
	})
	configuration, e := ContainerConfiguration(deployment, node, "GPU-00000000-0000-0000-0000-000000000003", model, cache, network)
	if e != nil {
		t.Fatal(e)
	}
	configuration.Image = image.ID
	configuration.HostConfig.DeviceRequests = nil
	configuration.Labels["projectone.schema_fixture"] = node
	// Only this identified schema fixture omits devices. It is never started and is not a deployment/GPU test.
	var result struct {
		ID string `json:"Id"`
	}
	if e = client.request(context.Background(), "POST", "/containers/create?name=projectone-vllm-"+deployment, configuration, &result, 201); e != nil {
		t.Fatal(e)
	}
	created = result.ID
	value, e := client.InspectContainer(context.Background(), created)
	if e != nil || value.State.Running {
		t.Fatal("schema fixture unexpectedly executing", e)
	}
	if value.HostConfig.Privileged || !value.HostConfig.ReadonlyRootfs || value.HostConfig.Memory != configuration.HostConfig.Memory || len(value.HostConfig.PortBindings) != 1 {
		t.Fatal("actual resource/port schema mismatch")
	}
	summary := struct {
		State           string
		SecurityOptions []string
		Capabilities    []string
		IPC             string
		Mounts          []Mount
		Network         map[string]struct{ NetworkID string }
		Image           string
	}{value.State.Status, value.HostConfig.SecurityOpt, value.HostConfig.CapDrop, value.HostConfig.IpcMode, value.Mounts, value.NetworkSettings.Networks, value.Image}
	body, _ := json.Marshal(summary)
	t.Log("Actual never-started Docker container schema, explicitly device-free fixture: " + string(body))
}

func TestActualOwnedLoopbackDockerNetworkLifecycle(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	if socket == "" {
		t.Fatal("explicit local Docker socket required")
	}
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	node := fmt.Sprintf("%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:16])
	if _, e = client.inspectNetwork(context.Background(), "projectone-inference-"+node); e == nil || e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
		t.Fatal("new isolated network scope required", e)
	}
	id, e := client.EnsureNetwork(context.Background(), node)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := client.RemoveUnusedNetwork(context.Background(), node); e != nil {
			t.Errorf("exact owned network cleanup failed: %s", e.Error())
		}
	})
	value, e := client.inspectNetwork(context.Background(), id)
	if e != nil || !ownedNetwork(value, node) || len(value.Containers) != 0 {
		t.Fatal("actual owned bridge network identity invalid", e)
	}
	again, e := client.EnsureNetwork(context.Background(), node)
	if e != nil || again != id {
		t.Fatal("network reconciliation created another resource", e)
	}
	t.Log("Actual isolated owned loopback bridge created, reconciled and ownership-checked; no GPU/container execution or public listener.")
}
