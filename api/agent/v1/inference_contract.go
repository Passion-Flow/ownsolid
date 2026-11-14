package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"time"
)

const DockerInferenceProfile = "docker_inference"

// InferenceVersion 1.6 added the gateway tasks (GW-114). Each profile has its own version,
// and 1.4 and 1.5 belong to the AI and model lifecycle profiles.
const InferenceVersion = "1.6"
const InferenceImageMissing = "INFERENCE_IMAGE_MISSING"

// InferenceFreeMemoryPercent is the share of a GPU's memory that must be free before a model
// loads on it. Control refuses below it before queuing work; the agent checks again on the node.
const InferenceFreeMemoryPercent = 80
const InferenceModelName = "projectone-qwen3-0.6b"
const InferenceVerificationPrompt = "Reply with one word: hello"

var inferenceActions = map[string]bool{"deploy": true, "stop": true, "remove": true, "observe": true, "verify": true}

func InferenceOperation(action string) string { return "inference." + action + ".v1" }
func IsInferenceOperation(operation string) bool {
	if operation == DerivedInferenceOperation {
		return true
	}
	for action := range inferenceActions {
		if InferenceOperation(action) == operation {
			return true
		}
	}
	return false
}

var inferenceUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var inferenceGPUUUID = regexp.MustCompile(`^GPU-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type InferenceCommand struct {
	RegistryPinID string                    `json:"image_registry_pin_id,omitempty"`
	Derived       *DerivedInferenceIdentity `json:"derived,omitempty"`
	Action        string                    `json:"action"`
	DeploymentID  string                    `json:"deployment_id,omitempty"`
	ModelID       string                    `json:"model_id,omitempty"`
	CatalogDigest string                    `json:"catalog_digest,omitempty"`
	GPUUUID       string                    `json:"gpu_uuid,omitempty"`
	Revision      int64                     `json:"revision,omitempty"`
}

func (c InferenceCommand) Validate(operation string) error {
	if c.RegistryPinID != "" && (c.Action != "deploy" || !inferenceUUID.MatchString(c.RegistryPinID)) {
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	if c.Derived != nil {
		return c.validateDerived(operation)
	}

	if !inferenceActions[c.Action] || InferenceOperation(c.Action) != operation {
		return errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	if c.DeploymentID == "" {
		if c.Action == "observe" && c.ModelID == "" && c.CatalogDigest == "" && c.GPUUUID == "" && c.Revision == 0 {
			return nil
		}
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	if !inferenceUUID.MatchString(c.DeploymentID) || !inferenceUUID.MatchString(c.ModelID) || c.CatalogDigest != ModelCatalogDigest() || !inferenceGPUUUID.MatchString(c.GPUUUID) || c.Revision < 1 || c.Revision > 9007199254740991 {
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	return nil
}

type GenerationVerification struct {
	Model          string    `json:"model"`
	PromptSHA256   string    `json:"prompt_sha256"`
	ResponseSHA256 string    `json:"response_sha256"`
	InputTokens    int64     `json:"input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	ObservedAt     time.Time `json:"observed_at"`
}

func InferencePromptDigest() string {
	value := sha256.Sum256([]byte(InferenceVerificationPrompt))
	return hex.EncodeToString(value[:])
}
func (v GenerationVerification) Validate() error {
	if v.Model != InferenceModelName || v.PromptSHA256 != InferencePromptDigest() || !sha256Pattern.MatchString(v.ResponseSHA256) || v.InputTokens < 1 || v.InputTokens > 2048 || v.OutputTokens < 1 || v.OutputTokens > 16 || !inferenceTime(v.ObservedAt) {
		return errors.New("INFERENCE_RESPONSE_INVALID")
	}
	return nil
}

type DeploymentObservation struct {
	ModelKind           string                  `json:"model_kind,omitempty"`
	ServingRecipeSHA256 string                  `json:"serving_recipe_sha256,omitempty"`
	DeploymentID        string                  `json:"deployment_id"`
	ModelID             string                  `json:"model_id"`
	CatalogDigest       string                  `json:"catalog_digest"`
	GPUUUID             string                  `json:"gpu_uuid"`
	Revision            int64                   `json:"revision"`
	Managed             bool                    `json:"managed"`
	State               string                  `json:"state"`
	Ready               bool                    `json:"ready"`
	ContainerID         string                  `json:"container_id,omitempty"`
	ImageID             string                  `json:"image_id,omitempty"`
	Verification        *GenerationVerification `json:"verification,omitempty"`
	ErrorCode           string                  `json:"error_code,omitempty"`
}
type InferenceObservation struct {
	RegistryImageDelivery RegistryDeliveryCapability `json:"registry_image_delivery,omitempty"`
	ProbeErrorCode        string                     `json:"probe_error_code,omitempty"`
	GPUs                  []NVIDIAGPU                `json:"gpus"`
	Deployment            *DeploymentObservation     `json:"deployment,omitempty"`
	ObservedAt            time.Time                  `json:"observed_at"`
	ErrorCode             string                     `json:"error_code,omitempty"`
}

