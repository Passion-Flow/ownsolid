package v1

import (
	"encoding/json"
	"errors"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type Bindings struct {
	ConfigurationSHA256 string            `json:"configuration_sha256"`
	Configuration       Configuration     `json:"configuration"`
	ExecutionIdentity   ExecutionIdentity `json:"execution_identity"`
	Scope               Scope             `json:"scope"`
	ModelRepository     string            `json:"model_repository"`
	ModelCatalogSHA256  string            `json:"model_catalog_sha256"`
	TrainSHA256         string            `json:"train_sha256"`
	ValidationSHA256    string            `json:"validation_sha256"`
	RecipeVersion       int               `json:"recipe_version"`
	Packages            Packages          `json:"packages"`
}

func ExpectedBindings(r ProcessorRequest, repository string) Bindings {
	digest, _ := r.Configuration.Digest()
	return Bindings{ConfigurationSHA256: digest, Configuration: r.Configuration, ExecutionIdentity: r.ExecutionIdentity, Scope: Scope{r.TenantID, r.WorkspaceID, r.JobID}, ModelRepository: repository, ModelCatalogSHA256: r.ModelCatalogSHA256, TrainSHA256: r.TrainSHA256, ValidationSHA256: r.ValidationSHA256, RecipeVersion: 1, Packages: PinnedPackages()}
}
func (b Bindings) ValidateFor(r ProcessorRequest, repository string) error {
	if r.Validate() != nil || repository == "" {
		return errors.New("TRAINING_BINDINGS_INVALID")
	}
	expected, _ := json.Marshal(ExpectedBindings(r, repository))
	actual, _ := json.Marshal(b)
	if string(expected) != string(actual) {
		return errors.New("TRAINING_BINDINGS_INVALID")
	}
	return nil
}

type Checkpoint struct {
	GlobalStep    int64  `json:"global_step"`
	ReceiptSHA256 string `json:"receipt_sha256"`
	Path          string `json:"path"`
}
type CheckpointReceipt struct {
	SchemaVersion int              `json:"schema_version"`
	Bindings      Bindings         `json:"bindings"`
	GlobalStep    int64            `json:"global_step"`
	Files         []datawire.Asset `json:"files"`
}

func (c CheckpointReceipt) ValidateFor(r ProcessorRequest, repository string) error {
	if c.SchemaVersion != 1 || c.Bindings.ValidateFor(r, repository) != nil || c.GlobalStep < 1 || c.GlobalStep > r.Configuration.MaximumSteps || len(c.Files) < 6 || len(c.Files) > len(checkpointNames)-1 {
		return errors.New("TRAINING_CHECKPOINT_INVALID")
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range c.Files {
		if !checkpointNames[asset.Path] || asset.Path == "projectone-checkpoint.json" || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > MaximumCheckpointBytes || !datawire.ValidDigest(asset.SHA256) {
			return errors.New("TRAINING_CHECKPOINT_INVALID")
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
	}
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth"} {
		if !seen[name] {
			return errors.New("TRAINING_CHECKPOINT_INVALID")
		}
	}
	if total > MaximumCheckpointBytes || total > r.Resources.MaximumOutputBytes {
		return errors.New("TRAINING_CHECKPOINT_INVALID")
	}
	return nil
}
func DecodeCheckpoint(raw []byte, r ProcessorRequest, repository string) (CheckpointReceipt, error) {
	var receipt CheckpointReceipt
	if len(raw) > MaximumCheckpointMetadataBytes || DecodeStrict(raw, &receipt) != nil {
		return receipt, errors.New("TRAINING_CHECKPOINT_INVALID")
	}
	return receipt, receipt.ValidateFor(r, repository)
}
