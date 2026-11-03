package databaseaccess

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func writeRuntimeConfiguration(t *testing.T, root, dsn string) string {
	t.Helper()
	var c config.Config
	c.Server.ListenAddress = "127.0.0.1:18443"
	c.Server.PortalOrigin = "https://portal.example.invalid"
	c.Server.AdminOrigin = "https://admin.example.invalid"
	c.Database.DSNFile = dsn
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.FromAddress = "platform@example.invalid"
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	b, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "runtime.yaml")
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestRuntimeInputExplicitURIAndAmbientDiscoveryRefusal(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	dsn := filepath.Join(root, "runtime-dsn")
	configuration := writeRuntimeConfiguration(t, root, dsn)
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:15433", Path: "/synthetic_test", User: url.UserPassword("synthetic_runtime", password), RawQuery: "sslmode=disable"}
	write := func(uri string) {
		t.Helper()
		if e := os.WriteFile(dsn, []byte(uri), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(u.String())
	if _, e = LoadRuntime(configuration); e != nil {
		t.Fatal("explicit URI", e)
	}
	for _, query := range []string{"", "sslmode=prefer", "sslmode=disable&service=private", "sslmode=disable&host=foreign.invalid", "sslmode=disable&sslmode=disable", "sslmode=verify-full", "sslmode=disable&options=-c%20role%3Dother"} {
		u.RawQuery = query
		write(u.String())
		if _, e = LoadRuntime(configuration); e == nil || strings.Contains(e.Error(), password) {
			t.Fatal("unsafe URI or secret diagnostic", query, e)
		}
	}
	u.RawQuery = "sslmode=disable"
	write(u.String())
	t.Setenv("PGSERVICE", "synthetic-private-service")
	if _, e = LoadRuntime(configuration); e == nil || e.Error() != "RUNTIME_ACCESS_AMBIENT_DRIVER_CONFIGURATION_REFUSED" {
		t.Fatal("implicit service discovery accepted", e)
	}
}

func TestRuntimeInputUsesOriginalProtectedExplicitEnvironment(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	dsn := filepath.Join(root, "runtime-dsn")
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:44801", Path: "/synthetic_test", User: url.UserPassword("synthetic_runtime", password), RawQuery: "sslmode=disable"}
	if e = os.WriteFile(dsn, []byte(u.String()), 0600); e != nil {
		t.Fatal(e)
	}
	file := writeRuntimeConfiguration(t, root, dsn)
	legacy, e := LoadRuntime(file)
	if e != nil {
		t.Fatal(e)
	}
	empty, e := LoadRuntimeWithEnvironment(file, "")
	if e != nil || empty.configuration.Server.Profile() != legacy.configuration.Server.Profile() || empty.connection.MaxConns != legacy.connection.MaxConns {
		t.Fatal("legacy empty environment changed", e)
	}
	environment := filepath.Join(root, "runtime.env")
	write := func(body string) {
		t.Helper()
		if e := os.WriteFile(environment, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("PROJECT_ONE_APPLICATION_PROFILE=account\nPROJECT_ONE_DATABASE_LOCK_TIMEOUT_MILLISECONDS=1234\nPROJECT_ONE_MODEL_CACHE_ENABLED=false\n")
	effective, e := LoadRuntimeWithEnvironment(file, environment)
	if e != nil || !effective.configuration.AccountOnly() || effective.configuration.DatabaseLockTimeoutMilliseconds() != 1234 || effective.configuration.ModelCache.Enabled || effective.connection.MaxConns != legacy.connection.MaxConns {
		t.Fatal("effective policy/principal boundary", e)
	}
	expected, e := config.LoadProtectedWithEnvironment(file, environment)
	if e != nil || expected.Server.Profile() != effective.configuration.Server.Profile() || expected.DatabaseLockTimeoutMilliseconds() != effective.configuration.DatabaseLockTimeoutMilliseconds() {
		t.Fatal("configuration authorities disagree", e)
	}
	for _, body := range []string{"PROJECT_ONE_APPLICATION_PROFILE=unknown\n", "PROJECT_ONE_MODEL_CACHE_ENABLED=FALSE\n", "PROJECT_ONE_DATABASE_DSN_FILE=/unsupported\n", "PROJECT_ONE_APPLICATION_PROFILE=account\nPROJECT_ONE_APPLICATION_PROFILE=platform\n"} {
		write(body)
		if _, e = LoadRuntimeWithEnvironment(file, environment); e == nil || strings.Contains(e.Error(), password) {
			t.Fatal("invalid/duplicate/unsupported overlay admitted or secret exposed", e)
		}
	}
	write("PROJECT_ONE_APPLICATION_PROFILE=account\n")
	if e = os.Chmod(environment, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadRuntimeWithEnvironment(file, environment); e == nil {
		t.Fatal("unprotected env admitted")
	}
}
