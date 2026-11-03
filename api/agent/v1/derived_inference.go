package v1

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

//go:embed derived-inference-recipe.json
var derivedInferenceRecipe []byte

const DerivedModelKind = "merged_lora"

type DerivedServingRecipe struct {
	InferenceRecipe
	ModelKind       string `json:"model_kind"`
	Dtype           string `json:"dtype"`
	TrustRemoteCode bool   `json:"trust_remote_code"`
	EnableThinking  bool   `json:"enable_thinking"`
}

func ApprovedDerivedServingRecipe() DerivedServingRecipe {
	var value DerivedServingRecipe
	// This is a compiled source asset, matching the existing approved base recipe loader.
	// External commands carry only its exact digest and use strict object decoding.
	if json.Unmarshal(derivedInferenceRecipe, &value) != nil || value.ModelKind != DerivedModelKind || value.Dtype != "bfloat16" || value.TrustRemoteCode || value.EnableThinking || value.MinimumComputeCapability != 8 {
		panic("COMPILED_DERIVED_SERVING_RECIPE_INVALID")
	}
	common := value.InferenceRecipe
	common.MinimumComputeCapability = ApprovedInferenceRecipe().MinimumComputeCapability
	if !reflect.DeepEqual(common, ApprovedInferenceRecipe()) {
		panic("COMPILED_DERIVED_SERVING_RECIPE_INVALID")
	}
	return value
}

// DerivedServingRecipeDigest uses recursively sorted compact JSON, preserving JSON integer tokens.
func DerivedServingRecipeDigest() string {
	raw, e := json.Marshal(ApprovedDerivedServingRecipe())
	if e != nil {
		panic("COMPILED_DERIVED_SERVING_RECIPE_INVALID")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		panic("COMPILED_DERIVED_SERVING_RECIPE_INVALID")
	}
	raw, e = json.Marshal(value)
	if e != nil {
		panic("COMPILED_DERIVED_SERVING_RECIPE_INVALID")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type DerivedInferenceIdentity struct {
	SchemaVersion       int                      `json:"schema_version"`
	Catalog             lifecycle.DerivedCatalog `json:"catalog"`
	ServingRecipeSHA256 string                   `json:"serving_recipe_sha256"`
}

// UnmarshalJSON keeps command names exact and rejects an explicit null variant.
// Required variant/operation/scope semantics are checked by Validate at the owning boundary.
func (c *InferenceCommand) UnmarshalJSON(raw []byte) error {
	type plain InferenceCommand
	var value plain
	if lifecycle.DecodeStrict(raw, &value) != nil {
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	if value, present := fields["derived"]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	if _, present := fields["image_registry_pin_id"]; present && (value.RegistryPinID == "" || value.Action != "deploy" || !inferenceUUID.MatchString(value.RegistryPinID)) {
		return errors.New("INFERENCE_CONFIGURATION_INVALID")
	}
	*c = InferenceCommand(value)
	return nil
}

func (c InferenceCommand) Operation() string {
	if c.Derived != nil {
		return DerivedInferenceOperation
	}
	return InferenceOperation(c.Action)
}
func (c InferenceCommand) validateDerived(operation string) error {
	bad := errors.New("INFERENCE_CONFIGURATION_INVALID")
	if operation != DerivedInferenceOperation || !inferenceActions[c.Action] {
		return errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	identity := c.Derived
	if identity == nil || identity.SchemaVersion != 1 || identity.Catalog.Validate() != nil || identity.ServingRecipeSHA256 != DerivedServingRecipeDigest() || !inferenceUUID.MatchString(c.DeploymentID) || c.ModelID != identity.Catalog.ModelID || !inferenceGPUUUID.MatchString(c.GPUUUID) || c.Revision < 1 || c.Revision > 9007199254740991 {
		return bad
	}
	digest, e := identity.Catalog.Digest()
	if e != nil || c.CatalogDigest != digest {
		return bad
	}
	return nil
}
func (c InferenceCommand) ModelFiles() []ModelFile {
	if c.Derived == nil {
		return ApprovedModel().Files
	}
	out := make([]ModelFile, 0, len(c.Derived.Catalog.Files))
	for _, file := range c.Derived.Catalog.Files {
		out = append(out, ModelFile{Path: file.Path, SizeBytes: file.SizeBytes, SHA256: file.SHA256})
	}
	return out
}
func (c InferenceCommand) ModelFile(name string) (ModelFile, bool) {
	for _, file := range c.ModelFiles() {
		if file.Path == name {
			return file, true
		}
	}
	return ModelFile{}, false
}
func (c InferenceCommand) ModelBytes() int64 {
	var total int64
	for _, file := range c.ModelFiles() {
		total += file.SizeBytes
	}
	return total
}

// ValidateForCommand retains the original base-only range contract and admits derived sizes only with an exact typed command.
func (r ModelFileRequest) ValidateForCommand(c InferenceCommand) error {
	if c.Validate(c.Operation()) != nil || c.Action != "deploy" {
		return errors.New("VALIDATION_FAILED")
	}
	file, ok := c.ModelFile(r.File)
	if !ok || r.Offset < 0 || r.Offset%ModelTransferChunkBytes != 0 || r.Offset >= file.SizeBytes || r.Length != min(ModelTransferChunkBytes, file.SizeBytes-r.Offset) {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func (d DeploymentObservation) validateIdentity() error {
	if d.ModelKind == "" {
		if d.ServingRecipeSHA256 != "" {
			return errors.New("VALIDATION_FAILED")
		}
		return (InferenceCommand{Action: "observe", DeploymentID: d.DeploymentID, ModelID: d.ModelID, CatalogDigest: d.CatalogDigest, GPUUUID: d.GPUUUID, Revision: d.Revision}).Validate(InferenceOperation("observe"))
	}
	if d.ModelKind != DerivedModelKind || d.ServingRecipeSHA256 != DerivedServingRecipeDigest() || !inferenceUUID.MatchString(d.DeploymentID) || !inferenceUUID.MatchString(d.ModelID) || !sha256Pattern.MatchString(d.CatalogDigest) || d.CatalogDigest == ModelCatalogDigest() || !inferenceGPUUUID.MatchString(d.GPUUUID) || d.Revision < 1 || d.Revision > 9007199254740991 {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func (c InferenceCommand) MatchesServingIdentity(d DeploymentObservation) bool {
	if c.Derived == nil {
		return d.ModelKind == "" && d.ServingRecipeSHA256 == ""
	}
	return d.ModelKind == DerivedModelKind && d.ServingRecipeSHA256 == c.Derived.ServingRecipeSHA256
}
