package runtime

import (
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Validate the complete disjoint lease before any executor receives it.
func validateExecutionLease(profile string, lease wire.Lease) error {
	bad := errors.New("AGENT_TASK_UNSUPPORTED")
	if !wire.OperationAllowed(profile, lease.Operation) || !idPattern.MatchString(lease.TaskID) || !token(lease.LeaseToken) || lease.Attempt < 1 || lease.Attempt > 3 {
		return bad
	}
	variants := 0
	if lease.Preparation != nil {
		variants++
	}
	if lease.Training != nil {
		variants++
	}
	if lease.Inference != nil {
		variants++
	}
	if lease.Component != nil {
		variants++
	}
	if lease.Gateway != nil {
		variants++
	}
	if lease.Operation == wire.CollectFacts {
		if variants != 0 {
			return bad
		}
		return nil
	}
	if variants != 1 {
		return bad
	}
	switch {
	case lease.Operation == wire.ModelPreparationRunOperation:
		if profile != wire.DockerModelLifecycleProfile || lease.Preparation == nil || lease.Preparation.Validate() != nil {
			return bad
		}
	case lease.Operation == wire.TrainingRunOperation:
		if !wire.IsTrainingProfile(profile) || lease.Training == nil || lease.Training.Validate() != nil {
			return bad
		}
	case wire.IsInferenceOperation(lease.Operation):
		if lease.Inference == nil || lease.Inference.Validate(lease.Operation) != nil {
			return bad
		}
	case wire.IsGatewayOperation(lease.Operation):
		if profile != wire.DockerInferenceProfile || lease.Gateway == nil || lease.Gateway.Validate(lease.Operation) != nil {
			return bad
		}
	case wire.IsComponentOperation(lease.Operation):
		if lease.Component == nil || lease.Component.ValidateForProfile(profile, lease.Operation) != nil {
			return bad
		}
	default:
		return bad
	}
	return nil
}
