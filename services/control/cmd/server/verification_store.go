package main

import (
	"errors"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// An explicit verification root is a binding assertion, never an override of
// the actual runtime configuration. Legacy no-root callers retain their scope.
func validateVerificationStore(c config.Config, command, model, dataset, training, preparation string) error {
	roots := []string{model, dataset, training, preparation}
	selected := -1
	configured := ""
	switch command {
	case "verify-model-store":
		selected, configured = 0, c.Models.ArtifactRoot
	case "verify-dataset-store":
		selected, configured = 1, c.Datasets.ArtifactRoot
	case "verify-training-files":
		selected, configured = 2, c.Training.ArtifactRoot
	case "verify-preparation-files":
		selected, configured = 3, c.ModelPreparation.ArtifactRoot
	default:
		return nil
	}
	provided := false
	for i, root := range roots {
		if root != "" {
			provided = true
			if i != selected {
				return errors.New("STORE_VERIFICATION_OTHER_ROOT_REFUSED")
			}
		}
	}
	if provided && (configured == "" || roots[selected] != configured || !filepath.IsAbs(configured) || filepath.Clean(configured) != configured || configured == string(filepath.Separator)) {
		return errors.New("STORE_VERIFICATION_CONFIGURED_ROOT_REQUIRED")
	}
	return nil
}
