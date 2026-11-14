package v1

import lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"

const DockerModelLifecycleProfile = lifecycle.NodeProfile
const ModelLifecycleVersion = lifecycle.NodeProtocolVersion
const ModelPreparationRunOperation = lifecycle.RunOperation
const DerivedInferenceOperation = lifecycle.DerivedInferenceOperation

func IsTrainingProfile(profile string) bool {
	return profile == DockerAIProfile || profile == DockerModelLifecycleProfile
}
