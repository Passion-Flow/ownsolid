package config

import "testing"

func TestSelectedPolicyEnvironmentComposesIndependentOwners(t *testing.T) {
	c := defaultConfiguration()
	input := "PROJECT_ONE_DATABASE_ENGINE=postgresql\n" +
		"PROJECT_ONE_MONGODB_CONFIG_FILE=/run/projectone/private/mongodb.json\n" +
		"PROJECT_ONE_AWS_ENABLED=false\nPROJECT_ONE_AWS_PAGE_SIZE=9\n" +
		"PROJECT_ONE_MODEL_CACHE_ENABLED=false\nPROJECT_ONE_MODEL_CACHE_TLS=false\n" +
		"PROJECT_ONE_MODEL_CACHE_TTL_SECONDS=17\nPROJECT_ONE_HTTP_REQUEST_SECONDS=31\n"
	if e := c.resolveWorkspaceEnvironment(workspaceEnvironmentFixture(t, input)); e != nil {
		t.Fatal("independent owner inputs rejected", e)
	}
	if c.DatabaseEngine() != PostgreSQLDatabaseEngine || c.MongoDBConfigFile != "/run/projectone/private/mongodb.json" || c.AWS.Enabled || c.AWS.PageSize != 9 || c.ModelCache.Enabled || c.ModelCache.TLS || c.ModelCache.TTLSeconds != 17 || c.Policy().HTTP.RequestSeconds != 31 {
		t.Fatal("configuration owner lost its explicit value")
	}
}
