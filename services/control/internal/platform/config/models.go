package config

import (
	"errors"
	"reflect"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
)

// These are immutable persistence ceilings, not deployment policy defaults.
const ModelImportSchemaMaximumAttempts = 3
const ModelImportSchemaMaximumBundleBytes = 2 * 1024 * 1024 * 1024

const (
	modelImportSecondsEnvironment   = "PROJECT_ONE_MODELS_IMPORT_SECONDS"
	modelImportPollEnvironment      = "PROJECT_ONE_MODELS_IMPORT_POLL_SECONDS"
	modelCleanupEnvironment         = "PROJECT_ONE_MODELS_CLEANUP_SECONDS"
	modelMaximumAttemptsEnvironment = "PROJECT_ONE_MODELS_MAXIMUM_ATTEMPTS"
	modelMaximumBundleEnvironment   = "PROJECT_ONE_MODELS_MAXIMUM_BUNDLE_BYTES"
)

type ModelsPolicy struct {
	ImportSeconds      int `yaml:"import_seconds"`
	ImportPollSeconds  int `yaml:"import_poll_seconds"`
	CleanupSeconds     int `yaml:"cleanup_seconds"`
	MaximumAttempts    int `yaml:"maximum_attempts"`
	MaximumBundleBytes int `yaml:"maximum_bundle_bytes"`
}

func DefaultModelsPolicy() ModelsPolicy {
	return ModelsPolicy{ImportSeconds: 600, ImportPollSeconds: 2, CleanupSeconds: 2,
		MaximumAttempts: ModelImportSchemaMaximumAttempts, MaximumBundleBytes: ModelImportSchemaMaximumBundleBytes}
}

func (p ModelsPolicy) Validate() error {
	if p.ImportSeconds < 60 || p.ImportSeconds > 7200 || p.ImportPollSeconds < 1 || p.ImportPollSeconds > 60 || p.CleanupSeconds < 1 || p.CleanupSeconds > 30 || p.MaximumAttempts < 1 || p.MaximumAttempts > ModelImportSchemaMaximumAttempts || int64(p.MaximumBundleBytes) < modelartifact.MinimumArchiveBytes() || p.MaximumBundleBytes > ModelImportSchemaMaximumBundleBytes {
		return errors.New("CONFIG_MODELS_BOUNDS_INVALID")
	}
	return nil
}

func (p *ModelsPolicy) UnmarshalYAML(node *yaml.Node) error {
	if strictModelsYAML(node) != nil {
		return errors.New("CONFIG_MODELS_BOUNDS_INVALID")
	}
	type plain ModelsPolicy
	value := plain(*p)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_MODELS_BOUNDS_INVALID")
	}
	*p = ModelsPolicy(value)
	return nil
}

func modelPolicyAnchorsAbsent(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return false
	}
	for _, child := range node.Content {
		if !modelPolicyAnchorsAbsent(child) {
			return false
		}
	}
	return true
}

func strictModelsYAML(node *yaml.Node) error {
	if !modelPolicyAnchorsAbsent(node) || strictKnowledgeYAML(node, reflect.TypeOf(ModelsPolicy{})) != nil {
		return errors.New("CONFIG_MODELS_BOUNDS_INVALID")
	}
	return nil
}

// Validate group nulls/aliases before YAML merging can silently retain defaults.
func validateModelsYAML(raw []byte) error {
	var document yaml.Node
	bad := errors.New("CONFIG_MODELS_BOUNDS_INVALID")
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return bad
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "runtime" {
			continue
		}
		runtime := root.Content[i+1]
		if runtime.Kind != yaml.MappingNode {
			// Legacy operator configurations use runtime:null to select defaults.
			// Only an explicitly supplied models group belongs to this validator.
			continue
		}
		for j := 0; j+1 < len(runtime.Content); j += 2 {
			if runtime.Content[j].Value == "models" && strictModelsYAML(runtime.Content[j+1]) != nil {
				return bad
			}
		}
	}
	return nil
}

func (p *ModelsPolicy) environmentField(key string) *int {
	switch key {
	case modelImportSecondsEnvironment:
		return &p.ImportSeconds
	case modelImportPollEnvironment:
		return &p.ImportPollSeconds
	case modelCleanupEnvironment:
		return &p.CleanupSeconds
	case modelMaximumAttemptsEnvironment:
		return &p.MaximumAttempts
	case modelMaximumBundleEnvironment:
		return &p.MaximumBundleBytes
	default:
		return nil
	}
}
