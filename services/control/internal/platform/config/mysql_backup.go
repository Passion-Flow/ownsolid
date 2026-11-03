package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// MySQLBackupConfiguration owns native local logical backup execution policy.
// Endpoint, credentials, CA, version and TLS retain the existing mysqlstore owner.
type MySQLBackupConfiguration struct {
	ClientFile              string `yaml:"client_file" json:"client_file"`
	ClientSHA256            string `yaml:"client_sha256" json:"client_sha256"`
	DumpFile                string `yaml:"dump_file" json:"dump_file"`
	DumpSHA256              string `yaml:"dump_sha256" json:"dump_sha256"`
	ArtifactManifestFile    string `yaml:"artifact_manifest_file" json:"artifact_manifest_file"`
	ScratchRoot             string `yaml:"scratch_root" json:"scratch_root"`
	OperationSeconds        int    `yaml:"operation_seconds" json:"operation_seconds"`
	TerminationGraceSeconds int    `yaml:"termination_grace_seconds" json:"termination_grace_seconds"`
	MaximumStoreBytes       int64  `yaml:"maximum_store_bytes" json:"maximum_store_bytes"`
	MaximumStoreFiles       int    `yaml:"maximum_store_files" json:"maximum_store_files"`
	MaximumSQLBytes         int64  `yaml:"maximum_sql_bytes" json:"maximum_sql_bytes"`
	MaximumManifestBytes    int64  `yaml:"maximum_manifest_bytes" json:"maximum_manifest_bytes"`
	MinimumFreeBytes        int64  `yaml:"minimum_free_bytes" json:"minimum_free_bytes"`
}

func DefaultMySQLBackupPolicy() MySQLBackupConfiguration {
	return MySQLBackupConfiguration{OperationSeconds: 3600, TerminationGraceSeconds: 10, MaximumSQLBytes: 16 << 30, MaximumStoreBytes: 128 << 30, MaximumStoreFiles: 1000000, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 1 << 30}
}

var mysqlBackupDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func mysqlBackupPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && !strings.ContainsFunc(path, unicode.IsControl)
}
func (c MySQLBackupConfiguration) Validate() error {
	seen := map[string]bool{}
	for _, path := range []string{c.ClientFile, c.DumpFile, c.ArtifactManifestFile, c.ScratchRoot} {
		if !mysqlBackupPath(path) || seen[path] {
			return errors.New("CONFIG_MYSQL_BACKUP_INVALID")
		}
		seen[path] = true
	}
	if !mysqlBackupDigest.MatchString(c.ClientSHA256) || !mysqlBackupDigest.MatchString(c.DumpSHA256) || c.ClientSHA256 == strings.Repeat("0", 64) || c.DumpSHA256 == strings.Repeat("0", 64) || c.OperationSeconds < 30 || c.OperationSeconds > 86400 || c.TerminationGraceSeconds < 1 || c.TerminationGraceSeconds > 60 || c.MaximumStoreBytes < 1<<20 || c.MaximumStoreBytes > 1<<40 || c.MaximumStoreFiles < 1 || c.MaximumStoreFiles > 1000000 || c.MaximumSQLBytes < 1<<20 || c.MaximumSQLBytes > 1<<40 || c.MaximumManifestBytes < 4096 || c.MaximumManifestBytes > 16<<20 || c.MinimumFreeBytes < 1<<20 || c.MinimumFreeBytes > 1<<40 {
		return errors.New("CONFIG_MYSQL_BACKUP_INVALID")
	}
	return nil
}
func (c *MySQLBackupConfiguration) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_MYSQL_BACKUP_INVALID")
	if strictKnowledgeYAML(node, reflect.TypeOf(*c)) != nil {
		return bad
	}
	type plain MySQLBackupConfiguration
	v := plain(DefaultMySQLBackupPolicy())
	if node.Decode(&v) != nil || MySQLBackupConfiguration(v).Validate() != nil {
		return bad
	}
	*c = MySQLBackupConfiguration(v)
	return nil
}
func (c Config) validateMySQLBackup() error {
	if c.Database.MySQLBackup == nil {
		return nil
	}
	if c.DatabaseEngine() != MySQLDatabaseEngine {
		return errors.New("CONFIG_MYSQL_BACKUP_INVALID")
	}
	return c.Database.MySQLBackup.Validate()
}
func validateMySQLBackupYAML(raw []byte) error {
	var node yaml.Node
	bad := errors.New("CONFIG_MYSQL_BACKUP_INVALID")
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
			if database.Content[j].Value == "mysql_backup" && strictKnowledgeYAML(database.Content[j+1], reflect.TypeOf(MySQLBackupConfiguration{})) != nil {
				return bad
			}
		}
	}
	return nil
}

const mysqlBackupEnvironmentPrefix = "PROJECT_ONE_MYSQL_BACKUP_"

func mysqlBackupEnvironment(key string) bool {
	if !strings.HasPrefix(key, mysqlBackupEnvironmentPrefix) {
		return false
	}
	t := reflect.TypeOf(MySQLBackupConfiguration{})
	for i := 0; i < t.NumField(); i++ {
		if key == mysqlBackupEnvironmentPrefix+strings.ToUpper(t.Field(i).Tag.Get("yaml")) {
			return true
		}
	}
	return false
}
func (c *Config) applyMySQLBackupEnvironment(key, value string) error {
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if !mysqlBackupEnvironment(key) || value == "" {
		return bad
	}
	if c.Database.MySQLBackup == nil {
		policy := DefaultMySQLBackupPolicy()
		c.Database.MySQLBackup = &policy
	}
	v := reflect.ValueOf(c.Database.MySQLBackup).Elem()
	for i := 0; i < v.NumField(); i++ {
		if key != mysqlBackupEnvironmentPrefix+strings.ToUpper(v.Type().Field(i).Tag.Get("yaml")) {
			continue
		}
		field := v.Field(i)
		if field.Kind() == reflect.String {
			field.SetString(value)
			return nil
		}
		n, e := strconv.ParseInt(value, 10, field.Type().Bits())
		if e != nil || strconv.FormatInt(n, 10) != value {
			return bad
		}
		field.SetInt(n)
		return nil
	}
	return bad
}
