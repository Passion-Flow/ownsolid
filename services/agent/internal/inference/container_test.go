package inference

import (
	"encoding/json"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestApprovedContainerCannotEscapeRecipeOrExposePublicPort(t *testing.T) {
	deployment := "00000000-0000-0000-0000-000000000001"
	node := "00000000-0000-0000-0000-000000000002"
	gpu := "GPU-00000000-0000-0000-0000-000000000003"
	base := "/opt/projectone-test/q-12345678/inference"
	model := base + "/models/" + wire.ModelCatalogDigest()
	cache := base + "/deployments/" + deployment + "/cache"
	network := strings.Repeat("a", 64)
	value, e := ContainerConfiguration(deployment, node, gpu, model, cache, network)
	if e != nil {
		t.Fatal(e)
	}
	if value.Image != wire.ApprovedInferenceRecipe().ImageConfigDigest || value.User != "2000:0" || value.HostConfig.Privileged || !value.HostConfig.ReadonlyRootfs || value.HostConfig.AutoRemove || value.HostConfig.NetworkMode != network {
		t.Fatal("container privilege/image/ownership policy drift")
	}
	if len(value.HostConfig.Dns) != 1 || value.HostConfig.Dns[0] != unreachableResolver {
		t.Fatal("embedded resolver may forward through the host's loopback resolver", value.HostConfig.Dns)
	}
	if binding := value.HostConfig.PortBindings["8000/tcp"]; len(binding) != 1 || binding[0].HostIP != "127.0.0.1" || binding[0].HostPort != "18000" {
		t.Fatal("public backend exposure")
	}
	if len(value.HostConfig.DeviceRequests) != 1 || len(value.HostConfig.DeviceRequests[0].DeviceIDs) != 1 || value.HostConfig.DeviceRequests[0].DeviceIDs[0] != gpu {
		t.Fatal("GPU scope widened")
	}
	if len(value.HostConfig.Mounts) != 2 || value.HostConfig.Mounts[0].Source != model || !value.HostConfig.Mounts[0].ReadOnly || value.HostConfig.Mounts[1].Source != cache {
		t.Fatal("mutable/shared model or unbounded mount")
	}
	// Preserve the pre-refactor deployed container identity, including resource ownership.
	if value.HostConfig.Memory != 8589934592 || value.HostConfig.MemorySwap != value.HostConfig.Memory || value.HostConfig.NanoCpus != 2000000000 || value.HostConfig.PidsLimit != 256 || value.HostConfig.ShmSize != 1073741824 || value.HostConfig.Tmpfs["/tmp"] != "rw,nosuid,nodev,size=1073741824,mode=1777" || value.HostConfig.LogConfig.Config["max-file"] != "2" {
		t.Fatal("approved container resource profile changed")
	}
	encoded, _ := json.Marshal(value)
	for _, bad := range []string{"--trust-remote-code", "docker.sock", "--api-key", "HF_TOKEN=", "--ipc=host"} {
		if strings.Contains(string(encoded), bad) {
			t.Fatal("unapproved runtime authority")
		}
	}
	for _, input := range [][3]string{{"/", cache, network}, {model, "/unowned/cache", network}, {model, cache, "host"}, {model, cache, "bridge"}, {base + "/models/../unowned", cache, network}} {
		if _, e = ContainerConfiguration(deployment, node, gpu, input[0], input[1], input[2]); e == nil {
			t.Fatal("unapproved execution configuration accepted")
		}
	}
}
