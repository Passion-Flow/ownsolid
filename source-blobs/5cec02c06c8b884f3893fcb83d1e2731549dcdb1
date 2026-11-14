package v1

import (
	"encoding/json"
	"os"
	"testing"
)

func TestActualPythonModelCatalogDigestMatchesFrozenM06GoIdentity(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/training/configuration-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Digest string `json:"approved_model_catalog_sha256"`
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.Digest != ModelCatalogDigest() {
		t.Fatal("Python/Go model catalog compatibility drift")
	}
}

func TestCombinedAIProfilePreservesLegacyCapabilitiesAndExplicitAuthority(t *testing.T) {
	// The AI profile keeps the inference profile's model tasks in order. Gateway tasks are
	// the inference profile's alone until the AI profile serves gateways too (GW-123).
	legacy := Capabilities(DockerInferenceProfile)[:6]
	combined := Capabilities(DockerAIProfile)
	if len(combined) != 7 || ProfileVersion(DockerAIProfile) != AIVersion || OperationAllowed(DockerAIProfile, GatewayOperation("start")) {
		t.Fatal("profile contract")
	}
	for index, capability := range legacy {
		if capability != combined[index] {
			t.Fatal("legacy capability order changed")
		}
	}
	if combined[len(combined)-1] != TrainingRunOperation || OperationAllowed(DockerInferenceProfile, TrainingRunOperation) || OperationAllowed(ReadOnlyProfile, TrainingRunOperation) || OperationAllowed(DockerAIProfile, ComponentOperation("install")) {
		t.Fatal("implicit or mixed capability authority")
	}
	h := Handshake{ProtocolVersion: AIVersion, ExecutionProfile: DockerAIProfile, Capabilities: combined, Facts: validFacts()}
	if e := h.Validate(); e != nil {
		t.Fatal(e)
	}
	h.ProtocolVersion = InferenceVersion
	if h.Validate() == nil {
		t.Fatal("combined profile accepted old protocol")
	}
	h.ProtocolVersion = AIVersion
	h.ExecutionProfile = DockerInferenceProfile
	if h.Validate() == nil {
		t.Fatal("legacy profile silently elevated")
	}
}
