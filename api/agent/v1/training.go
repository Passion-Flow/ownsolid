package v1

import training "github.com/Passion-Flow/ownsolid/api/training/v1"

const DockerAIProfile = training.NodeProfile
const AIVersion = training.NodeProtocolVersion
const TrainingRunOperation = training.RunOperation

func IsInferenceProfile(profile string) bool {
	return profile == DockerInferenceProfile || profile == DockerAIProfile || profile == DockerModelLifecycleProfile
}
