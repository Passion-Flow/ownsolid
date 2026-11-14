package config

import (
	"errors"
	"reflect"

	"go.yaml.in/yaml/v3"
)

const DefaultDatabaseLockTimeoutMilliseconds = 3000
const MaximumDatabaseLockTimeoutMilliseconds = 30000
const databaseLockEnvironment = "PROJECT_ONE_DATABASE_LOCK_TIMEOUT_MILLISECONDS"

// A nil optional field represents omission in trusted typed snapshots. External
// nulls are rejected before decoding; they must not select an implicit default.
func (c Config) DatabaseLockTimeoutMilliseconds() int {
	if c.Database.LockTimeoutMilliseconds == nil {
		return DefaultDatabaseLockTimeoutMilliseconds
	}
	return *c.Database.LockTimeoutMilliseconds
}

func (c Config) ValidateDatabaseLockTimeout() error {
	value := c.DatabaseLockTimeoutMilliseconds()
	if value < 1 || value > MaximumDatabaseLockTimeoutMilliseconds || value >= c.Database.StatementTimeoutSeconds*1000 {
		return errors.New("CONFIG_DATABASE_LOCK_TIMEOUT_INVALID")
	}
	return nil
}

func validateDatabaseYAML(raw []byte) error {
	bad := errors.New("CONFIG_DATABASE_INVALID")
	var node yaml.Node
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return bad
	}
	root := node.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "database" && strictKnowledgeYAML(root.Content[i+1], reflect.TypeOf(Config{}.Database)) != nil {
			return bad
		}
	}
	return nil
}
