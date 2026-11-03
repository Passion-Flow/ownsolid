package runtime

import (
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"strings"
)

func runtimePreparationLease() agentwire.Lease {
	const id = "00000000-0000-4000-8000-000000000001"
	hash := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	cfg := wire.DefaultConfiguration()
	cfgSHA, _ := cfg.Digest()
	train := trainingwire.DefaultConfiguration()
	trainSHA, _ := train.Digest()
	c := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, AttemptID: id, AttemptNumber: 1, ModelID: id, ModelCatalogSHA256: wire.ApprovedModelCatalogSHA256, TrainingJobID: id, TrainingAttemptID: id, TrainingConfiguration: train, TrainingConfigurationSHA256: trainSHA, HeldoutID: id, HeldoutManifestSHA256: hash, GPUUUID: "GPU-" + id, Configuration: cfg, ConfigurationSHA256: cfgSHA, Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: wire.ImageRepository + "@sha256:" + hash, Identity: trainingwire.ExecutionIdentity{Profile: trainingwire.ProductionProfile, Python: trainingwire.PythonVersion, WorkerSourceSHA256: hash, ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}, RecipeSHA256: wire.RecipeDigest(), ProcessorSHA256: wire.ProcessorDigest()}, Inputs: wire.InputInventory{AdapterFiles: []datawire.Asset{{Path: "README.md", SizeBytes: 248, SHA256: hash}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: hash}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: hash}}, Heldout: datawire.Asset{Path: "heldout.jsonl", SizeBytes: 512, SHA256: hash}}}
	return agentwire.Lease{TaskID: id, LeaseToken: strings.Repeat("A", 43), Attempt: 1, Operation: wire.RunOperation, Preparation: &c}
}
