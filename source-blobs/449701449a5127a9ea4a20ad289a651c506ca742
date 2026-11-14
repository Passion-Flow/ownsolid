package modelpreparations

import (
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// executionRecipe binds a new intent to the actual configured immutable release and initialized input/output stores.
// Registry access is independent of this boundary. This does not attest node hardware or launch a worker.
func (s *Service) executionRecipe() (wire.ExecutionRecipe, error) {
	c := s.Identity.Config
	release := c.ModelPreparation
	if !release.Enabled() || release.Validate(c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot) != nil || s.Store == nil || s.Store.VerifyNamespace() != nil || s.Training == nil || s.Training.Store == nil || s.Nodes == nil || s.Models == nil || s.Datasets == nil || !s.Datasets.PublishedSourcesAvailable() {
		return wire.ExecutionRecipe{}, fail("MODEL_PREPARATION_UNAVAILABLE")
	}
	limits := c.Policy().Datasets.Limits
	limits.MaximumRows = min(limits.MaximumRows, wire.MaximumRows)
	limits.MaximumErrors = min(limits.MaximumErrors, limits.MaximumRows)
	if limits.MaximumRows < wire.MinimumRows {
		return wire.ExecutionRecipe{}, fail("MODEL_PREPARATION_INPUT_LIMIT_REACHED")
	}
	image := release.ImageID
	value := wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: release.ImageReference, Identity: trainingwire.ExecutionIdentity{Profile: trainingwire.ProductionProfile, Python: trainingwire.PythonVersion, WorkerSourceSHA256: release.WorkerSourceSHA256, ImageID: &image}, Resources: s.policy().Resources, DataLimits: limits, RecipeSHA256: wire.RecipeDigest(), ProcessorSHA256: wire.ProcessorDigest()}
	if value.Validate() != nil {
		return wire.ExecutionRecipe{}, fail("MODEL_PREPARATION_RECIPE_INVALID")
	}
	return value, nil
}
