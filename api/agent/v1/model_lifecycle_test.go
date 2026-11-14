package v1

import "testing"

func TestLifecycleProfileExplicitNegotiationPreservesEveryLegacyCapability(t *testing.T) {
	legacy := Capabilities(DockerAIProfile)
	current := Capabilities(DockerModelLifecycleProfile)
	if !KnownProfile(DockerModelLifecycleProfile) || ProfileVersion(DockerModelLifecycleProfile) != ModelLifecycleVersion || len(current) != len(legacy)+2 {
		t.Fatal("lifecycle profile identity")
	}
	for index, operation := range legacy {
		if current[index] != operation {
			t.Fatal("existing capability order changed")
		}
	}
	for _, operation := range []string{ModelPreparationRunOperation, DerivedInferenceOperation} {
		if !OperationAllowed(DockerModelLifecycleProfile, operation) {
			t.Fatal("explicit operation missing")
		}
		for _, profile := range []string{ReadOnlyProfile, SystemdComponentsProfile, SystemdMaintenanceProfile, DockerInferenceProfile, DockerAIProfile} {
			if OperationAllowed(profile, operation) {
				t.Fatal("legacy enrollment silently upgraded", profile)
			}
		}
	}
	if !OperationAllowed(DockerModelLifecycleProfile, TrainingRunOperation) || OperationAllowed(DockerModelLifecycleProfile, ComponentOperation("install")) {
		t.Fatal("mixed execution authority")
	}
	h := Handshake{ProtocolVersion: ModelLifecycleVersion, ExecutionProfile: DockerModelLifecycleProfile, Capabilities: current, Facts: validFacts()}
	if e := h.Validate(); e != nil {
		t.Fatal(e)
	}
	h.ProtocolVersion = AIVersion
	if h.Validate() == nil {
		t.Fatal("old protocol elevated")
	}
	h.ProtocolVersion = ModelLifecycleVersion
	h.Capabilities = legacy
	if h.Validate() == nil {
		t.Fatal("missing new capability accepted")
	}
	h.Capabilities = current
	h.ExecutionProfile = DockerAIProfile
	if h.Validate() == nil {
		t.Fatal("old profile with new protocol/capability accepted")
	}
}
