package inference

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type DeviceRequest struct {
	Driver       string
	DeviceIDs    []string
	Capabilities [][]string
}
type PortBinding struct {
	HostIP   string
	HostPort string
}
type BindMount struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}
type HostConfig struct {
	NetworkMode    string
	Dns            []string
	CapAdd         []string
	Binds          []string
	Devices        []any
	PidMode        string
	IpcMode        string
	UTSMode        string
	UsernsMode     string
	ReadonlyRootfs bool
	Privileged     bool
	CapDrop        []string
	SecurityOpt    []string
	Memory         int64
	MemorySwap     int64
	NanoCpus       int64
	PidsLimit      int64
	ShmSize        int64
	DeviceRequests []DeviceRequest
	Mounts         []BindMount
	PortBindings   map[string][]PortBinding
	Tmpfs          map[string]string
	LogConfig      struct {
		Type   string
		Config map[string]string
	}
	AutoRemove bool
}
type ContainerConfig struct {
	Image        string
	User         string
	Entrypoint   []string
	Cmd          []string
	Env          []string
	Labels       map[string]string
	WorkingDir   string
	ExposedPorts map[string]struct{}
	HostConfig   HostConfig
}

var gpuID = regexp.MustCompile(`^GPU-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var protectedPath = regexp.MustCompile(`^/[A-Za-z0-9_/-]+$`)

func ContainerConfiguration(deployment, node, gpu, modelPath, cachePath, network string) (ContainerConfig, error) {
	return containerConfiguration(deployment, node, gpu, modelPath, cachePath, network, wire.ModelCatalogDigest(), "float16", "")
}

func DerivedContainerConfiguration(c wire.InferenceCommand, node, modelPath, cachePath, network string) (ContainerConfig, error) {
	if c.Derived == nil || c.Validate(wire.DerivedInferenceOperation) != nil || c.Action != "deploy" {
		return ContainerConfig{}, fail("INFERENCE_CONFIGURATION_INVALID")
	}
	return containerConfiguration(c.DeploymentID, node, c.GPUUUID, modelPath, cachePath, network, c.CatalogDigest, wire.ApprovedDerivedServingRecipe().Dtype, c.Derived.ServingRecipeSHA256)
}
func containerConfiguration(deployment, node, gpu, modelPath, cachePath, network, digest, dtype, servingRecipe string) (ContainerConfig, error) {
	var out ContainerConfig
	if !deploymentID.MatchString(deployment) || !deploymentID.MatchString(node) || !gpuID.MatchString(gpu) || !containerID.MatchString(network) {
		return out, fail("INFERENCE_CONFIGURATION_INVALID")
	}
	if !protectedPath.MatchString(modelPath) || !protectedPath.MatchString(cachePath) || filepath.Clean(modelPath) != modelPath || filepath.Clean(cachePath) != cachePath {
		return out, fail("INFERENCE_CONFIGURATION_INVALID")
	}
	modelSuffix := "/models/" + digest
	cacheSuffix := "/deployments/" + deployment + "/cache"
	base := strings.TrimSuffix(modelPath, modelSuffix)
	if !strings.HasSuffix(modelPath, modelSuffix) || !strings.HasSuffix(cachePath, cacheSuffix) || len(base) < 5 || strings.TrimSuffix(cachePath, cacheSuffix) != base {
		return out, fail("INFERENCE_CONFIGURATION_INVALID")
	}
	recipe := wire.ApprovedInferenceRecipe()
	resources := wire.ApprovedInferenceResources()
	port := strconv.Itoa(recipe.ContainerPort) + "/tcp"
	out = ContainerConfig{Image: recipe.ImageConfigDigest, User: recipe.ContainerUser, Entrypoint: []string{"vllm", "serve"}, WorkingDir: "/home/vllm", Cmd: []string{"/model", "--tokenizer", "/model", "--served-model-name", wire.InferenceModelName, "--host", "0.0.0.0", "--port", strconv.Itoa(recipe.ContainerPort), "--dtype", dtype, "--max-model-len", strconv.Itoa(recipe.MaximumContext), "--max-num-seqs", strconv.Itoa(recipe.MaximumSequences), "--gpu-memory-utilization", strconv.FormatFloat(recipe.GPUMemoryUtilization, 'f', -1, 64), "--enforce-eager"},
		Labels: map[string]string{"projectone.owner": "node-agent", "projectone.node_id": node, "projectone.deployment_id": deployment, "projectone.model_digest": digest, "projectone.gpu_uuid": gpu}, ExposedPorts: map[string]struct{}{port: {}},
		Env:        []string{"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_DATASETS_OFFLINE=1", "DO_NOT_TRACK=1", "VLLM_NO_USAGE_STATS=1", "VLLM_USAGE_SOURCE=projectone-offline", "VLLM_ENABLE_CUDA_COMPATIBILITY=0", "NVIDIA_VISIBLE_DEVICES=" + gpu, "NVIDIA_DRIVER_CAPABILITIES=compute,utility", "HOME=/home/vllm", "XDG_CACHE_HOME=/home/vllm/.cache", "HF_HOME=/home/vllm/.cache/huggingface", "UV_CACHE_DIR=/home/vllm/.cache/uv", "TORCHINDUCTOR_CACHE_DIR=/home/vllm/.cache/torchinductor", "TRITON_CACHE_DIR=/home/vllm/.cache/triton", "CUDA_CACHE_PATH=/home/vllm/.cache/cuda", "OMP_NUM_THREADS=" + strconv.Itoa(resources.CPUThreads), "MKL_NUM_THREADS=" + strconv.Itoa(resources.CPUThreads), "TOKENIZERS_PARALLELISM=false"},
		HostConfig: HostConfig{NetworkMode: network, Dns: workloadResolvers(), ReadonlyRootfs: true, Privileged: false, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Memory: resources.MemoryBytes, MemorySwap: resources.MemoryBytes, NanoCpus: resources.NanoCPUs, PidsLimit: resources.PidsLimit, ShmSize: resources.SharedMemoryBytes, DeviceRequests: []DeviceRequest{{Driver: "nvidia", DeviceIDs: []string{gpu}, Capabilities: [][]string{{"gpu"}}}}, Mounts: []BindMount{{Type: "bind", Source: modelPath, Target: "/model", ReadOnly: true}, {Type: "bind", Source: cachePath, Target: "/home/vllm/.cache", ReadOnly: false}}, PortBindings: map[string][]PortBinding{port: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(recipe.HostPort)}}}, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,size=" + strconv.FormatInt(resources.TemporaryMemoryBytes, 10) + ",mode=1777"}},
	}
	if servingRecipe != "" {
		out.Cmd = append(out.Cmd, "--no-trust-remote-code", "--default-chat-template-kwargs", `{"enable_thinking":false}`)
		out.Labels["projectone.model_kind"] = wire.DerivedModelKind
		out.Labels["projectone.serving_recipe_sha256"] = servingRecipe
	}
	out.HostConfig.LogConfig.Type = "json-file"
	out.HostConfig.LogConfig.Config = map[string]string{"max-size": resources.LogMaxSize, "max-file": strconv.Itoa(resources.LogMaxFiles)}
	return out, nil
}
