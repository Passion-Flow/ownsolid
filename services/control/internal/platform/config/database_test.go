package config

import (
	"strings"
	"testing"
)

func databaseLockInput(t *testing.T, value string) []byte {
	t.Helper()
	return []byte(strings.Replace(string(workspaceConfigInput(t)), "database:\n", "database:\n    lock_timeout_milliseconds: "+value+"\n", 1))
}

func TestDatabaseLockPolicyDefaultsAndExplicitPrecedence(t *testing.T) {
	raw := workspaceConfigInput(t)
	t.Setenv(databaseLockEnvironment, "999")
	c, e := Decode(raw)
	if e != nil || c.DatabaseLockTimeoutMilliseconds() != DefaultDatabaseLockTimeoutMilliseconds {
		t.Fatal("owned omission default and no ambient override", e)
	}
	raw = databaseLockInput(t, "275")
	c, e = Decode(raw)
	if e != nil || c.DatabaseLockTimeoutMilliseconds() != 275 {
		t.Fatal("explicit YAML", e)
	}
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, databaseLockEnvironment+"='175'\n"+workspaceCountEnvironment+"=7\n"))
	if e != nil || c.DatabaseLockTimeoutMilliseconds() != 175 || c.Policy().Workspaces.MaximumCount != 7 {
		t.Fatal("same protected file independent owned settings", e)
	}
	for _, value := range []int{1, 29999} {
		c := validConfig()
		c.Database.StatementTimeoutSeconds = 30
		c.Database.LockTimeoutMilliseconds = &value
		if c.Validate() != nil || c.DatabaseLockTimeoutMilliseconds() != value {
			t.Fatal("effective inclusive boundaries")
		}
	}
}

func TestDatabaseLockPolicyInvalidYAMLAndSafeErrors(t *testing.T) {
	override := workspaceEnvironmentFixture(t, databaseLockEnvironment+"=175\n")
	for _, value := range []string{"null", "false", "true", "'275'", "0", "-1", "30001", "5000", "6000", "1.5", "99999999999999999999999999", "&lock 175\n    max_idle_connections: *lock", "275\n    lock_timeout_milliseconds: 175", "275\n    unexpected: sensitive-value"} {
		if _, e := decodeWithEnvironment(databaseLockInput(t, value), override); e == nil || strings.Contains(e.Error(), "sensitive-value") {
			t.Fatal("invalid YAML hidden by override or diagnostic leaked")
		}
	}
	for _, value := range []int{0, -1, 30000, 30001, 5000} {
		c := validConfig()
		c.Database.LockTimeoutMilliseconds = &value
		if e := c.Validate(); e == nil || e.Error() != "CONFIG_DATABASE_LOCK_TIMEOUT_INVALID" {
			t.Fatal("invalid typed setting", e)
		}
	}
}

func TestDatabaseLockPolicyInvalidEnvironmentAndSafeErrors(t *testing.T) {
	for _, value := range []string{"", "false", "-1", "0", "30001", "5000", "1.5", "9999999999999999999999999", "$(touch sensitive-value)", "\"175\" trailing", "175\n" + databaseLockEnvironment + "=275"} {
		if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, databaseLockEnvironment+"="+value+"\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
			t.Fatal("invalid environment must fail safely", e)
		}
	}
	c, e := decodeWithEnvironment(databaseLockInput(t, "275"), workspaceEnvironmentFixture(t, "# keep YAML\n"))
	if e != nil || c.DatabaseLockTimeoutMilliseconds() != 275 {
		t.Fatal("missing optional key must preserve YAML", e)
	}
}
