package v1

import (
	_ "embed"
	"encoding/json"
	"time"
)

//go:embed inference-recipe.json
var inferenceRecipe []byte

type InferenceRecipe struct {
	RecipeVersion            int     `json:"recipe_version"`
	Engine                   string  `json:"engine"`
	Version                  string  `json:"version"`
	ImageRepository          string  `json:"image_repository"`
	ImageManifestDigest      string  `json:"image_manifest_digest"`
	ImageConfigDigest        string  `json:"image_config_digest"`
	Architecture             string  `json:"architecture"`
	OS                       string  `json:"os"`
	CUDAVersion              string  `json:"cuda_version"`
	MinimumDriverMajor       int     `json:"minimum_driver_major"`
	MinimumComputeCapability float64 `json:"minimum_compute_capability"`
	MinimumGPUMemoryMiB      int64   `json:"minimum_gpu_memory_mib"`
	MinimumHostMemoryBytes   int64   `json:"minimum_host_memory_bytes"`
	HostPort                 int     `json:"host_port"`
	ContainerPort            int     `json:"container_port"`
	ContainerUser            string  `json:"container_user"`
	MaximumContext           int     `json:"maximum_context"`
	MaximumSequences         int     `json:"maximum_sequences"`
	GPUMemoryUtilization     float64 `json:"gpu_memory_utilization"`
	ModelKey                 string  `json:"model_key"`
}

type NVIDIAGPU struct {
	UUID              string    `json:"uuid"`
	Name              string    `json:"name"`
	DriverVersion     string    `json:"driver_version"`
	MemoryTotalMiB    int64     `json:"memory_total_mib"`
	MemoryFreeMiB     int64     `json:"memory_free_mib"`
	ComputeCapability float64   `json:"compute_capability"`
	MIGMode           string    `json:"mig_mode"`
	ObservedAt        time.Time `json:"observed_at"`
}

func ApprovedInferenceRecipe() InferenceRecipe {
	var value InferenceRecipe
	if json.Unmarshal(inferenceRecipe, &value) != nil {
		panic("COMPILED_INFERENCE_RECIPE_INVALID")
	}
	return value
}
