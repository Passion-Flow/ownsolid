package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() Config {
	var c Config
	c.Server.ListenAddress = "127.0.0.1:8443"
	c.Server.PortalOrigin = "https://localhost:8443"
	c.Server.AdminOrigin = "https://127.0.0.1:8443"
	c.Database.MaxOpenConnections = 10
	c.Database.MaxIdleConnections = 5
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "localhost"
	c.SMTP.Port = 465
	c.SMTP.FromAddress = "platform@example.invalid"
	c.SMTP.TLSMode = "implicit_tls"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	return c
}
func TestM01ConfigurationFailsClosed(t *testing.T) {
	c := validConfig()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.SMTP.TLSMode = "plaintext" }, func(c *Config) { c.Server.PortalOrigin = "http://localhost:8443" }, func(c *Config) { c.Server.AdminOrigin = c.Server.PortalOrigin }, func(c *Config) { c.Database.MaxOpenConnections = 999 }, func(c *Config) { c.SMTP.FromAddress = "injected\r\n@example.invalid" }} {
		bad := c
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	p := filepath.Join(t.TempDir(), "secret")
	if e := os.WriteFile(p, []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	// Creation permissions are filtered by umask; explicitly establish the unsafe fixture.
	if e := os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if info, e := os.Stat(p); e != nil || info.Mode().Perm() != 0644 {
		t.Fatal("public fixture mode was not established", e)
	}
	if _, e := Secret(p); e == nil {
		t.Fatal("world-readable secret accepted")
	}
	if e := os.Chmod(p, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Secret(p); e != nil {
		t.Fatal(e)
	}
	if _, e := c.TLSConfig(); e == nil {
		t.Fatal("missing TLS material accepted")
	}
}
