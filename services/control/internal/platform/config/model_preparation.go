package config

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// ModelPreparationConfiguration selects a real manufactured worker and its separate private persistent store.
// The empty group disables execution while leaving the independent heldout registry available.
type ModelPreparationConfiguration struct {
	ArtifactRoot       string `yaml:"artifact_root"`
	ImageReference     string `yaml:"image_reference"`
	ImageID            string `yaml:"image_id"`
	WorkerSourceSHA256 string `yaml:"worker_source_sha256"`
}

func (c ModelPreparationConfiguration) Enabled() bool { return c.ArtifactRoot != "" }
func (c ModelPreparationConfiguration) Validate(modelRoot, datasetRoot, trainingRoot string) error {
	if c == (ModelPreparationConfiguration{}) {
		return nil
	}
	if !filepath.IsAbs(c.ArtifactRoot) || filepath.Clean(c.ArtifactRoot) != c.ArtifactRoot || c.ArtifactRoot == string(filepath.Separator) || strings.ContainsFunc(c.ArtifactRoot, unicode.IsControl) {
		return errors.New("CONFIG_MODEL_PREPARATION_PATH_INVALID")
	}
	for _, root := range []string{modelRoot, datasetRoot, trainingRoot} {
		if root == "" {
			return errors.New("CONFIG_MODEL_PREPARATION_INPUT_STORES_REQUIRED")
		}
		if root == c.ArtifactRoot || strings.HasPrefix(root, c.ArtifactRoot+string(filepath.Separator)) || strings.HasPrefix(c.ArtifactRoot, root+string(filepath.Separator)) {
			return errors.New("CONFIG_MODEL_PREPARATION_PATH_CONFLICT")
		}
	}
	prefix := wire.ImageRepository + "@sha256:"
	if !strings.HasPrefix(c.ImageReference, prefix) || !datawire.ValidDigest(strings.TrimPrefix(c.ImageReference, prefix)) || !strings.HasPrefix(c.ImageID, "sha256:") || !datawire.ValidDigest(strings.TrimPrefix(c.ImageID, "sha256:")) || !datawire.ValidDigest(c.WorkerSourceSHA256) {
		return errors.New("CONFIG_MODEL_PREPARATION_ARTIFACT_IDENTITY_REQUIRED")
	}
	return nil
}
