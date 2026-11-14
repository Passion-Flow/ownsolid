package config

import (
	"errors"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
)

const registryConfigEnvironment = "PROJECT_ONE_REGISTRY_CONFIG_FILE"

func registryConfigurationPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func validateImageRegistryYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil {
		return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
	}
	root := document.Content[0]
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "image_registry_config_file" {
			value := root.Content[index+1]
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || !registryConfigurationPath(value.Value) {
				return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
			}
		}
	}
	return nil
}

func (c *Config) resolveImageRegistryConfiguration() error {
	c.ImageRegistry = nil
	if c.ImageRegistryConfigFile == "" {
		return nil
	}
	if !registryConfigurationPath(c.ImageRegistryConfigFile) {
		return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
	}
	// The central configuration owner pins the selected snapshot. Do not enter
	// the legacy offline-factory pathname reader from this configuration closure.
	raw, err := readPrivateFile(c.ImageRegistryConfigFile, registryconfig.MaximumConfigurationBytes, "CONFIG_IMAGE_REGISTRY_INVALID")
	if err != nil {
		return err
	}
	value, err := registryconfig.Decode(raw)
	if err != nil {
		return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
	}
	c.ImageRegistry = &value
	return c.validateImageRegistryConfiguration()
}

func (c Config) validateImageRegistryConfiguration() error {
	if c.ImageRegistryConfigFile == "" && c.ImageRegistry == nil {
		return nil
	}
	if !registryConfigurationPath(c.ImageRegistryConfigFile) || c.ImageRegistry == nil || c.ImageRegistry.Validate() != nil {
		return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
	}
	for _, root := range []string{c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot} {
		if root == "" {
			continue
		}
		for _, registry := range []string{c.ImageRegistry.StoreRoot, c.ImageRegistry.PreparedReleaseRoot} {
			if root == registry || strings.HasPrefix(root, registry+string(filepath.Separator)) || strings.HasPrefix(registry, root+string(filepath.Separator)) {
				return errors.New("CONFIG_IMAGE_REGISTRY_STORAGE_OVERLAP")
			}
		}
	}
	return nil
}
