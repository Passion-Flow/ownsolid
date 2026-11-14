package postgresbackup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestSeparatedKubernetesProjectionSatisfiesNativeConfigurationClosure(t *testing.T) {
	f := newRestoreInputFixture(t)
	oldRoot := f.request.ConfigurationRoot
	private := filepath.Join(filepath.Dir(oldRoot), "kubernetes-private")
	if e := os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	configuration, credentials := filepath.Join(private, "configuration"), filepath.Join(private, "credentials")
	if e := os.Rename(oldRoot, configuration); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(credentials, 0700); e != nil {
		t.Fatal(e)
	}
	runtimeDSN := filepath.Join(credentials, "database-dsn")
	if e := os.Rename(f.runtime.Database.DSNFile, runtimeDSN); e != nil {
		t.Fatal(e)
	}
	rebase := func(c config.Config) config.Config {
		t.Helper()
		raw, e := yaml.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		c, e = config.Decode([]byte(strings.ReplaceAll(string(raw), oldRoot, configuration)))
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	operator, runtime := rebase(f.operator), rebase(f.runtime)
	runtime.Database.DSNFile = runtimeDSN
	r := f.manifest.Request
	r.ConfigurationRoot = configuration
	r.RuntimeConfigurationFile = filepath.Join(configuration, "control.yaml")
	if e := os.Rename(filepath.Join(configuration, filepath.Base(f.request.RuntimeConfigurationFile)), r.RuntimeConfigurationFile); e != nil {
		t.Fatal(e)
	}
	for path, c := range map[string]config.Config{f.operatorPath: operator, r.RuntimeConfigurationFile: runtime} {
		raw, e := yaml.Marshal(c)
		if e != nil || os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("complete private configuration")
		}
	}
	var e error
	operator, e = config.LoadProtected(f.operatorPath)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e = config.LoadProtected(r.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	effective, e := configurationSnapshot(operator, runtime, f.operatorPath, r)
	if e != nil || len(effective) == 0 || !strings.Contains(string(effective), runtimeDSN) {
		t.Fatal("native owner accepts complete nested configuration with sibling credential reference; no SQL required", e)
	}
	dsn, e := os.ReadFile(runtimeDSN)
	if e != nil || strings.Contains(string(effective), string(dsn)) {
		t.Fatal("effective configuration cannot contain DSN credential bytes", e)
	}
	bad := runtime
	bad.Database.DSNFile = filepath.Join(configuration, "database-dsn")
	if e = os.WriteFile(bad.Database.DSNFile, dsn, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = configurationSnapshot(operator, bad, f.operatorPath, r); e == nil || e.Error() != "POSTGRES_BACKUP_CONFIGURATION_CLOSURE_REQUIRED" {
		t.Fatal("credential inside backed-up root must still refuse", e)
	}
}
