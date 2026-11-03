package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"reflect"
	"strings"
	"unicode/utf8"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type Scope struct {
	TenantID      string `json:"tenant_id"`
	WorkspaceID   string `json:"workspace_id"`
	PreparationID string `json:"preparation_id"`
}

func (s Scope) Validate() error {
	for _, value := range []string{s.TenantID, s.WorkspaceID, s.PreparationID} {
		if !dataset.ValidID(value) {
			return errors.New("MODEL_PREPARATION_SCOPE_INVALID")
		}
	}
	return nil
}

type ExecutionRecipe struct {
	SchemaVersion   int                        `json:"schema_version"`
	WorkerVersion   string                     `json:"worker_version"`
	ImageReference  string                     `json:"image_reference"`
	Identity        training.ExecutionIdentity `json:"execution_identity"`
	Resources       Resources                  `json:"resources"`
	DataLimits      dataset.Limits             `json:"data_limits"`
	RecipeSHA256    string                     `json:"recipe_sha256"`
	ProcessorSHA256 string                     `json:"processor_sha256"`
}

func (r ExecutionRecipe) Validate() error {
	prefix := ImageRepository + "@sha256:"
	if r.SchemaVersion != 1 || r.WorkerVersion != WorkerVersion || r.Identity.Validate(training.ProductionProfile) != nil || r.Resources.Validate() != nil || r.DataLimits.Validate() != nil || r.DataLimits.MaximumRows > MaximumRows || r.RecipeSHA256 != RecipeDigest() || r.ProcessorSHA256 != ProcessorDigest() || !strings.HasPrefix(r.ImageReference, prefix) || !dataset.ValidDigest(strings.TrimPrefix(r.ImageReference, prefix)) {
		return errors.New("MODEL_PREPARATION_RECIPE_INVALID")
	}
	return nil
}

type InputInventory struct {
	AdapterFiles []dataset.Asset `json:"adapter_files"`
	Heldout      dataset.Asset   `json:"heldout"`
}

