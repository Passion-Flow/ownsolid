package v1

import (
	_ "embed"
	"encoding/json"
)

//go:embed inference-resources.json
var inferenceResources []byte

// InferenceResources is the versioned M06 container recipe, including its resource envelope.
// Ownership reconciliation requires the same profile when recovering an existing container.
// Deployment-specific resource profiles require a versioned lifecycle contract migration.
type InferenceResources struct {
	MemoryBytes          int64  `json:"memory_bytes"`
	NanoCPUs             int64  `json:"nano_cpus"`
	PidsLimit            int64  `json:"pids_limit"`
	SharedMemoryBytes    int64  `json:"shared_memory_bytes"`
	TemporaryMemoryBytes int64  `json:"temporary_memory_bytes"`
	CPUThreads           int    `json:"cpu_threads"`
	LogMaxSize           string `json:"log_max_size"`
	LogMaxFiles          int    `json:"log_max_files"`
}

func ApprovedInferenceResources() InferenceResources {
	var value InferenceResources
	if json.Unmarshal(inferenceResources, &value) != nil {
		panic("COMPILED_INFERENCE_RESOURCES_INVALID")
	}
	return value
}