var inferenceErrors = map[string]bool{"": true, "INFERENCE_OPERATION_FAILED": true, "INFERENCE_CONFIGURATION_INVALID": true, "INFERENCE_DOCKER_UNAVAILABLE": true, "INFERENCE_DOCKER_REJECTED": true, "INFERENCE_DOCKER_NOT_FOUND": true, "INFERENCE_CONTAINER_CONFLICT": true, "INFERENCE_IMAGE_UNAVAILABLE": true, InferenceImageMissing: true, "INFERENCE_IMAGE_INVALID": true, "INFERENCE_NETWORK_CONFLICT": true, "INFERENCE_EGRESS_GUARD_UNAVAILABLE": true, "INFERENCE_NVIDIA_UNAVAILABLE": true, "INFERENCE_PLATFORM_UNSUPPORTED": true, "INFERENCE_HOST_MEMORY_LOW": true, "INFERENCE_GPU_PROBE_INVALID": true, "INFERENCE_GPU_UNAVAILABLE": true, "INFERENCE_DRIVER_UNSUPPORTED": true, "INFERENCE_GPU_UNSUPPORTED": true, "INFERENCE_MIG_UNSUPPORTED": true, "INFERENCE_GPU_MEMORY_LOW": true, "INFERENCE_GPU_BUSY": true, "INFERENCE_DOCKER_SOCKET_INVALID": true, "INFERENCE_RESOURCE_LIMITS_UNAVAILABLE": true, "INFERENCE_TOOLKIT_UNAVAILABLE": true, "INFERENCE_STATE_INVALID": true, "INFERENCE_IO_FAILED": true, "INFERENCE_CANCELLED": true, "INFERENCE_MODEL_INVALID": true, "INFERENCE_DISK_SPACE_LOW": true, "INFERENCE_PORT_CONFLICT": true, "INFERENCE_NOT_READY": true, "INFERENCE_RESPONSE_INVALID": true, "INFERENCE_LOAD_TIMEOUT": true, "INFERENCE_OOM": true, "INFERENCE_GPU_OOM": true, "INFERENCE_OWNERSHIP_CONFLICT": true, "INFERENCE_REVISION_CONFLICT": true, "INFERENCE_RECOVERY_REQUIRED": true}

func KnownInferenceError(value string) bool { return inferenceErrors[value] }
func inferenceTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 2020 && value.Year() <= 2200
}
func (o InferenceObservation) Validate() error {
	if o.GPUs == nil || len(o.GPUs) > 32 || !KnownInferenceError(o.ErrorCode) || !KnownInferenceError(o.ProbeErrorCode) || !inferenceTime(o.ObservedAt) {
		return errors.New("VALIDATION_FAILED")
	}
	seen := map[string]bool{}
	for _, gpu := range o.GPUs {
		if !inferenceGPUUUID.MatchString(gpu.UUID) || seen[gpu.UUID] || !text(gpu.Name, 128) || !text(gpu.DriverVersion, 32) || gpu.MemoryTotalMiB < 1 || gpu.MemoryTotalMiB > 1048576 || gpu.MemoryFreeMiB < 0 || gpu.MemoryFreeMiB > gpu.MemoryTotalMiB || math.IsNaN(gpu.ComputeCapability) || math.IsInf(gpu.ComputeCapability, 0) || gpu.ComputeCapability < 1 || gpu.ComputeCapability > 99 || (gpu.MIGMode != "Disabled" && gpu.MIGMode != "Enabled" && gpu.MIGMode != "[N/A]" && gpu.MIGMode != "N/A") || !inferenceTime(gpu.ObservedAt) {
			return errors.New("VALIDATION_FAILED")
		}
		seen[gpu.UUID] = true
	}
	if d := o.Deployment; d != nil {
		if d.validateIdentity() != nil || !KnownInferenceError(d.ErrorCode) {
			return errors.New("VALIDATION_FAILED")
		}
		if d.State != "running" && d.State != "loading" && d.State != "stopped" && d.State != "not_installed" && d.State != "unknown" && d.State != "failed" {
			return errors.New("VALIDATION_FAILED")
		}
		if d.ContainerID != "" && !sha256Pattern.MatchString(d.ContainerID) {
			return errors.New("VALIDATION_FAILED")
		}
		recipe := ApprovedInferenceRecipe()
		if d.ImageID != "" && d.ImageID != recipe.ImageManifestDigest && d.ImageID != recipe.ImageConfigDigest {
			return errors.New("VALIDATION_FAILED")
		}
		if d.Managed && (d.State == "running" || d.State == "stopped" || d.State == "loading") && (d.ContainerID == "" || d.ImageID == "") {
			return errors.New("VALIDATION_FAILED")
		}
		if d.State == "not_installed" && (d.Managed || d.ContainerID != "" || d.Ready || d.Verification != nil) {
			return errors.New("VALIDATION_FAILED")
		}
		if d.Verification != nil && d.Verification.Validate() != nil {
			return errors.New("VALIDATION_FAILED")
		}
		if d.Ready && (!d.Managed || d.State != "running" || d.ErrorCode != "" || o.ErrorCode != "" || o.ProbeErrorCode != "" || d.Verification == nil) {
			return errors.New("VALIDATION_FAILED")
		}
	}
	return nil
}
