package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

const DefaultDamengReadinessSeconds = 10
const MaximumDamengReadinessSeconds = 300
const damengRuntimeConfigurationEnvironment = "PROJECT_ONE_DAMENG_RUNTIME_CONFIGURATION_FILE"
const damengSchemaConfigurationEnvironment = "PROJECT_ONE_DAMENG_SCHEMA_CONFIGURATION_FILE"
const damengReadinessEnvironment = "PROJECT_ONE_DAMENG_READINESS_SECONDS"

// References only. Native connection, credential references and schema/readback
// budgets remain in their existing protected typed owners; never copy their fields.
type DamengServingConfiguration struct {
	RuntimeConfigurationFile string `yaml:"runtime_configuration_file" json:"runtime_configuration_file"`
	SchemaConfigurationFile  string `yaml:"schema_configuration_file" json:"schema_configuration_file"`
	ReadinessSeconds         int    `yaml:"readiness_seconds" json:"readiness_seconds"`
}

func (DamengServingConfiguration) String() string   { return "DAMENG_SERVING_CONFIGURATION_REDACTED" }
func (DamengServingConfiguration) GoString() string { return "DAMENG_SERVING_CONFIGURATION_REDACTED" }
func nativeServingReference(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && !strings.ContainsAny(path, "\x00\r\n")
}
func (d DamengServingConfiguration) Validate() error {
	if !nativeServingReference(d.RuntimeConfigurationFile) || !nativeServingReference(d.SchemaConfigurationFile) || d.RuntimeConfigurationFile == d.SchemaConfigurationFile || d.ReadinessSeconds < 1 || d.ReadinessSeconds > MaximumDamengReadinessSeconds {
		return errors.New("CONFIG_DAMENG_SERVING_INVALID")
	}
	return nil
}
func (d *DamengServingConfiguration) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_DAMENG_SERVING_INVALID")
	if strictKnowledgeYAML(node, reflect.TypeOf(*d)) != nil {
		return bad
	}
	type plain DamengServingConfiguration
	v := plain{ReadinessSeconds: DefaultDamengReadinessSeconds}
	if node.Decode(&v) != nil || DamengServingConfiguration(v).Validate() != nil {
		return bad
	}
	*d = DamengServingConfiguration(v)
	return nil
}
func (c Config) validateDamengServing() error {
	if c.DatabaseEngine() != DamengDatabaseEngine {
		if c.Database.Dameng != nil {
			return errors.New("CONFIG_DATABASE_SELECTION_INVALID")
		}
		return nil
	}
	if c.Database.Dameng == nil || c.Database.Dameng.Validate() != nil || c.Database.DSNFile != "" || c.Database.MySQLConfigurationFile != "" || c.Database.MySQLBackup != nil || c.Database.MySQLRuntimeAccess != nil || c.Database.LockTimeoutMilliseconds != nil {
		return errors.New("CONFIG_DAMENG_SERVING_INVALID")
	}
	return nil
}
func validateDamengServingYAML(raw []byte) error {
	var root yaml.Node
	if yaml.Unmarshal(raw, &root) != nil || len(root.Content) != 1 {
		return errors.New("CONFIG_DATABASE_SELECTION_INVALID")
	}
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "database" {
			continue
		}
		db := top.Content[i+1]
		native := false
		group := false
		for j := 0; j+1 < len(db.Content); j += 2 {
			k, v := db.Content[j].Value, db.Content[j+1]
			if k == "engine" {
				native = v.Value == DamengDatabaseEngine
			}
			if k == "dameng" {
				group = true
				if v.Kind != yaml.MappingNode || strictKnowledgeYAML(v, reflect.TypeOf(DamengServingConfiguration{})) != nil {
					return errors.New("CONFIG_DAMENG_SERVING_INVALID")
				}
			}
		}
		if native != group {
			return errors.New("CONFIG_DAMENG_SERVING_INVALID")
		}
		if native {
			for j := 0; j+1 < len(db.Content); j += 2 {
				switch db.Content[j].Value {
				case "engine", "dameng":
				default:
					return errors.New("CONFIG_DAMENG_SERVING_INVALID")
				}
			}
		}
	}
	return nil
}
func damengServingEnvironment(key string) bool {
	return key == damengRuntimeConfigurationEnvironment || key == damengSchemaConfigurationEnvironment || key == damengReadinessEnvironment
}
func (c *Config) applyDamengServingEnvironment(key, value string) error {
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if c.DatabaseEngine() != DamengDatabaseEngine || c.Database.Dameng == nil || value == "" {
		return bad
	}
	d := *c.Database.Dameng
	switch key {
	case damengRuntimeConfigurationEnvironment:
		if !nativeServingReference(value) {
			return bad
		}
		d.RuntimeConfigurationFile = value
	case damengSchemaConfigurationEnvironment:
		if !nativeServingReference(value) {
			return bad
		}
		d.SchemaConfigurationFile = value
	case damengReadinessEnvironment:
		for _, r := range value {
			if r < '0' || r > '9' {
				return bad
			}
		}
		n, e := strconv.Atoi(value)
		if e != nil || n < 1 || n > MaximumDamengReadinessSeconds {
			return bad
		}
		d.ReadinessSeconds = n
	default:
		return bad
	}
	if d.Validate() != nil {
		return bad
	}
	c.Database.Dameng = &d
	return nil
}
