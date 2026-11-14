package deployments

import wire "github.com/Passion-Flow/ownsolid/api/agent/v1"

// Only the owning server-created pinned deploy intent can defer actual image
// presence to its leased loader. Every other probe failure remains a refusal.
func registryProbeAdmission(observation wire.InferenceObservation, command wire.InferenceCommand) error {
	if command.RegistryPinID != "" {
		if command.Action != "deploy" || command.Validate(command.Operation()) != nil {
			return fail("INFERENCE_CONFIGURATION_INVALID")
		}
		if !observation.RegistryImageDelivery {
			return fail("AGENT_PROFILE_REJECTED")
		}
	}
	if observation.ProbeErrorCode == "" {
		return nil
	}
	if observation.ProbeErrorCode == wire.InferenceImageMissing && command.RegistryPinID != "" {
		return nil
	}
	return fail(observation.ProbeErrorCode)
}
