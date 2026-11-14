package config

import (
	"strings"
	"testing"
)

func TestModelCacheConfiguration(t *testing.T) {
	c := DefaultModelCacheConfiguration()
	if c.Validate() != nil || c.Enabled || !c.TLS {
		t.Fatal("safe defaults")
	}
	for _, field := range []string{"null", "{enabled: null}", "{enabled: 'false'}", "{ttl_seconds: false}", "{ttl_seconds: 0}", "{unknown: 1}", "{enabled: false, enabled: true}", "&cache {enabled: false}"} {
		if _, e := Decode(append(workspaceConfigInput(t), []byte("\nmodel_cache: "+field+"\n")...)); e == nil {
			t.Fatalf("invalid %s", field)
		}
	}
	for _, mutate := range []func(*ModelCacheConfiguration){func(c *ModelCacheConfiguration) { c.TTLSeconds = 0 }, func(c *ModelCacheConfiguration) { c.TimeoutMilliseconds = 1001 }, func(c *ModelCacheConfiguration) { c.MaximumBytes = 0 }, func(c *ModelCacheConfiguration) { c.MaximumConcurrent = 33 }, func(c *ModelCacheConfiguration) { c.Namespace = "foreign:*" }, func(c *ModelCacheConfiguration) { c.Address = "0.0.0.0:6379" }, func(c *ModelCacheConfiguration) { c.Address = "unbracketed::1:6379" }, func(c *ModelCacheConfiguration) { c.TLS = false; c.CAFile = "/private/ca" }, func(c *ModelCacheConfiguration) { c.Enabled = true }, func(c *ModelCacheConfiguration) { c.Username = "user" }} {
		v := c
		mutate(&v)
		if v.Validate() == nil {
			t.Fatal("invalid accepted")
		}
	}
	c.Enabled = true
	c.Address = "[::1]:6380"
	c.Username = "cache"
	c.PasswordFile = "/private/password"
	if c.Validate() != nil {
		t.Fatal("explicit IPv6/TLS")
	}
}
func TestModelCacheEnvironment(t *testing.T) {
	input := workspaceConfigInput(t)
	body := "PROJECT_ONE_MODEL_CACHE_ENABLED=true\nPROJECT_ONE_MODEL_CACHE_TLS=false\nPROJECT_ONE_MODEL_CACHE_ADDRESS=127.0.0.1:6387\nPROJECT_ONE_MODEL_CACHE_USERNAME=cache\nPROJECT_ONE_MODEL_CACHE_PASSWORD_FILE=/private/cache-password\nPROJECT_ONE_MODEL_CACHE_NAMESPACE=nondefault\nPROJECT_ONE_MODEL_CACHE_TTL_SECONDS=17\nPROJECT_ONE_MODEL_CACHE_TIMEOUT_MILLISECONDS=23\nPROJECT_ONE_MODEL_CACHE_MAXIMUM_BYTES=2048\nPROJECT_ONE_MODEL_CACHE_MAXIMUM_CONCURRENT=2\n"
	c, e := decodeWithEnvironment(input, workspaceEnvironmentFixture(t, body))
	if e != nil || !c.ModelCache.Enabled || c.ModelCache.TLS || c.ModelCache.TTLSeconds != 17 || c.ModelCache.MaximumConcurrent != 2 || c.ModelCache.Address != "127.0.0.1:6387" {
		t.Fatal("nondefault explicit env", e)
	}
	c, e = decodeWithEnvironment(input, workspaceEnvironmentFixture(t, strings.Replace(body, "ENABLED=true", "ENABLED=false", 1)))
	if e != nil || c.ModelCache.Enabled {
		t.Fatal("false truthiness", e)
	}
	t.Setenv("PROJECT_ONE_MODEL_CACHE_ENABLED", "true")
	c, e = decodeWithEnvironment(input, "")
	if e != nil || c.ModelCache.Enabled {
		t.Fatal("ambient env read", e)
	}
	for _, body := range []string{"ENABLED=FALSE", "ENABLED=1", "UNKNOWN=sensitive", "TTL_SECONDS=0", "MAXIMUM_BYTES=-2", "ENABLED=false\nPROJECT_ONE_MODEL_CACHE_ENABLED=true", "NAMESPACE=bad:*", "ADDRESS=host:0", "PASSWORD_FILE=relative"} {
		if _, e := decodeWithEnvironment(input, workspaceEnvironmentFixture(t, "PROJECT_ONE_MODEL_CACHE_"+body+"\n")); e == nil || strings.Contains(e.Error(), "sensitive") {
			t.Fatal("invalid env", e)
		}
	}
}
