package training

import (
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func ContainerConfiguration(config Config, node, task, journal string, command wire.Command, modelPath, attemptPath string) (ContainerConfig, error) {
	var out ContainerConfig
	if config.Bind(command) != nil || !datawire.ValidID(node) || !datawire.ValidID(task) || !datawire.ValidID(journal) || command.ModelCatalogSHA256 != agentwire.ModelCatalogDigest() || modelPath != filepath.Join(config.RootDirectory, "models", command.ModelCatalogSHA256) || attemptPath != filepath.Join(config.RootDirectory, "jobs", command.Scope.JobID, command.AttemptID) {
		return out, fail("TRAINING_AGENT_RECIPE_MISMATCH")
	}
	p := config.Policy()
	scope := path.Join(wire.ContainerAttemptsDirectory, command.Scope.JobID, command.AttemptID)
	out = ContainerConfig{Image: config.ImageID, User: p.User(), WorkingDir: ImageWorkingDirectory, Entrypoint: []string{"python", WorkerFile}, Cmd: []string{"--request-file", path.Join(scope, "request.json")}, Labels: map[string]string{"projectone.owner": "node-agent", "projectone.node_id": node, "projectone.task_id": task, "projectone.journal_id": journal, "projectone.tenant_id": command.Scope.TenantID, "projectone.workspace_id": command.Scope.WorkspaceID, "projectone.job_id": command.Scope.JobID, "projectone.attempt_id": command.AttemptID, "projectone.gpu_uuid": command.GPUUUID, "projectone.training.worker_source_sha256": config.WorkerSourceSHA256}, ExposedPorts: map[string]struct{}{}}
	digest, _ := command.Digest()
	out.Labels["projectone.command_sha256"] = digest
	out.Env = []string{"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_DATASETS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "DO_NOT_TRACK=1", "TOKENIZERS_PARALLELISM=false", "NVIDIA_VISIBLE_DEVICES=" + command.GPUUUID, "NVIDIA_DRIVER_CAPABILITIES=compute,utility", "OMP_NUM_THREADS=" + strconv.FormatInt(command.Configuration.Threads, 10), "MKL_NUM_THREADS=" + strconv.FormatInt(command.Configuration.Threads, 10), "HOME=/tmp", "XDG_CACHE_HOME=/tmp/cache", "HF_HOME=/tmp/huggingface", "TORCH_HOME=/tmp/torch", "CUDA_CACHE_PATH=/tmp/cuda", "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1", "PYTHONNOUSERSITE=1"}
	out.HostConfig = invariantHost(p, command, modelPath, attemptPath, scope)
	return out, nil
}
func invariantHost(p Policy, command wire.Command, modelPath, attemptPath, scope string) HostConfig {
	r := command.Recipe.Resources
	h := HostConfig{HostConfig: inference.HostConfig{NetworkMode: "none", IpcMode: "private", ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Memory: r.MemoryBytes, MemorySwap: r.MemoryBytes, NanoCpus: command.Configuration.Threads * 1000000000, PidsLimit: p.PidsLimit, ShmSize: p.SharedMemoryBytes, DeviceRequests: []inference.DeviceRequest{{Driver: "nvidia", DeviceIDs: []string{command.GPUUUID}, Capabilities: [][]string{{"gpu"}}}}, Mounts: []inference.BindMount{{Type: "bind", Source: modelPath, Target: wire.ContainerModelDirectory, ReadOnly: true}, {Type: "bind", Source: attemptPath, Target: scope, ReadOnly: false}}, PortBindings: map[string][]inference.PortBinding{}, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=" + strconv.FormatInt(p.TemporaryMemoryBytes, 10) + ",mode=1777"}}, Ulimits: []Ulimit{{Name: "core", Soft: 0, Hard: 0}, {Name: "nofile", Soft: r.OpenFiles, Hard: r.OpenFiles}}, CgroupnsMode: "private"}
	h.RestartPolicy.Name = "no"
	h.LogConfig.Type = "json-file"
	h.LogConfig.Config = map[string]string{"max-size": strconv.FormatInt(p.LogMaximumBytes, 10), "max-file": strconv.Itoa(p.LogMaximumFiles)}
	return h
}
func mergedEnvironment(base, override []string) (map[string]string, bool) {
	out := map[string]string{}
	for _, values := range [][]string{base, override} {
		seen := map[string]bool{}
		for _, value := range values {
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

// VerifyContainer checks before every signal/removal and before adoption after uncertain create.
func VerifyContainer(actual Container, expected ContainerConfig, image Image, name, id string) error {
	if !containerID.MatchString(actual.ID) || id != "" && actual.ID != id || actual.Name != "/"+name || actual.Image != expected.Image || actual.Config.Image != expected.Image || actual.Config.User != expected.User || actual.Config.WorkingDir != expected.WorkingDir || !reflect.DeepEqual(actual.Config.Entrypoint, expected.Entrypoint) || !reflect.DeepEqual(actual.Config.Cmd, expected.Cmd) || len(actual.Config.ExposedPorts) != 0 {
		return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
	}
	for key, value := range expected.Labels {
		if actual.Config.Labels[key] != value {
			return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
		}
	}
	want, ok := mergedEnvironment(image.Config.Env, expected.Env)
	if !ok {
		return fail("TRAINING_IMAGE_INVALID")
	}
	got, ok := mergedEnvironment(nil, actual.Config.Env)
	if !ok || !reflect.DeepEqual(got, want) {
		return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
	}
	a, b := actual.HostConfig, expected.HostConfig
	if a.Privileged || a.AutoRemove || a.PublishAllPorts || !a.ReadonlyRootfs || a.NetworkMode != "none" || a.IpcMode != "private" || a.CgroupnsMode != "private" || a.PidMode != "" || a.UTSMode != "" || a.UsernsMode != "" || a.CgroupParent != "" || len(a.CapAdd) != 0 || len(a.Binds) != 0 || len(a.Devices) != 0 || len(a.VolumesFrom) != 0 || len(a.GroupAdd) != 0 || len(a.ExtraHosts) != 0 || len(a.Links) != 0 || len(a.Dns) != 0 || a.RestartPolicy != b.RestartPolicy || a.Memory != b.Memory || a.MemorySwap != b.MemorySwap || a.NanoCpus != b.NanoCpus || a.PidsLimit != b.PidsLimit || a.ShmSize != b.ShmSize || !reflect.DeepEqual(a.CapDrop, b.CapDrop) || !reflect.DeepEqual(a.SecurityOpt, b.SecurityOpt) || !reflect.DeepEqual(a.DeviceRequests, b.DeviceRequests) || !reflect.DeepEqual(a.Tmpfs, b.Tmpfs) || !reflect.DeepEqual(a.LogConfig, b.LogConfig) || !reflect.DeepEqual(a.Ulimits, b.Ulimits) || len(a.PortBindings) != 0 {
		return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
	}
	if len(actual.Mounts) != len(b.Mounts) {
		return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
	}
	seen := map[string]bool{}
	for _, mount := range actual.Mounts {
		matched := false
		for _, want := range b.Mounts {
			if mount.Type == want.Type && mount.Source == want.Source && mount.Destination == want.Target && mount.RW == !want.ReadOnly && !seen[mount.Destination] {
				matched = true
				seen[mount.Destination] = true
			}
		}
		if !matched {
			return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
		}
	}
	if len(actual.NetworkSettings.Networks) != 1 {
		return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
	}
	for name, network := range actual.NetworkSettings.Networks {
		if name != "none" || network.NetworkID != "" && !containerID.MatchString(network.NetworkID) {
			return fail("TRAINING_CONTAINER_OWNERSHIP_CONFLICT")
		}
	}
	return nil
}
