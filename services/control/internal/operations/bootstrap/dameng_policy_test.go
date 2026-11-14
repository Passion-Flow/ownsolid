package bootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestDamengPolicyProtectedMappingAndNativeProjection(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture")
	}
	runtime := config.DefaultRuntimePolicy()
	runtime.Identity.HashWorkers = 2
	p := DamengPolicy{FormatVersion: 1, Runtime: &runtime, PasswordBlocklistFile: filepath.Join(root, "blocklist"), LicenseTrustFile: filepath.Join(root, "trust.json")}
	write := func(name string, v any) string {
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, name)
		if os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("fixture")
		}
		if name != "bad.json" {
			var decoded DamengPolicy
			if err := decodeDamengPolicy(raw, &decoded); err != nil {
				t.Fatalf("protected policy readback (%d bytes): %v", len(raw), err)
			}
			if err := decoded.Runtime.Validate(); err != nil {
				t.Fatal("runtime policy fixture", err)
			}
		}
		return path
	}
	base := write("base.json", p)
	runtime.HTTP.RequestSeconds++
	over := write("override.json", p)
	env := filepath.Join(root, "operator.env")
	if os.WriteFile(env, []byte("PROJECT_ONE_DAMENG_OPERATOR_POLICY_FILE="+over+"\n"), 0600) != nil {
		t.Fatal("fixture")
	}
	actual, e := LoadDamengPolicy(base, env)
	if e != nil {
		t.Fatal(e)
	}
	c, e := actual.Configuration()
	if e != nil || c.Policy().Identity.HashWorkers != 2 || c.Policy().HTTP.RequestSeconds != runtime.HTTP.RequestSeconds || c.Security.PasswordBlocklistFile != p.PasswordBlocklistFile || c.Database.Engine != "" || c.Database.DSNFile != "" || c.Database.MySQLConfigurationFile != "" {
		t.Fatal("native policy projection changed database selection", e)
	}
	for _, bad := range []string{"PROJECT_ONE_DAMENG_OPERATOR_POLICY_FILE=false\n", "PROJECT_ONE_DAMENG_OPERATOR_POLICY_FILE=\n", "UNKNOWN=" + over + "\n", "PROJECT_ONE_DAMENG_OPERATOR_POLICY_FILE=" + over + "\nPROJECT_ONE_DAMENG_OPERATOR_POLICY_FILE=" + over + "\n"} {
		if os.WriteFile(env, []byte(bad), 0600) != nil {
			t.Fatal("fixture")
		}
		if _, e = LoadDamengPolicy(base, env); e == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
	for _, bad := range []any{map[string]any{"format_version": 1, "runtime": false}, map[string]any{"format_version": 1, "runtime": nil}, map[string]any{"format_version": 1, "runtime": runtime, "password_blocklist_file": p.PasswordBlocklistFile, "unknown": true}} {
		if _, e = LoadDamengPolicy(write("bad.json", bad), ""); e == nil {
			t.Fatal("invalid explicit policy accepted")
		}
	}
}

func TestDamengPolicyExactRuntimeJSON(t *testing.T) {
	p := DamengPolicyTemplate()
	p.PasswordBlocklistFile = "/synthetic/blocklist"
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string][]byte{
		"case alias":              bytes.Replace(raw, []byte(`"HashWorkers":2`), []byte(`"hashworkers":2`), 1),
		"duplicate":               bytes.Replace(raw, []byte(`"HashWorkers":2`), []byte(`"HashWorkers":2,"HashWorkers":2`), 1),
		"missing nested":          bytes.Replace(raw, []byte(`"HashWorkers":2,`), nil, 1),
		"null nested":             bytes.Replace(raw, []byte(`"HashWorkers":2`), []byte(`"HashWorkers":null`), 1),
		"boolean number":          bytes.Replace(raw, []byte(`"HashWorkers":2`), []byte(`"HashWorkers":false`), 1),
		"unknown nested":          bytes.Replace(raw, []byte(`"HashWorkers":2`), []byte(`"HashWorkers":2,"unknown":1`), 1),
		"missing empty reference": bytes.Replace(raw, []byte(`,"license_trust_file":""`), nil, 1),
		"trailing object":         append(append([]byte(nil), raw...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(raw, changed) {
				t.Fatal("negative fixture did not change")
			}
			var actual DamengPolicy
			if decodeDamengPolicy(changed, &actual) == nil {
				t.Fatal("ambiguous or incomplete policy accepted")
			}
		})
	}
}
