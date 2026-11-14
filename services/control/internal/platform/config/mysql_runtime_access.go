package config

import (
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const mysqlRuntimeAccountHostEnvironment = "PROJECT_ONE_MYSQL_RUNTIME_ACCOUNT_HOST"
const mysqlRuntimeIntentFileEnvironment = "PROJECT_ONE_MYSQL_RUNTIME_INTENT_FILE"

// MySQLRuntimeAccessConfiguration owns provisioning identity/recovery policy only.
// User/password/TLS/endpoint remain in the one protected mysqlstore input.
type MySQLRuntimeAccessConfiguration struct {
	AccountHost string `yaml:"account_host" json:"account_host"`
	IntentFile  string `yaml:"intent_file" json:"intent_file"`
}

var mysqlAccountDNS = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func validMySQLAccountHost(v string) bool {
	if v == "" || len(v) > 253 || v == "true" || v == "false" {
		return false
	}
	if address, e := netip.ParseAddr(v); e == nil {
		return address.Zone() == "" && !address.Is4In6() && address.String() == v && (address.IsGlobalUnicast() || address.IsLoopback())
	}
	return mysqlAccountDNS.MatchString(v)
}
func (c MySQLRuntimeAccessConfiguration) Validate() error {
	if !validMySQLAccountHost(c.AccountHost) || !filepath.IsAbs(c.IntentFile) || filepath.Clean(c.IntentFile) != c.IntentFile || c.IntentFile == string(filepath.Separator) || strings.ContainsFunc(c.IntentFile, unicode.IsControl) {
		return errors.New("CONFIG_MYSQL_RUNTIME_ACCESS_INVALID")
	}
	return nil
}
func (c *MySQLRuntimeAccessConfiguration) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_MYSQL_RUNTIME_ACCESS_INVALID")
	if strictKnowledgeYAML(node, reflect.TypeOf(*c)) != nil {
		return bad
	}
	type plain MySQLRuntimeAccessConfiguration
	var value plain
	if node.Decode(&value) != nil || MySQLRuntimeAccessConfiguration(value).Validate() != nil {
		return bad
	}
	*c = MySQLRuntimeAccessConfiguration(value)
	return nil
}
func (c Config) validateMySQLRuntimeAccess() error {
	if c.Database.MySQLRuntimeAccess == nil {
		return nil
	}
	if c.DatabaseEngine() != MySQLDatabaseEngine {
		return errors.New("CONFIG_MYSQL_RUNTIME_ACCESS_INVALID")
	}
	return c.Database.MySQLRuntimeAccess.Validate()
}
func validateMySQLRuntimeAccessYAML(raw []byte) error {
	var node yaml.Node
	bad := errors.New("CONFIG_MYSQL_RUNTIME_ACCESS_INVALID")
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return bad
	}
	root := node.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "database" {
			continue
		}
		database := root.Content[i+1]
		if database.Kind != yaml.MappingNode {
			return bad
		}
		for j := 0; j+1 < len(database.Content); j += 2 {
			if database.Content[j].Value == "mysql_runtime_access" && strictKnowledgeYAML(database.Content[j+1], reflect.TypeOf(MySQLRuntimeAccessConfiguration{})) != nil {
				return bad
			}
		}
	}
	return nil
}
func (c *Config) applyMySQLRuntimeAccessEnvironment(key, value string) error {
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if c.Database.MySQLRuntimeAccess == nil {
		c.Database.MySQLRuntimeAccess = &MySQLRuntimeAccessConfiguration{}
	}
	switch key {
	case mysqlRuntimeAccountHostEnvironment:
		if !validMySQLAccountHost(value) {
			return bad
		}
		c.Database.MySQLRuntimeAccess.AccountHost = value
	case mysqlRuntimeIntentFileEnvironment:
		if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) || strings.ContainsFunc(value, unicode.IsControl) {
			return bad
		}
		c.Database.MySQLRuntimeAccess.IntentFile = value
	default:
		return bad
	}
	return nil
}