func validateAdapterFiles(files []dataset.Asset) error {
	if len(files) != 3 {
		return errors.New("MODEL_PREPARATION_INPUT_INVALID")
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range files {
		if !training.AdapterAssetAllowed(asset.Path) || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > MaximumAdapterBytes || !dataset.ValidDigest(asset.SHA256) {
			return errors.New("MODEL_PREPARATION_INPUT_INVALID")
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
	}
	if total > MaximumAdapterBytes {
		return errors.New("MODEL_PREPARATION_INPUT_INVALID")
	}
	return nil
}
func (i InputInventory) Validate(limits dataset.Limits) error {
	if limits.Validate() != nil || i.Heldout.Path != "heldout.jsonl" || i.Heldout.SizeBytes < 1 || i.Heldout.SizeBytes > limits.MaximumBytes || !dataset.ValidDigest(i.Heldout.SHA256) {
		return errors.New("MODEL_PREPARATION_INPUT_INVALID")
	}
	return validateAdapterFiles(i.AdapterFiles)
}

type Command struct {
	SchemaVersion               int                    `json:"schema_version"`
	Scope                       Scope                  `json:"scope"`
	AttemptID                   string                 `json:"attempt_id"`
	AttemptNumber               int                    `json:"attempt_number"`
	ModelID                     string                 `json:"model_id"`
	ModelCatalogSHA256          string                 `json:"model_catalog_sha256"`
	TrainingJobID               string                 `json:"training_job_id"`
	TrainingAttemptID           string                 `json:"training_attempt_id"`
	TrainingConfiguration       training.Configuration `json:"training_configuration"`
	TrainingConfigurationSHA256 string                 `json:"training_configuration_sha256"`
	HeldoutID                   string                 `json:"heldout_id"`
	HeldoutManifestSHA256       string                 `json:"heldout_manifest_sha256"`
	GPUUUID                     string                 `json:"gpu_uuid"`
	Configuration               Configuration          `json:"configuration"`
	ConfigurationSHA256         string                 `json:"configuration_sha256"`
	Recipe                      ExecutionRecipe        `json:"recipe"`
	Inputs                      InputInventory         `json:"inputs"`
}

func (c Command) Validate() error {
	if c.SchemaVersion != 1 || c.Scope.Validate() != nil || c.AttemptNumber < 1 || c.AttemptNumber > MaximumAttemptNumber || c.ModelCatalogSHA256 != ApprovedModelCatalogSHA256 || c.TrainingConfiguration.Validate() != nil || c.TrainingConfiguration.Device != training.ProductionProfile || c.Configuration.Validate() != nil || c.Configuration.Device != training.ProductionProfile || c.Recipe.Validate() != nil || c.Inputs.Validate(c.Recipe.DataLimits) != nil || !dataset.ValidDigest(c.HeldoutManifestSHA256) || !strings.HasPrefix(c.GPUUUID, "GPU-") || !dataset.ValidID(strings.TrimPrefix(c.GPUUUID, "GPU-")) {
		return errors.New("MODEL_PREPARATION_COMMAND_INVALID")
	}
	for _, value := range []string{c.AttemptID, c.ModelID, c.TrainingJobID, c.TrainingAttemptID, c.HeldoutID} {
		if !dataset.ValidID(value) {
			return errors.New("MODEL_PREPARATION_COMMAND_INVALID")
		}
	}
	config, e := c.Configuration.Digest()
	if e != nil || config != c.ConfigurationSHA256 {
		return errors.New("MODEL_PREPARATION_COMMAND_INVALID")
	}
	train, e := c.TrainingConfiguration.Digest()
	if e != nil || train != c.TrainingConfigurationSHA256 || c.Recipe.Resources.CPUSeconds > c.Recipe.Resources.WallSeconds*c.Configuration.Threads {
		return errors.New("MODEL_PREPARATION_COMMAND_INVALID")
	}
	return nil
}
func DecodeCommand(raw []byte) (Command, error) {
	var out Command
	if len(raw) > MaximumCommandBytes || DecodeStrict(raw, &out) != nil {
		return out, errors.New("MODEL_PREPARATION_COMMAND_INVALID")
	}
	return out, out.Validate()
}

// Digest binds the exact typed node command independently of its translated protected worker request.
func (c Command) Digest() (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type ProcessorRequest struct {
	SchemaVersion         int                        `json:"schema_version"`
	Operation             string                     `json:"operation"`
	TenantID              string                     `json:"tenant_id"`
	WorkspaceID           string                     `json:"workspace_id"`
	PreparationID         string                     `json:"preparation_id"`
	AttemptID             string                     `json:"attempt_id"`
	ScopeDirectory        string                     `json:"scope_directory"`
	ModelDirectory        string                     `json:"model_directory"`
	ModelCatalogSHA256    string                     `json:"model_catalog_sha256"`
	AdapterDirectory      string                     `json:"adapter_directory"`
	AdapterFiles          []dataset.Asset            `json:"adapter_files"`
	TrainingConfiguration training.Configuration     `json:"training_configuration"`
	TrainingJobID         string                     `json:"training_job_id"`
	TrainingAttemptID     string                     `json:"training_attempt_id"`
	HeldoutID             string                     `json:"heldout_id"`
	HeldoutFile           string                     `json:"heldout_file"`
	HeldoutSHA256         string                     `json:"heldout_sha256"`
	Configuration         Configuration              `json:"configuration"`
	DataLimits            dataset.Limits             `json:"data_limits"`
	Resources             Resources                  `json:"resources"`
	Identity              training.ExecutionIdentity `json:"execution_identity"`
	OutputDirectory       string                     `json:"output_directory"`
}

func (r ProcessorRequest) Validate() error {
	if r.SchemaVersion != 1 || r.Operation != RunOperation || r.ModelCatalogSHA256 != ApprovedModelCatalogSHA256 || r.Configuration.Validate() != nil || r.TrainingConfiguration.Validate() != nil || r.DataLimits.Validate() != nil || r.DataLimits.MaximumRows > MaximumRows || r.Resources.Validate() != nil || r.Identity.Validate(r.Configuration.Device) != nil || !dataset.ValidDigest(r.HeldoutSHA256) {
		return errors.New("MODEL_PREPARATION_REQUEST_INVALID")
	}
	for _, value := range []string{r.TenantID, r.WorkspaceID, r.PreparationID, r.AttemptID, r.TrainingJobID, r.TrainingAttemptID, r.HeldoutID} {
		if !dataset.ValidID(value) {
			return errors.New("MODEL_PREPARATION_SCOPE_INVALID")
		}
	}
	tail := path.Join(r.TenantID, r.WorkspaceID, r.PreparationID, r.AttemptID)
	if !path.IsAbs(r.ScopeDirectory) || path.Clean(r.ScopeDirectory) != r.ScopeDirectory || !strings.HasSuffix(r.ScopeDirectory, "/"+tail) || !path.IsAbs(r.ModelDirectory) || path.Clean(r.ModelDirectory) != r.ModelDirectory || r.ModelDirectory == r.ScopeDirectory || strings.HasPrefix(r.ModelDirectory, r.ScopeDirectory+"/") || strings.HasPrefix(r.ScopeDirectory, r.ModelDirectory+"/") || r.AdapterDirectory != path.Join(r.ScopeDirectory, "inputs/adapter") || r.HeldoutFile != path.Join(r.ScopeDirectory, "inputs/heldout.jsonl") || r.OutputDirectory != path.Join(r.ScopeDirectory, "output") {
		return errors.New("MODEL_PREPARATION_PATH_INVALID")
	}
	for _, value := range []string{r.ScopeDirectory, r.ModelDirectory, r.AdapterDirectory, r.HeldoutFile, r.OutputDirectory} {
		if len(value) > MaximumPathBytes || !utf8.ValidString(value) || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return errors.New("MODEL_PREPARATION_PATH_INVALID")
		}
	}
	if r.Configuration.Device == training.ProductionProfile && (!strings.HasPrefix(r.ScopeDirectory, ContainerScopesDirectory+"/") || r.ModelDirectory != ContainerModelDirectory) {
		return errors.New("MODEL_PREPARATION_PATH_INVALID")
	}
	if validateAdapterFiles(r.AdapterFiles) != nil || r.Resources.CPUSeconds > r.Resources.WallSeconds*r.Configuration.Threads || r.Configuration.Device == training.ProductionProfile && r.TrainingConfiguration.Device != training.ProductionProfile {
		return errors.New("MODEL_PREPARATION_REQUEST_INVALID")
	}
	return nil
}
func (c Command) ContainerRequest() (ProcessorRequest, error) {
	if e := c.Validate(); e != nil {
		return ProcessorRequest{}, e
	}
	scope := path.Join(ContainerScopesDirectory, c.Scope.TenantID, c.Scope.WorkspaceID, c.Scope.PreparationID, c.AttemptID)
	out := ProcessorRequest{SchemaVersion: 1, Operation: RunOperation, TenantID: c.Scope.TenantID, WorkspaceID: c.Scope.WorkspaceID, PreparationID: c.Scope.PreparationID, AttemptID: c.AttemptID, ScopeDirectory: scope, ModelDirectory: ContainerModelDirectory, ModelCatalogSHA256: c.ModelCatalogSHA256, AdapterDirectory: path.Join(scope, "inputs/adapter"), AdapterFiles: append([]dataset.Asset(nil), c.Inputs.AdapterFiles...), TrainingConfiguration: c.TrainingConfiguration, TrainingJobID: c.TrainingJobID, TrainingAttemptID: c.TrainingAttemptID, HeldoutID: c.HeldoutID, HeldoutFile: path.Join(scope, "inputs/heldout.jsonl"), HeldoutSHA256: c.Inputs.Heldout.SHA256, Configuration: c.Configuration, DataLimits: c.Recipe.DataLimits, Resources: c.Recipe.Resources, Identity: c.Recipe.Identity, OutputDirectory: path.Join(scope, "output")}
	if out.Identity.ImageID != nil {
		image := *out.Identity.ImageID
		out.Identity.ImageID = &image
	}
	return out, out.Validate()
}
func (c Command) RequestBytes() ([]byte, error) {
	request, e := c.ContainerRequest()
	if e != nil {
		return nil, e
	}
	return json.Marshal(request)
}
func SameCommand(a, b Command) bool { return reflect.DeepEqual(a, b) }

func DecodeProcessorRequest(raw []byte) (ProcessorRequest, error) {
	var out ProcessorRequest
	if len(raw) > MaximumRequestBytes || DecodeStrict(raw, &out) != nil {
		return out, errors.New("MODEL_PREPARATION_REQUEST_INVALID")
	}
	if out.Configuration.Device == training.VerificationProfile {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return out, errors.New("MODEL_PREPARATION_REQUEST_INVALID")
		}
		if _, e := training.ExactObject(fields["execution_identity"], []string{"profile", "python", "worker_source_sha256"}); e != nil {
			return out, errors.New("MODEL_PREPARATION_EXECUTION_IDENTITY_REQUIRED")
		}
	}
	return out, out.Validate()
}
