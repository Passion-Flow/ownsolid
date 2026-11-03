package inference

import (
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"reflect"
	"strings"
	"testing"
	"time"
)

func derivedCommandFixture(t *testing.T) wire.InferenceCommand {
	t.Helper()
	// Catalog/model identities and the128-byte tensor descriptor are explicit contract-test data.
	// This fixture registers/accepts no model and makes no file, GPU or serving claim.
	id := "12345678-1234-1234-1234-123456789abc"
	model := "22345678-1234-1234-1234-123456789abc"
	catalog := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: wire.DerivedModelKind, Scope: lifecycle.Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, ModelID: model, BaseModelID: id, BaseCatalogSHA256: wire.ModelCatalogDigest(), AttemptID: id, ArtifactID: model, ReportSHA256: strings.Repeat("a", 64), RecipeSHA256: lifecycle.RecipeDigest(), BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: 2048, Files: lifecycle.ApprovedBaseFiles()}
	for index, file := range catalog.Files {
		if file.Path == "model.safetensors" {
			catalog.Files[index].SizeBytes = 128
			catalog.Files[index].SHA256 = strings.Repeat("b", 64)
		}
	}
	digest, e := catalog.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return wire.InferenceCommand{Action: "deploy", DeploymentID: id, ModelID: model, CatalogDigest: digest, GPUUUID: "GPU-" + id, Revision: 1, Derived: &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: wire.DerivedServingRecipeDigest()}}
}

func TestDerivedContainerAndGPURequireExactBF16Identity(t *testing.T) {
	c := derivedCommandFixture(t)
	node := c.DeploymentID
	root := "/opt/projectone-inference-test/runtime"
	network := strings.Repeat("c", 64)
	spec, e := DerivedContainerConfiguration(c, node, root+"/models/"+c.CatalogDigest, root+"/deployments/"+c.DeploymentID+"/cache", network)
	if e != nil {
		t.Fatal(e)
	}
	args := strings.Join(spec.Cmd, " ")
	if !strings.Contains(args, "--dtype bfloat16") || !strings.Contains(args, `--no-trust-remote-code --default-chat-template-kwargs {"enable_thinking":false}`) || spec.Labels["projectone.model_digest"] != c.CatalogDigest || spec.Labels["projectone.serving_recipe_sha256"] != c.Derived.ServingRecipeSHA256 {
		t.Fatal("derived recipe not exact")
	}
	if _, e = DerivedContainerConfiguration(c, node, root+"/models/"+wire.ModelCatalogDigest(), root+"/deployments/"+c.DeploymentID+"/cache", network); e == nil {
		t.Fatal("base path accepted")
	}
	gpu := wire.NVIDIAGPU{UUID: c.GPUUUID, Name: "Explicit synthetic test GPU", DriverVersion: "580.65.06", MemoryTotalMiB: 8192, MemoryFreeMiB: 8192, ComputeCapability: 7.5, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}
	if e = ValidateCommandGPU([]wire.NVIDIAGPU{gpu}, nil, c); e == nil || e.Error() != "INFERENCE_GPU_UNSUPPORTED" {
		t.Fatal("BF16 admitted SM7.5", e)
	}
	gpu.ComputeCapability = 8
	gpu.MemoryFreeMiB = 8191
	if e = ValidateCommandGPU([]wire.NVIDIAGPU{gpu}, nil, c); e == nil || e.Error() != "INFERENCE_GPU_MEMORY_LOW" {
		t.Fatal("BF16 free memory gate", e)
	}
	gpu.MemoryFreeMiB = 8192
	if e = ValidateCommandGPU([]wire.NVIDIAGPU{gpu}, nil, c); e != nil {
		t.Fatal(e)
	}
	altered := c
	altered.Derived = nil
	altered.CatalogDigest = wire.ModelCatalogDigest()
	if sameCommandIdentity(c, altered) {
		t.Fatal("base/derived identity aliased")
	}
	j := journal{NodeID: node, Command: c}
	m := Manager{config: Config{RootDirectory: root}}
	j.NetworkID = network
	reconstructed, e := m.commandContainerConfiguration(j)
	if e != nil || !reflect.DeepEqual(spec, reconstructed) {
		t.Fatal("ownership recipe not reconstructed", e)
	}
}
