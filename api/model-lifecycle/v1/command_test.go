package v1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func validCommand(t *testing.T) Command {
	t.Helper()
	id := "11111111-1111-4111-8111-111111111111"
	hash := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	config := DefaultConfiguration()
	configSHA, e := config.Digest()
	if e != nil {
		t.Fatal(e)
	}
	train := training.DefaultConfiguration()
	trainSHA, e := train.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return Command{SchemaVersion: 1, Scope: Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, AttemptID: id, AttemptNumber: 1, ModelID: id, ModelCatalogSHA256: ApprovedModelCatalogSHA256, TrainingJobID: id, TrainingAttemptID: id, TrainingConfiguration: train, TrainingConfigurationSHA256: trainSHA, HeldoutID: id, HeldoutManifestSHA256: hash, GPUUUID: "GPU-" + id, Configuration: config, ConfigurationSHA256: configSHA,
		Recipe: ExecutionRecipe{SchemaVersion: 1, WorkerVersion: WorkerVersion, ImageReference: ImageRepository + "@sha256:" + hash, Identity: training.ExecutionIdentity{Profile: training.ProductionProfile, Python: training.PythonVersion, WorkerSourceSHA256: hash, ImageID: &image}, Resources: DefaultResources(), DataLimits: dataset.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}, RecipeSHA256: RecipeDigest(), ProcessorSHA256: ProcessorDigest()},
		Inputs: InputInventory{AdapterFiles: []dataset.Asset{{Path: "README.md", SizeBytes: 248, SHA256: hash}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: hash}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: hash}}, Heldout: dataset.Asset{Path: "heldout.jsonl", SizeBytes: 512, SHA256: hash}}}
}
func TestPreparationCommandBindsRecipeProductionInputsAndExactContainerABI(t *testing.T) {
	command := validCommand(t)
	if e := command.Validate(); e != nil {
		t.Fatal(e)
	}
	raw, e := command.RequestBytes()
	if e != nil {
		t.Fatal(e)
	}
	request, e := DecodeProcessorRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	scope := ContainerScopesDirectory + "/" + command.Scope.TenantID + "/" + command.Scope.WorkspaceID + "/" + command.Scope.PreparationID + "/" + command.AttemptID
	if request.ScopeDirectory != scope || request.ModelDirectory != ContainerModelDirectory || request.OutputDirectory != scope+"/output" || request.AdapterDirectory != scope+"/inputs/adapter" || request.HeldoutFile != scope+"/inputs/heldout.jsonl" {
		t.Fatal("incorrect image filesystem ABI")
	}
	request.AdapterFiles[0].SHA256 = strings.Repeat("c", 64)
	*request.Identity.ImageID = "changed"
	if command.Inputs.AdapterFiles[0].SHA256 != strings.Repeat("a", 64) || *command.Recipe.Identity.ImageID == "changed" {
		t.Fatal("request mutated frozen command")
	}
	encoded, _ := json.Marshal(command)
	decoded, e := DecodeCommand(encoded)
	if e != nil || !reflect.DeepEqual(command, decoded) {
		t.Fatal("exact command roundtrip", e)
	}
}
func TestPreparationCommandRejectsImplicitProfileCPUStaleRecipeAndForeignInput(t *testing.T) {
	changes := map[string]func(*Command){
		"cpu_prepare": func(c *Command) {
			c.Configuration.Device = training.VerificationProfile
			c.ConfigurationSHA256, _ = c.Configuration.Digest()
		},
		"cpu_training": func(c *Command) {
			c.TrainingConfiguration.Device = training.VerificationProfile
			c.TrainingConfigurationSHA256, _ = c.TrainingConfiguration.Digest()
		},
		"old_worker":        func(c *Command) { c.Recipe.WorkerVersion = training.WorkerVersion },
		"stale_recipe":      func(c *Command) { c.Recipe.RecipeSHA256 = strings.Repeat("0", 64) },
		"stale_processor":   func(c *Command) { c.Recipe.ProcessorSHA256 = strings.Repeat("0", 64) },
		"mutable_image":     func(c *Command) { c.Recipe.ImageReference = ImageRepository + ":latest" },
		"foreign_catalog":   func(c *Command) { c.ModelCatalogSHA256 = strings.Repeat("0", 64) },
		"bad_gpu":           func(c *Command) { c.GPUUUID = "GPU-" + strings.Repeat("-", 36) },
		"duplicate_adapter": func(c *Command) { c.Inputs.AdapterFiles[1] = c.Inputs.AdapterFiles[0] },
		"optimizer":         func(c *Command) { c.Inputs.AdapterFiles[0].Path = "optimizer.pt" },
		"bad_hash":          func(c *Command) { c.ConfigurationSHA256 = strings.Repeat("0", 64) },
		"too_many_rows":     func(c *Command) { c.Recipe.DataLimits.MaximumRows = MaximumRows + 1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			command := validCommand(t)
			change(&command)
			if command.Validate() == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}
func TestNativeProcessorRequestCannotBePromotedToProductionCommand(t *testing.T) {
	command := validCommand(t)
	request, e := command.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	request.Configuration.Device = training.VerificationProfile
	request.Identity.Profile = "native_cpu_verification"
	request.Identity.ImageID = nil
	raw, _ := json.Marshal(request)
	if _, e := DecodeProcessorRequest(raw); e != nil {
		t.Fatal("explicit native verification", e)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	var identity map[string]json.RawMessage
	json.Unmarshal(fields["execution_identity"], &identity)
	identity["image_id"] = json.RawMessage("null")
	fields["execution_identity"], _ = json.Marshal(identity)
	raw, _ = json.Marshal(fields)
	if _, e := DecodeProcessorRequest(raw); e == nil {
		t.Fatal("native identity accepted undeclared null image")
	}
	command.Configuration = request.Configuration
	command.ConfigurationSHA256, _ = command.Configuration.Digest()
	if command.Validate() == nil {
		t.Fatal("native verification promoted to node production authority")
	}
}
