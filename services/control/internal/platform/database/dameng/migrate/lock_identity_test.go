package migrate

import "testing"

func TestPublicSchemaLockIdentityRetainsOriginalAlgorithm(t *testing.T) {
	for _, schema := range []string{"P1_OWNER", "PRIVATE_SCHEMA_2"} {
		id, err := SchemaLockID(schema)
		if err != nil || id != lockID(schema) {
			t.Fatal("maintenance and migration lock identities diverged", err)
		}
	}
	for _, schema := range []string{"", "sys", "SYSDBA", "SYS_CUSTOM", "LOWERcase", "P1;SELECT"} {
		if _, err := SchemaLockID(schema); err == nil {
			t.Fatal("invalid schema lock identity accepted")
		}
	}
}
