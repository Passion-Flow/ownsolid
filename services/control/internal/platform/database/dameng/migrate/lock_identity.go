package migrate

import "strings"

// SchemaLockID exposes the same source-bound installation lock identity used by
// Run. It acquires no lock and grants no migration or maintenance authority.
func SchemaLockID(schema string) (int64, error) {
	if !identifier.MatchString(schema) || strings.HasPrefix(schema, "SYS") {
		return 0, fail("DAMENG_SCHEMA_CONFIGURATION_INVALID")
	}
	return lockID(schema), nil
}
