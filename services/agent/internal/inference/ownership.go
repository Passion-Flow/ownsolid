package inference

import (
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

func validStagePath(root, path string) bool {
	return filepath.Dir(path) == filepath.Join(root, "models") && regexp.MustCompile(`^\.staging-[a-f0-9-]{36}-[a-f0-9]{32}$`).MatchString(filepath.Base(path))
}
func environment(base, override []string) (map[string]string, bool) {
	out := map[string]string{}
	for _, list := range [][]string{base, override} {
		seen := map[string]bool{}
		for _, value := range list {
			key, body, ok := strings.Cut(value, "=")
			if !ok || key == "" || seen[key] {
				return nil, false
			}
			seen[key] = true
			out[key] = body
		}
	}
	return out, true
}
func (m *Manager) ownedContainer(ctx context.Context, j journal) (Container, error) {
	reference := j.ContainerID
	if reference == "" {
		reference = "projectone-vllm-" + j.Command.DeploymentID
	}
	container, e := m.docker.InspectContainer(ctx, reference)
	if e != nil {
		return container, e
	}
	image, e := m.docker.InspectApprovedImage(ctx)
	if e != nil {
		return container, e
	}
	expected, e := m.commandContainerConfiguration(j)
	if e != nil {
		return container, e
	}
	expected.Image = j.ImageID
	if j.ImageID == "" || container.Name != "/projectone-vllm-"+j.Command.DeploymentID || container.Image != j.ImageID || container.Config.Image != j.ImageID || container.Config.User != expected.User || container.Config.WorkingDir != expected.WorkingDir || !reflect.DeepEqual(container.Config.Cmd, expected.Cmd) || !reflect.DeepEqual(container.Config.Entrypoint, expected.Entrypoint) {
		return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	for key, value := range expected.Labels {
		if container.Config.Labels[key] != value {
			return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
	}
	wantEnvironment, ok := environment(image.Config.Env, expected.Env)
	if !ok {
		return container, fail("INFERENCE_IMAGE_INVALID")
	}
	actualEnvironment, ok := environment(nil, container.Config.Env)
	if !ok || !reflect.DeepEqual(wantEnvironment, actualEnvironment) {
		return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	host, want := container.HostConfig, expected.HostConfig
	if host.Privileged || host.AutoRemove || !host.ReadonlyRootfs || len(host.CapAdd) != 0 || len(host.Binds) != 0 || len(host.Devices) != 0 || host.PidMode != "" || host.UTSMode != "" || host.UsernsMode != "" || host.IpcMode != "private" && host.IpcMode != "" || host.NetworkMode != want.NetworkMode || !reflect.DeepEqual(host.Dns, want.Dns) || host.Memory != want.Memory || host.MemorySwap != want.MemorySwap || host.NanoCpus != want.NanoCpus || host.PidsLimit != want.PidsLimit || host.ShmSize != want.ShmSize || !reflect.DeepEqual(host.DeviceRequests, want.DeviceRequests) || !reflect.DeepEqual(host.PortBindings, want.PortBindings) || !reflect.DeepEqual(host.Tmpfs, want.Tmpfs) || !reflect.DeepEqual(host.CapDrop, want.CapDrop) || !reflect.DeepEqual(host.SecurityOpt, want.SecurityOpt) || !reflect.DeepEqual(host.LogConfig, want.LogConfig) {
		return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	if len(container.Mounts) != 2 {
		return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	seen := map[string]bool{}
	for _, mount := range container.Mounts {
		if seen[mount.Destination] {
			return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		seen[mount.Destination] = true
		if mount.Type != "bind" || !(mount.Destination == "/model" && mount.Source == m.commandModelPath(j.Command) && !mount.RW || mount.Destination == "/home/vllm/.cache" && mount.Source == m.cachePath(j.Command.DeploymentID) && mount.RW) {
			return container, fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
	}
	if len(container.NetworkSettings.Networks) != 1 {
		return container, fail("INFERENCE_NETWORK_CONFLICT")
	}
	for name, network := range container.NetworkSettings.Networks {
		if name != "projectone-inference-"+j.NodeID || network.NetworkID != j.NetworkID && !(network.NetworkID == "" && !container.State.Running && container.State.Status == "created") {
			return container, fail("INFERENCE_NETWORK_CONFLICT")
		}
	}
	if e = m.docker.VerifyNetworkEndpoint(ctx, j.NodeID, j.NetworkID, container.ID); e != nil {
		return container, e
	}
	return container, nil
}
