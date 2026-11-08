package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

const maximumBundleComposeBytes = 1 << 20

// The current installer owns every service and image reference in the signed
// Compose path. Check these before Docker parses the file or any image is loaded.
func checkBundleCompose(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return errors.New("INSTALL_BUNDLE_COMPOSE_INVALID: cannot read the signed Compose input")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumBundleComposeBytes+1))
	if err != nil || len(raw) > maximumBundleComposeBytes {
		return errors.New("INSTALL_BUNDLE_COMPOSE_INVALID: signed Compose input exceeds the supported size")
	}
	var doc struct {
		Name     string                       `yaml:"name"`
		Services map[string]map[string]any    `yaml:"services"`
		Networks map[string]map[string]any    `yaml:"networks"`
		Secrets  map[string]map[string]string `yaml:"secrets"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return errors.New("INSTALL_BUNDLE_COMPOSE_INVALID: signed Compose input must use the local single-node format")
	}
	expected := map[string]string{
		"control": "${OWNSOLID_CONTROL_IMAGE:?}", "control-operator": "${OWNSOLID_CONTROL_IMAGE:?}",
		"postgres": "${OWNSOLID_POSTGRES_IMAGE:?}", "metrics": "${OWNSOLID_METRICS_IMAGE:?}", "logs": "${OWNSOLID_LOGS_IMAGE:?}",
	}
	if len(doc.Services) != len(expected) || doc.Name != "${OWNSOLID_PROJECT:?}" {
		return errors.New("INSTALL_BUNDLE_COMPOSE_INVALID: signed Compose input must declare exactly the supported services")
	}
	for role, image := range expected {
		service := doc.Services[role]
		if service["image"] != image || service["pull_policy"] != "never" {
			return fmt.Errorf("INSTALL_BUNDLE_COMPOSE_INVALID: %s must use its signed image and never pull", role)
		}
		for _, field := range []string{"build", "extends", "provider"} {
			if _, present := service[field]; present {
				return fmt.Errorf("INSTALL_BUNDLE_COMPOSE_INVALID: %s cannot use %s in a signed bundle", role, field)
			}
		}
	}
	for _, network := range doc.Networks {
		driver := network["driver"]
		if driver != nil && driver != "bridge" || network["driver_opts"] != nil || network["external"] != nil {
			return errors.New("INSTALL_BUNDLE_COMPOSE_INVALID: only local Compose bridge networks are supported")
		}
	}
	for _, secret := range doc.Secrets {
		if len(secret) != 1 || secret["file"] == "" {
			return errors.New("INSTALL_BUNDLE_COMPOSE_INVALID: Compose secrets must come from local files")
		}
	}
	return nil
}
