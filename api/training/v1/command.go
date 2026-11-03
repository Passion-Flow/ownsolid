package v1

import (
	"errors"
	"regexp"
	"strings"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const NodeProfile = "docker_ai"
const NodeProtocolVersion = "1.4"
const RunOperation = "training.run.v1"
const MaximumCommandBytes = 32768
const MaximumAttemptNumber = 1000
const TransferChunkBytes int64 = 8388608

type ExecutionRecipe struct {
	SchemaVersion  int               `json:"schema_version"`
	WorkerVersion  string            `json:"worker_version"`
	ImageReference string            `json:"image_reference"`
	Identity       ExecutionIdentity `json:"execution_identity"`
	Resources      Resources         `json:"resources"`
	DataLimits     datawire.Limits   `json:"data_limits"`
}

func (r ExecutionRecipe) Validate() error {
	if r.SchemaVersion != 1 || r.WorkerVersion != WorkerVersion || r.Identity.Validate(ProductionProfile) != nil || r.Resources.Validate() != nil || r.DataLimits.Validate() != nil || !strings.HasPrefix(r.ImageReference, "projectone/training@sha256:") || !datawire.ValidDigest(strings.TrimPrefix(r.ImageReference, "projectone/training@sha256:")) {
		return errors.New("TRAINING_RECIPE_INVALID")
	}
	return nil
}

type InputInventory struct {
	Train      datawire.Asset `json:"train"`
	Validation datawire.Asset `json:"validation"`
}

func (v InputInventory) Validate(limits datawire.Limits) error {
	if limits.Validate() != nil {
		return errors.New("TRAINING_INPUT_INVALID")
	}
	for _, asset := range []datawire.Asset{v.Train, v.Validation} {
		if asset.SizeBytes < 1 || asset.SizeBytes > limits.MaximumBytes || !datawire.ValidDigest(asset.SHA256) {
			return errors.New("TRAINING_INPUT_INVALID")
		}
	}
	if v.Train.Path != "train.jsonl" || v.Validation.Path != "validation.jsonl" || v.Train.SHA256 == v.Validation.SHA256 {
		return errors.New("TRAINING_INPUT_INVALID")
	}
	return nil
}

type ResumeInput struct {
	CheckpointID  string           `json:"checkpoint_id"`
	GlobalStep    int64            `json:"global_step"`
	ReceiptSHA256 string           `json:"receipt_sha256"`
	Files         []datawire.Asset `json:"files"`
}

var checkpointNames = map[string]bool{"adapter_model.safetensors": true, "adapter_config.json": true, "optimizer.pt": true, "scheduler.pt": true, "trainer_state.json": true, "rng_state.pth": true, "training_args.bin": true, "README.md": true, "projectone-checkpoint.json": true}

func CheckpointAssetAllowed(name string) bool { return checkpointNames[name] }
func AdapterAssetAllowed(name string) bool {
	return name == "adapter_model.safetensors" || name == "adapter_config.json" || name == "README.md"
}

func (r ResumeInput) Validate(configuration Configuration) error {
	if !datawire.ValidID(r.CheckpointID) || !datawire.ValidDigest(r.ReceiptSHA256) || r.GlobalStep < 1 || r.GlobalStep > configuration.MaximumSteps || len(r.Files) < 7 || len(r.Files) > len(checkpointNames) {
		return errors.New("TRAINING_CHECKPOINT_INVALID")
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range r.Files {
		if !checkpointNames[asset.Path] || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > MaximumCheckpointBytes || !datawire.ValidDigest(asset.SHA256) {
			return errors.New("TRAINING_CHECKPOINT_INVALID")
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
		if asset.Path == "projectone-checkpoint.json" && (asset.SHA256 != r.ReceiptSHA256 || asset.SizeBytes > MaximumCheckpointMetadataBytes) {
			return errors.New("TRAINING_CHECKPOINT_INVALID")
		}
	}
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth", "projectone-checkpoint.json"} {
		if !seen[name] {
			return errors.New("TRAINING_CHECKPOINT_INVALID")
		}
	}
	if total > MaximumCheckpointBytes {
		return errors.New("TRAINING_CHECKPOINT_INVALID")
	}
	return nil
}

type Command struct {
	SchemaVersion         int             `json:"schema_version"`
	Scope                 Scope           `json:"scope"`
	AttemptID             string          `json:"attempt_id"`
	AttemptNumber         int             `json:"attempt_number"`
	ModelID               string          `json:"model_id"`
	DatasetID             string          `json:"dataset_id"`
	DatasetVersionID      string          `json:"dataset_version_id"`
	DatasetManifestSHA256 string          `json:"dataset_manifest_sha256"`
	ModelCatalogSHA256    string          `json:"model_catalog_sha256"`
	GPUUUID               string          `json:"gpu_uuid"`
	Configuration         Configuration   `json:"configuration"`
	ConfigurationSHA256   string          `json:"configuration_sha256"`
	Recipe                ExecutionRecipe `json:"recipe"`
	Inputs                InputInventory  `json:"inputs"`
	Resume                *ResumeInput    `json:"resume"`
}

var gpuUUID = regexp.MustCompile(`^GPU-[a-f0-9-]{36}$`)

func (c Command) Validate() error {
	if c.SchemaVersion != 1 || c.Scope.Validate() != nil || !datawire.ValidID(c.AttemptID) || c.AttemptNumber < 1 || c.AttemptNumber > MaximumAttemptNumber || !datawire.ValidID(c.ModelID) || !datawire.ValidID(c.DatasetID) || !datawire.ValidID(c.DatasetVersionID) || !datawire.ValidDigest(c.DatasetManifestSHA256) || !datawire.ValidDigest(c.ModelCatalogSHA256) || !gpuUUID.MatchString(c.GPUUUID) || c.Configuration.Validate() != nil || c.Configuration.Device != ProductionProfile || c.Recipe.Validate() != nil || c.Inputs.Validate(c.Recipe.DataLimits) != nil {
		return errors.New("TRAINING_COMMAND_INVALID")
	}
	digest, _ := c.Configuration.Digest()
	if digest != c.ConfigurationSHA256 || c.Recipe.Resources.CPUSeconds > c.Recipe.Resources.WallSeconds*c.Configuration.Threads {
		return errors.New("TRAINING_COMMAND_INVALID")
	}
	if c.Resume != nil {
		return c.Resume.Validate(c.Configuration)
	}
	return nil
}
func DecodeCommand(raw []byte) (Command, error) {
	var out Command
	if len(raw) > MaximumCommandBytes || DecodeStrict(raw, &out) != nil {
		return out, errors.New("TRAINING_COMMAND_INVALID")
	}
	return out, out.Validate()
}
