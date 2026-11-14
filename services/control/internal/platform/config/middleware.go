package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
)

const middlewareConfigurationEnvironment = "PROJECT_ONE_MIDDLEWARE_CONFIG_FILE"
const middlewarePolicyEnvironment = "PROJECT_ONE_MIDDLEWARE_ENVIRONMENT_FILE"

func middlewareEnvironment(key string) bool {
	return key == middlewareConfigurationEnvironment || key == middlewarePolicyEnvironment
}

// References are typed here; the middleware owner loads their protected bytes
// once during composition. Importing the business module here would cycle Identity.
func (c Config) ValidateMiddlewareReferences() error {
	bad := errors.New("CONFIG_MIDDLEWARE_INVALID")
	if c.MiddlewareConfigFile == "" {
		if c.MiddlewareEnvironmentFile != "" {
			return bad
		}
		return nil
	}
	if !registryConfigurationPath(c.MiddlewareConfigFile) || c.MiddlewareEnvironmentFile != "" &&
		(!registryConfigurationPath(c.MiddlewareEnvironmentFile) || c.MiddlewareEnvironmentFile == c.MiddlewareConfigFile) {
		return bad
	}
	return nil
}

func validateMiddlewareYAML(raw []byte) error {
	var node yaml.Node
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_MIDDLEWARE_INVALID")
	}
	root := node.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i].Value, root.Content[i+1]
		if key == "middleware_config_file" || key == "middleware_environment_file" {
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || !registryConfigurationPath(value.Value) {
				return errors.New("CONFIG_MIDDLEWARE_INVALID")
			}
		}
	}
	return nil
}
