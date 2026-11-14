//go:build docker_integration && oom_integration

package inference

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This actual kernel/Engine fixture runs only a static Go allocator, never vLLM/CUDA or a model.
func TestActualDockerCgroupOOMStateWithoutDeviceOrModelExecution(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	binary := os.Getenv("PROJECT_ONE_OOM_AMD64_BINARY")
	info, e := os.Stat(binary)
	if socket == "" || e != nil || !info.Mode().IsRegular() || !filepath.IsAbs(binary) {
		t.Fatal("EXPLICIT_LOCAL_SOCKET_AND_AMD64_OOM_BINARY_REQUIRED")
	}
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
	scope := fmt.Sprintf("%x", nonce[:])
	name := "projectone-m06-oom-" + scope
	spec := ContainerConfig{Image: image.ID, User: "2000:0", WorkingDir: "/", Entrypoint: []string{"/oom-workload"}, Cmd: []string{}, Env: []string{"NVIDIA_VISIBLE_DEVICES=void", "GOMAXPROCS=2"}, Labels: map[string]string{"projectone.owner": "mvp-development", "projectone.oom_fixture": scope}, HostConfig: HostConfig{NetworkMode: "none", ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Memory: 64 * 1024 * 1024, MemorySwap: 64 * 1024 * 1024, NanoCpus: 1000000000, PidsLimit: 64, ShmSize: 8 * 1024 * 1024, Mounts: []BindMount{{Type: "bind", Source: binary, Target: "/oom-workload", ReadOnly: true}}}}
	var created struct {
		ID string `json:"Id"`
	}
	if e = client.request(ctx, "POST", "/containers/create?name="+name+"&platform=linux%2Famd64", spec, &created, 201); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		value, e := client.InspectContainer(context.Background(), created.ID)
		if e != nil || value.Config.Labels["projectone.owner"] != "mvp-development" || value.Config.Labels["projectone.oom_fixture"] != scope {
			t.Error("changed OOM fixture preserved")
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
		}
	})
	if e = client.StartContainer(ctx, created.ID); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		value, e := client.InspectContainer(ctx, created.ID)
		if e != nil {
			t.Fatal(e)
		}
		if !value.State.Running {
			if !value.State.OOMKilled {
				t.Fatal("allocator exited without actual kernel OOM")
			}
			if len(value.HostConfig.DeviceRequests) != 0 || len(value.HostConfig.PortBindings) != 0 || value.HostConfig.Memory != 64*1024*1024 {
				t.Fatal("device-free isolated OOM recipe changed")
			}
			t.Log("Actual Docker cgroup OOMKilled mapping passed: one 256 MiB static Go allocation,64 MiB cap,one CPU,network none,no devices/model/vLLM execution. Linux amd64 process is emulated on this Mac; not NVIDIA qualification.")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("ACTUAL_CGROUP_OOM_TIMEOUT")
}
