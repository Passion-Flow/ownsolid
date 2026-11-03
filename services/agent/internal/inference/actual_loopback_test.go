//go:build docker_integration && loopback_integration

package inference

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// Synthetic HTTP responses validate actual Engine owned-bridge/loopback transport only, never GPU inference.
func TestActualOwnedBridgeAndFixedLoopbackProbeWithoutDeviceOrModelExecution(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	binary := os.Getenv("PROJECT_ONE_LOOPBACK_AMD64_BINARY")
	if socket == "" || binary == "" {
		t.Fatal("EXPLICIT_LOCAL_SOCKET_AND_LOOPBACK_FIXTURE_REQUIRED")
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:18000")
	if e != nil {
		t.Fatal("existing loopback service preserved", e)
	}
	listener.Close()
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	image, e := client.InspectApprovedImage(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		t.Fatal(e)
	}
	node := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[0:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:16])
	network, e := client.EnsureNetwork(ctx, node)
	if e != nil {
		t.Fatal(e)
	}
	var created struct {
		ID string `json:"Id"`
	}
	t.Cleanup(func() {
		if created.ID != "" {
			value, e := client.InspectContainer(context.Background(), created.ID)
			if e != nil || value.Config.Labels["projectone.loopback_fixture"] != node {
				t.Error("changed loopback fixture preserved")
				return
			}
			if value.State.Running {
				if e = client.StopContainer(context.Background(), created.ID); e != nil {
					t.Error(e)
					return
				}
			}
			if e = client.RemoveContainer(context.Background(), created.ID); e != nil {
				t.Error(e)
				return
			}
		}
		if e = client.RemoveUnusedNetwork(context.Background(), node, network); e != nil {
			t.Error(e)
		}
	})
	spec := ContainerConfig{Image: image.ID, User: "2000:0", WorkingDir: "/", Entrypoint: []string{"/loopback-workload"}, Cmd: []string{}, Env: []string{"NVIDIA_VISIBLE_DEVICES=void", "GOMAXPROCS=2"}, Labels: map[string]string{"projectone.owner": "mvp-development", "projectone.loopback_fixture": node}, ExposedPorts: map[string]struct{}{"8000/tcp": {}}, HostConfig: HostConfig{NetworkMode: network, ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Memory: 64 * 1024 * 1024, MemorySwap: 64 * 1024 * 1024, NanoCpus: 1000000000, PidsLimit: 64, ShmSize: 8 * 1024 * 1024, Mounts: []BindMount{{Type: "bind", Source: binary, Target: "/loopback-workload", ReadOnly: true}}, PortBindings: map[string][]PortBinding{"8000/tcp": {{HostIP: "127.0.0.1", HostPort: "18000"}}}}}
	if e = client.request(ctx, "POST", "/containers/create?name=projectone-m06-loopback-"+node+"&platform=linux%2Famd64", spec, &created, 201); e != nil {
		t.Fatal(e)
	}
	if e = client.StartContainer(ctx, created.ID); e != nil {
		t.Fatal(e)
	}
	probe := localVerifier()
	defer probe.Close()
	deadline := time.Now().Add(15 * time.Second)
	for {
		e = probe.Health(ctx)
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			value, _ := client.InspectContainer(ctx, created.ID)
			t.Logf("fixture state=%+v, actual port map=%+v", value.State, value.NetworkSettings)
			t.Fatal("actual owned bridge/loopback unreachable", e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e = client.VerifyNetworkEndpoint(ctx, node, network, created.ID); e != nil {
		t.Fatal(e)
	}
	verification, e := probe.Generate(ctx)
	if e != nil || verification.OutputTokens != 1 {
		t.Fatal("actual loopback protocol transport failed", e)
	}
	inspected, e := client.InspectContainer(ctx, created.ID)
	if e != nil || len(inspected.HostConfig.DeviceRequests) != 0 || inspected.HostConfig.PortBindings["8000/tcp"][0].HostIP != "127.0.0.1" {
		t.Fatal("fixture exposed devices or a public listener", e)
	}
	t.Log("Actual owned owned Docker bridge,127.0.0.1-only host mapping and production fixed-address health/models/chat transport passed with an identified synthetic static Go HTTP fixture; no model/vLLM/CUDA execution or NVIDIA qualification.")
}
