package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"path/filepath"
	"reflect"
	"regexp"
)

type LocalizationPolicy struct {
	PackageBytes    int `yaml:"package_bytes"`
	FontBytes       int `yaml:"font_bytes"`
	AggregateBytes  int `yaml:"aggregate_bytes"`
	MaximumLocales  int `yaml:"maximum_locales"`
	MaximumMessages int `yaml:"maximum_messages"`
	MaximumFonts    int `yaml:"maximum_fonts"`
	CacheSeconds    int `yaml:"cache_seconds"`
}
type LocalizationConfiguration struct {
	PackageDirectory string   `yaml:"package_directory"`
	RequiredLocales  []string `yaml:"required_locales"`
}

var localeTag = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8}){0,3}$`)

func (c LocalizationConfiguration) Validate() error {
	if c.PackageDirectory != "" && (!filepath.IsAbs(c.PackageDirectory) || filepath.Clean(c.PackageDirectory) != c.PackageDirectory) {
		return errors.New("CONFIG_LOCALIZATION_DIRECTORY_INVALID")
	}
	seen := map[string]bool{}
	if len(c.RequiredLocales) > 256 {
		return errors.New("CONFIG_LOCALIZATION_LOCALES_INVALID")
	}
	for _, value := range c.RequiredLocales {
		if len(value) > 35 || !localeTag.MatchString(value) || seen[value] {
			return errors.New("CONFIG_LOCALIZATION_LOCALES_INVALID")
		}
		seen[value] = true
	}
	return nil
}
func (p LocalizationPolicy) Validate() error {
	if !p.inBounds() || p.AggregateBytes < p.PackageBytes {
		return errors.New("CONFIG_LOCALIZATION_POLICY_INVALID")
	}
	return nil
}
func (c *LocalizationConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*c)) != nil {
		return errors.New("CONFIG_LOCALIZATION_INVALID")
	}
	type plain LocalizationConfiguration
	var value plain
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_LOCALIZATION_INVALID")
	}
	*c = LocalizationConfiguration(value)
	return nil
}
func (p *LocalizationPolicy) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*p)) != nil {
		return errors.New("CONFIG_LOCALIZATION_POLICY_INVALID")
	}
	type plain LocalizationPolicy
	value := plain(*p)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_LOCALIZATION_POLICY_INVALID")
	}
	*p = LocalizationPolicy(value)
	return nil
}
func validateLocalizationYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_LOCALIZATION_INVALID")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, value := root.Content[i].Value, root.Content[i+1]
		if name == "localization" && strictKnowledgeYAML(value, reflect.TypeOf(LocalizationConfiguration{})) != nil {
			return errors.New("CONFIG_LOCALIZATION_INVALID")
		}
		if name == "runtime" && value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "localization" && strictKnowledgeYAML(value.Content[j+1], reflect.TypeOf(LocalizationPolicy{})) != nil {
					return errors.New("CONFIG_LOCALIZATION_POLICY_INVALID")
				}
			}
		}
	}
	return nil
}
