package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const PostgreSQLDatabaseEngine = "postgresql"
const MySQLDatabaseEngine = "mysql"
const DamengDatabaseEngine = "dameng"
const databaseEngineEnvironment = "PROJECT_ONE_DATABASE_ENGINE"
const mysqlConfigurationEnvironment = "PROJECT_ONE_MYSQL_CONFIGURATION_FILE"
const mysqlMigrationLockEnvironment = "PROJECT_ONE_MYSQL_MIGRATION_LOCK_WAIT_SECONDS"
const mysqlMigrationStepEnvironment = "PROJECT_ONE_MYSQL_MIGRATION_STEP_SECONDS"
const mysqlMigrationTotalEnvironment = "PROJECT_ONE_MYSQL_MIGRATION_TOTAL_SECONDS"
const mysqlMigrationCleanupEnvironment = "PROJECT_ONE_MYSQL_MIGRATION_CLEANUP_SECONDS"
const mysqlMigrationMetadataEnvironment = "PROJECT_ONE_MYSQL_MIGRATION_MAXIMUM_METADATA_BYTES"

// MySQLMigrationPolicy owns DDL/readiness budgets, not MySQL connection policy.
// Catalog step count is a fixed source invariant supplied by the migration owner.
type MySQLMigrationPolicy struct {
	LockWaitSeconds      int `yaml:"lock_wait_seconds"`
	StepSeconds          int `yaml:"step_seconds"`
	TotalSeconds         int `yaml:"total_seconds"`
	CleanupSeconds       int `yaml:"cleanup_seconds"`
	MaximumMetadataBytes int `yaml:"maximum_metadata_bytes"`
}

func DefaultMySQLMigrationPolicy() MySQLMigrationPolicy {
	return MySQLMigrationPolicy{LockWaitSeconds: 1, StepSeconds: 30, TotalSeconds: 600, CleanupSeconds: 5, MaximumMetadataBytes: 1 << 20}
}
func (p MySQLMigrationPolicy) Validate() error {
	if p.LockWaitSeconds < 1 || p.LockWaitSeconds > 60 || p.StepSeconds < 1 || p.StepSeconds > 300 || p.TotalSeconds < p.LockWaitSeconds+p.StepSeconds || p.TotalSeconds > 1800 || p.CleanupSeconds < 1 || p.CleanupSeconds > 30 || p.MaximumMetadataBytes < 4096 || p.MaximumMetadataBytes > 1<<20 {
		return errors.New("CONFIG_MYSQL_MIGRATION_POLICY_INVALID")
	}
	return nil
}
func (p *MySQLMigrationPolicy) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_MYSQL_MIGRATION_POLICY_INVALID")
	if strictKnowledgeYAML(node, reflect.TypeOf(*p)) != nil {
		return bad
	}
	type plain MySQLMigrationPolicy
	v := plain(*p)
	if v == (plain{}) {
		v = plain(DefaultMySQLMigrationPolicy())
	}
	if node.Decode(&v) != nil || MySQLMigrationPolicy(v).Validate() != nil {
		return bad
	}
	*p = MySQLMigrationPolicy(v)
	return nil
}
func (c Config) DatabaseEngine() string {
	if c.Database.Engine == "" {
		return PostgreSQLDatabaseEngine
	}
	return c.Database.Engine
}
func (c Config) MySQLMigrationPolicy() MySQLMigrationPolicy {
	p := c.Database.MySQLMigrations
	if p == (MySQLMigrationPolicy{}) {
		return DefaultMySQLMigrationPolicy()
	}
	return p
}
func (c Config) ValidateDatabaseSelection() error {
	engine := c.DatabaseEngine()
	if engine != PostgreSQLDatabaseEngine && engine != MySQLDatabaseEngine && engine != DamengDatabaseEngine {
		return errors.New("CONFIG_DATABASE_SELECTION_INVALID")
	}
	if e := c.validateDamengServing(); e != nil {
		return e
	}
	if e := c.validateMySQLBackup(); e != nil {
		return e
	}
	if e := c.validateMySQLRuntimeAccess(); e != nil {
		return e
	}
	file := c.Database.MySQLConfigurationFile
	if engine != MySQLDatabaseEngine && file != "" || engine == MySQLDatabaseEngine && (file == "" || !filepath.IsAbs(file) || filepath.Clean(file) != file || file == string(filepath.Separator) || strings.ContainsAny(file, "\x00\r\n") || c.Database.DSNFile != "") {
		return errors.New("CONFIG_DATABASE_SELECTION_INVALID")
	}
	return c.MySQLMigrationPolicy().Validate()
}

