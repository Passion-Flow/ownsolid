package config

import (
	"os"
	"testing"
)

func TestDistributionProtectedEnvironmentTypedBounds(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/gateway.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_DISTRIBUTION_KEY_TTL_SECONDS=900\nPROJECT_ONE_DISTRIBUTION_KEY_MAX_ACTIVE=3\nPROJECT_ONE_DISTRIBUTION_MAX_CONCURRENT_REQUESTS=2\nPROJECT_ONE_DISTRIBUTION_REQUEST_SECONDS=30\n"))
	if e != nil || c.Policy().Distribution.KeyTTLSeconds != 900 || c.Policy().Distribution.KeyMaxActive != 3 || c.Policy().Distribution.MaxConcurrentRequests != 2 || c.Policy().Distribution.RequestSeconds != 30 {
		t.Fatal(e)
	}
	for _, env := range []string{"PROJECT_ONE_DISTRIBUTION_KEY_TTL_SECONDS=false\n", "PROJECT_ONE_DISTRIBUTION_REQUEST_SECONDS=\n", "PROJECT_ONE_DISTRIBUTION_MAX_CONCURRENT_REQUESTS=5\n", "PROJECT_ONE_DISTRIBUTION_CONTEXT_TOKENS=2049\n", "PROJECT_ONE_DISTRIBUTION_KEY_TTL_SECONDS=900\nPROJECT_ONE_DISTRIBUTION_KEY_TTL_SECONDS=901\n", "PROJECT_ONE_DISTRIBUTION_UNKNOWN=1\n", "PROJECT_ONE_DISTRIBUTION_KEY_MAX_TTL_SECONDS=300\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env)); e == nil {
			t.Fatal("invalid policy accepted", env)
		}
	}
}
