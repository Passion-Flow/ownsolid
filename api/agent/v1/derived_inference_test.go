package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func derivedInferenceFixture(t *testing.T) InferenceCommand {
	t.Helper()
	// Catalog/model identities and the128-byte tensor descriptor are explicit contract-test data.
	// This fixture registers/accepts no model and makes no file, GPU or serving claim.
	id := "12345678-1234-1234-1234-123456789abc"
	model := "22345678-1234-1234-1234-123456789abc"
	catalog := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: DerivedModelKind, Scope: lifecycle.Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, ModelID: model, BaseModelID: id, BaseCatalogSHA256: ModelCatalogDigest(), AttemptID: id, ArtifactID: model, ReportSHA256: strings.Repeat("a", 64), RecipeSHA256: lifecycle.RecipeDigest(), BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: 2048, Files: lifecycle.ApprovedBaseFiles()}
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
	return InferenceCommand{Action: "deploy", DeploymentID: id, ModelID: model, CatalogDigest: digest, GPUUUID: "GPU-" + id, Revision: 1, Derived: &DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: DerivedServingRecipeDigest()}}
}
func TestDerivedInferenceRecipeCanonicalPinAndLegacyRecipeUnchanged(t *testing.T) {
	derived := ApprovedDerivedServingRecipe()
	base := ApprovedInferenceRecipe()
	if derived.Dtype != "bfloat16" || derived.MinimumComputeCapability != 8 || base.MinimumComputeCapability != 7.5 || derived.TrustRemoteCode || derived.EnableThinking {
		t.Fatal("precision or hardware profile drift")
	}
	raw, e := os.ReadFile("derived-inference-recipe.json")
	if e != nil {
		t.Fatal(e)
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		t.Fatal("compiled recipe")
	}
	raw, e = json.Marshal(object)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if DerivedServingRecipeDigest() != hex.EncodeToString(sum[:]) {
		t.Fatal("Go and canonical sorted JSON recipe digests differ")
	}
	if derived.ImageConfigDigest != base.ImageConfigDigest || derived.ImageManifestDigest != base.ImageManifestDigest || derived.ModelKey != base.ModelKey {
		t.Fatal("derived recipe changed pinned engine/base architecture")
	}
}
func TestDerivedInferenceExplicitOperationScopeAndLegacySeparation(t *testing.T) {
	c := derivedInferenceFixture(t)
	if !IsInferenceOperation(c.Operation()) || c.Operation() != DerivedInferenceOperation || c.Validate(c.Operation()) != nil {
		t.Fatal("explicit derived command")
	}
	for _, action := range []string{"deploy", "observe", "verify", "stop", "remove"} {
		current := c
		current.Action = action
		if current.Validate(DerivedInferenceOperation) != nil || current.Validate(InferenceOperation(action)) == nil {
			t.Fatal("derived action crossed legacy operation", action)
		}
	}
	for _, profile := range []string{ReadOnlyProfile, SystemdComponentsProfile, SystemdMaintenanceProfile, DockerInferenceProfile, DockerAIProfile} {
		if OperationAllowed(profile, DerivedInferenceOperation) {
			t.Fatal("old profile gained derived authority", profile)
		}
	}
	if !OperationAllowed(DockerModelLifecycleProfile, DerivedInferenceOperation) {
		t.Fatal("explicit lifecycle capability missing")
	}
	changes := map[string]func(*InferenceCommand){
		"catalog_sha":     func(c *InferenceCommand) { c.CatalogDigest = ModelCatalogDigest() },
		"model":           func(c *InferenceCommand) { c.ModelID = c.DeploymentID },
		"scope":           func(c *InferenceCommand) { c.Derived.Catalog.Scope.WorkspaceID = "" },
		"recipe":          func(c *InferenceCommand) { c.Derived.ServingRecipeSHA256 = strings.Repeat("c", 64) },
		"schema":          func(c *InferenceCommand) { c.Derived.SchemaVersion = 2 },
		"unbound_observe": func(c *InferenceCommand) { c.Action = "observe"; c.DeploymentID = "" },
		"revision":        func(c *InferenceCommand) { c.Revision = 9007199254740992 },
		"gpu":             func(c *InferenceCommand) { c.GPUUUID = "all" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(c)
			var bad InferenceCommand
			if lifecycle.DecodeStrict(raw, &bad) != nil {
				t.Fatal("fixture")
			}
			change(&bad)
			if bad.Validate(DerivedInferenceOperation) == nil {
				t.Fatal("changed derived binding accepted")
			}
		})
	}
	legacy := c
	legacy.Derived = nil
	legacy.CatalogDigest = ModelCatalogDigest()
	if legacy.Validate(InferenceOperation("deploy")) != nil || legacy.Validate(DerivedInferenceOperation) == nil {
		t.Fatal("base command inherited derived operation")
	}
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "derived") {
		t.Fatal("legacy wire gained fields")
	}
	raw, _ = json.Marshal(c)
	raw = append([]byte(`{"unknown":true,`), raw[1:]...)
	var bad InferenceCommand
	if lifecycle.DecodeStrict(raw, &bad) == nil {
		t.Fatal("unknown command field accepted")
	}
	raw, _ = json.Marshal(legacy)
	raw = append([]byte(`{"derived":null,`), raw[1:]...)
	if json.Unmarshal(raw, &bad) == nil {
		t.Fatal("explicit null legacy variant accepted")
	}
	raw, _ = json.Marshal(c)
	raw = append([]byte(`{"derived":null,`), raw[1:]...)
	if lifecycle.DecodeStrict(raw, &bad) == nil {
		t.Fatal("duplicate/null variant accepted")
	}
}
func TestDerivedInferenceRangesUseExactCandidateBytesAndCopies(t *testing.T) {
	c := derivedInferenceFixture(t)
	file, ok := c.ModelFile("model.safetensors")
	if !ok || file.SizeBytes != 128 {
		t.Fatal("candidate bytes substituted")
	}
	good := ModelFileRequest{File: file.Path, Length: file.SizeBytes}
	if good.ValidateForCommand(c) != nil || good.Validate() == nil {
		t.Fatal("derived range used base-only validator")
	}
	for _, bad := range []ModelFileRequest{{File: "../model.safetensors", Length: 128}, {File: file.Path, Offset: 1, Length: 127}, {File: file.Path, Length: 129}, {File: "arbitrary.bin", Length: 1}, {File: file.Path, Offset: 128, Length: 1}} {
		if bad.ValidateForCommand(c) == nil {
			t.Fatal("invalid derived range accepted")
		}
	}
	files := c.ModelFiles()
	files[0].SHA256 = "mutated"
	if c.Derived.Catalog.Files[0].SHA256 == "mutated" {
		t.Fatal("asset helper exposed catalog mutation")
	}
	var total int64
	for _, file := range c.ModelFiles() {
		total += file.SizeBytes
	}
	if total != c.ModelBytes() {
		t.Fatal("bounded byte accounting")
	}
	c.Action = "stop"
	if good.ValidateForCommand(c) == nil {
		t.Fatal("stop command downloaded weights")
	}
}
func TestDerivedInferenceResultBindsKindRecipeAndExactCommand(t *testing.T) {
	c := derivedInferenceFixture(t)
	now := time.Now().UTC()
	d := DeploymentObservation{ModelKind: DerivedModelKind, ServingRecipeSHA256: DerivedServingRecipeDigest(), DeploymentID: c.DeploymentID, ModelID: c.ModelID, CatalogDigest: c.CatalogDigest, GPUUUID: c.GPUUUID, Revision: c.Revision, Managed: true, State: "running", Ready: true, ContainerID: strings.Repeat("c", 64), ImageID: ApprovedDerivedServingRecipe().ImageConfigDigest, Verification: &GenerationVerification{Model: InferenceModelName, PromptSHA256: InferencePromptDigest(), ResponseSHA256: strings.Repeat("d", 64), InputTokens: 12, OutputTokens: 1, ObservedAt: now}}
	out := Result{Outcome: "success", Inference: &InferenceObservation{GPUs: []NVIDIAGPU{}, Deployment: &d, ObservedAt: now}}
	if e := out.ValidateInference(c); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*DeploymentObservation){func(d *DeploymentObservation) { d.ModelKind = "" }, func(d *DeploymentObservation) { d.ServingRecipeSHA256 = "" }, func(d *DeploymentObservation) { d.ServingRecipeSHA256 = strings.Repeat("a", 64) }, func(d *DeploymentObservation) { d.CatalogDigest = ModelCatalogDigest() }, func(d *DeploymentObservation) { d.Verification = nil }, func(d *DeploymentObservation) { d.Revision++ }} {
		raw, _ := json.Marshal(out)
		var bad Result
		if json.Unmarshal(raw, &bad) != nil {
			t.Fatal("fixture")
		}
		change(bad.Inference.Deployment)
		if bad.ValidateInference(c) == nil {
			t.Fatal("unbound derived readiness accepted")
		}
	}
	c.Action = "stop"
	d.Ready = false
	d.State = "stopped"
	out.Inference.ProbeErrorCode = "INFERENCE_NVIDIA_UNAVAILABLE"
	if e := out.ValidateInference(c); e != nil {
		t.Fatal("positive owned stop required unrelated healthy GPU probe", e)
	}
}
