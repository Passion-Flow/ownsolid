package deployments

import (
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"testing"
)

func TestRegistryProbeAdmissionPreservesEveryOtherFailure(t *testing.T) {
	valid := wire.InferenceCommand{Action: "deploy", DeploymentID: "12345678-1234-1234-1234-123456789abc", ModelID: "22345678-1234-1234-1234-123456789abc", CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Revision: 1, RegistryPinID: "32345678-1234-1234-1234-123456789abc"}
	for _, sample := range []struct {
		name, probe, pin, action, want string
		capability                     bool
	}{
		{"pinned_missing", wire.InferenceImageMissing, valid.RegistryPinID, "deploy", "", true},
		{"ordinary_missing", wire.InferenceImageMissing, "", "deploy", wire.InferenceImageMissing, true},
		{"disabled_loader", wire.InferenceImageMissing, valid.RegistryPinID, "deploy", "AGENT_PROFILE_REJECTED", false},
		{"legacy_present", "", valid.RegistryPinID, "deploy", "AGENT_PROFILE_REJECTED", false},
		{"pinned_present", "", valid.RegistryPinID, "deploy", "", true},
		{"ordinary_present", "", "", "deploy", "", false},
		{"engine_failure", "INFERENCE_IMAGE_UNAVAILABLE", valid.RegistryPinID, "deploy", "INFERENCE_IMAGE_UNAVAILABLE", true},
		{"wrong_image", "INFERENCE_IMAGE_INVALID", valid.RegistryPinID, "deploy", "INFERENCE_IMAGE_INVALID", true},
		{"GPU_failure", "INFERENCE_GPU_UNAVAILABLE", valid.RegistryPinID, "deploy", "INFERENCE_GPU_UNAVAILABLE", true},
		{"invalid_pin", wire.InferenceImageMissing, "invalid", "deploy", "INFERENCE_CONFIGURATION_INVALID", true},
		{"wrong_action", wire.InferenceImageMissing, valid.RegistryPinID, "stop", "INFERENCE_CONFIGURATION_INVALID", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			command := valid
			command.RegistryPinID = sample.pin
			command.Action = sample.action
			o := wire.InferenceObservation{ProbeErrorCode: sample.probe, RegistryImageDelivery: wire.RegistryDeliveryCapability(sample.capability)}
			e := registryProbeAdmission(o, command)
			if sample.want == "" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil || e.Error() != sample.want {
				t.Fatal("probe authority changed", e)
			}
		})
	}
}
