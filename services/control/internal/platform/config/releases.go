package config

import (
	"errors"

	"go.yaml.in/yaml/v3"
)

const releaseViewConfigurationEnvironment = "PROJECT_ONE_RELEASE_VIEW_CONFIG_FILE"

// The global boundary owns only the reference; the releaseView module owns its
// protected file and typed policy. Keeping these separate avoids an Identity cycle.
func (c Config) ValidateReleaseViewReference() error {
	if c.ReleaseViewConfigFile != "" && !registryConfigurationPath(c.ReleaseViewConfigFile) {
		return errors.New("CONFIG_RELEASE_VIEW_INVALID")
	}
	return nil
}

func validateReleaseViewYAML(raw []byte) error {
	var node yaml.Node
	bad := errors.New("CONFIG_RELEASE_VIEW_INVALID")
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return bad
	}
	root := node.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "release_view_config_file" {
			value := root.Content[i+1]
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || !registryConfigurationPath(value.Value) {
				return bad
			}
		}
	}
	return nil
}