// Reject explicit missing/empty selectors before omitted legacy defaults resolve.
func validateDatabaseSelectionYAML(raw []byte) error {
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
		for j := 0; j+1 < len(db.Content); j += 2 {
			key, value := db.Content[j].Value, db.Content[j+1]
			if key == "engine" && (value.Tag != "!!str" || value.Value != PostgreSQLDatabaseEngine && value.Value != MySQLDatabaseEngine && value.Value != DamengDatabaseEngine) || key == "mysql_configuration_file" && (value.Tag != "!!str" || value.Value == "") {
				return errors.New("CONFIG_DATABASE_SELECTION_INVALID")
			}
		}
	}
	return validateDamengServingYAML(raw)
}
func databaseSelectionEnvironment(key string) bool {
	if damengServingEnvironment(key) {
		return true
	}
	if mysqlBackupEnvironment(key) {
		return true
	}
	switch key {
	case databaseEngineEnvironment, mysqlConfigurationEnvironment, mysqlMigrationLockEnvironment, mysqlMigrationStepEnvironment, mysqlMigrationTotalEnvironment, mysqlMigrationCleanupEnvironment, mysqlMigrationMetadataEnvironment, mysqlRuntimeAccountHostEnvironment, mysqlRuntimeIntentFileEnvironment:
		return true
	}
	return false
}
func (c *Config) applyDatabaseSelectionEnvironment(key, value string) error {
	if damengServingEnvironment(key) {
		return c.applyDamengServingEnvironment(key, value)
	}
	if c.DatabaseEngine() == DamengDatabaseEngine && key != databaseEngineEnvironment {
		return errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	}
	if mysqlBackupEnvironment(key) {
		return c.applyMySQLBackupEnvironment(key, value)
	}
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if value == "" {
		return bad
	}
	if key == mysqlRuntimeAccountHostEnvironment || key == mysqlRuntimeIntentFileEnvironment {
		return c.applyMySQLRuntimeAccessEnvironment(key, value)
	}
	switch key {
	case databaseEngineEnvironment:
		if value != PostgreSQLDatabaseEngine && value != MySQLDatabaseEngine && value != DamengDatabaseEngine {
			return bad
		}
		if (value == DamengDatabaseEngine || c.DatabaseEngine() == DamengDatabaseEngine) && value != c.DatabaseEngine() {
			return bad
		}
		c.Database.Engine = value
		return nil
	case mysqlConfigurationEnvironment:
		if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return bad
		}
		c.Database.MySQLConfigurationFile = value
		return nil
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return bad
		}
	}
	v, e := strconv.Atoi(value)
	if e != nil {
		return bad
	}
	p := c.MySQLMigrationPolicy()
	switch key {
	case mysqlMigrationLockEnvironment:
		p.LockWaitSeconds = v
	case mysqlMigrationStepEnvironment:
		p.StepSeconds = v
	case mysqlMigrationTotalEnvironment:
		p.TotalSeconds = v
	case mysqlMigrationCleanupEnvironment:
		p.CleanupSeconds = v
	case mysqlMigrationMetadataEnvironment:
		p.MaximumMetadataBytes = v
	default:
		return bad
	}
	c.Database.MySQLMigrations = p
	return nil
}
