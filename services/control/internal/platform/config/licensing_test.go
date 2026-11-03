package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func licenseConfigFixture(t *testing.T) (Config, string, wire.Trust) {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture")
	}
	_, trust, e := wire.GenerateKey("synthetic-config-trust")
	if e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(root, "license-trust.json")
	raw, _ := json.Marshal(trust)
	if os.WriteFile(name, raw, 0600) != nil {
		t.Fatal("protected public fixture")
	}
	c := validConfig()
	c.Licensing = &LicensingConfiguration{TrustFile: name}
	return c, root, trust
}
func decodeLicenseConfig(t *testing.T, c Config) (Config, error) {
	t.Helper()
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return Decode(raw)
}

func TestLicenseConfigurationOmittedOperatorsAndExplicitTrustBoundary(t *testing.T) {
	legacy := validConfig()
	raw, e := json.Marshal(legacy)
	if e != nil || strings.Contains(string(raw), `"Licensing"`) {
		t.Fatal("absent legacy metadata changed")
	}
	raw, e = json.Marshal(legacy.Policy())
	if e != nil || strings.Contains(string(raw), `"Licensing"`) {
		t.Fatal("absent legacy runtime metadata changed")
	}
	if legacy.LicensePolicy() != wire.DefaultPolicy() {
		t.Fatal("single owned policy defaults")
	}
	if _, e = legacy.LicenseTrust(); e == nil || e.Error() != "CONFIG_LICENSING_TRUST_REQUIRED" {
		t.Fatal("omitted operator config granted runtime trust", e)
	}
	c, _, trust := licenseConfigFixture(t)
	decoded, e := decodeLicenseConfig(t, c)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := decoded.LicenseTrust()
	if e != nil || actual != trust {
		t.Fatal("actual protected public trust", actual, e)
	}
	policy := LicensePolicy{MaximumImports: 7, RollbackToleranceSeconds: 0}
	runtime := c.Policy()
	runtime.Licensing = &policy
	c.Runtime = &runtime
	decoded, e = decodeLicenseConfig(t, c)
	if e != nil || decoded.LicensePolicy() != (wire.Policy{MaximumImports: 7, RollbackToleranceSeconds: 0}) {
		t.Fatal("nondefault and explicit zero", e)
	}
}

func TestLicenseConfigurationExplicitEnvironmentPrecedenceAndNoAmbientFallback(t *testing.T) {
	c, root, trust := licenseConfigFixture(t)
	environment := filepath.Join(root, "license.env")
	c.Licensing.EnvironmentFile = environment
	text := "# component owned, no shell\nPROJECT_ONE_LICENSE_TRUST_FILE='" + c.Licensing.TrustFile + "'\nPROJECT_ONE_LICENSE_MAXIMUM_IMPORTS=3\nPROJECT_ONE_LICENSE_ROLLBACK_TOLERANCE_SECONDS=0\n"
	if os.WriteFile(environment, []byte(text), 0600) != nil {
		t.Fatal("environment fixture")
	}
	policy := LicensePolicy{MaximumImports: 10, RollbackToleranceSeconds: 5}
	runtime := c.Policy()
	runtime.Licensing = &policy
	c.Runtime = &runtime
	t.Setenv("PROJECT_ONE_LICENSE_MAXIMUM_IMPORTS", "999")
	decoded, e := decodeLicenseConfig(t, c)
	if e != nil || decoded.LicensePolicy() != (wire.Policy{MaximumImports: 3, RollbackToleranceSeconds: 0}) {
		t.Fatal("protected environment precedence", e)
	}
	if actual, e := decoded.LicenseTrust(); e != nil || actual != trust {
		t.Fatal("environment trust reference", e)
	}
	for _, bad := range []string{"UNKNOWN_LICENSE_KEY=1", "PROJECT_ONE_LICENSE_MAXIMUM_IMPORTS=0", "PROJECT_ONE_LICENSE_MAXIMUM_IMPORTS=false", "PROJECT_ONE_LICENSE_MAXIMUM_IMPORTS=01", "PROJECT_ONE_LICENSE_MAXIMUM_IMPORTS=1\nPROJECT_ONE_LICENSE_MAXIMUM_IMPORTS=2", "PROJECT_ONE_LICENSE_TRUST_FILE=''", "PROJECT_ONE_LICENSE_TRUST_FILE='$SECRET'", "PROJECT_ONE_LICENSE_TRUST_FILE=relative", "PROJECT_ONE_LICENSE_ROLLBACK_TOLERANCE_SECONDS=-1", "PROJECT_ONE_LICENSE_ROLLBACK_TOLERANCE_SECONDS=301"} {
		if os.WriteFile(environment, []byte(bad), 0600) != nil {
			t.Fatal("bad environment fixture")
		}
		if _, e := decodeLicenseConfig(t, c); e == nil || strings.Contains(e.Error(), "$SECRET") || strings.Contains(e.Error(), root) {
			t.Fatal("invalid environment accepted or diagnostic leaked", e)
		}
	}
}

func TestLicenseConfigurationStrictYAMLPolicyNullAliasBooleanAndPaths(t *testing.T) {
	c, _, _ := licenseConfigFixture(t)
	c.Licensing = nil
	base, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	if yaml.Unmarshal(base, &fields) != nil {
		t.Fatal("fixture map")
	}
	delete(fields, "runtime")
	base, e = yaml.Marshal(fields)
	if e != nil {
		t.Fatal(e)
	}
	for _, extra := range []string{"licensing: false\n", "licensing: null\n", "licensing: {trust_file: false}\n", "licensing: {enabled: false}\n", "licensing: {trust_file: relative}\n", "licensing: {trust_file: /first, trust_file: /second}\n", "licensing: {trust_file: &path /first, environment_file: *path}\n", "runtime: {licensing: {maximum_imports: 0}}\n", "runtime: {licensing: {maximum_imports: null}}\n", "runtime: {licensing: {maximum_imports: '2'}}\n", "runtime: {licensing: {rollback_tolerance_seconds: false}}\n", "runtime: {licensing: {unknown: 1}}\n"} {
		if _, e := Decode(append(append([]byte{}, base...), []byte(extra)...)); e == nil {
			t.Fatal("invalid license YAML accepted", extra)
		}
	}
}

func TestLicenseConfigurationPrivateFileModesLinksAndPrivateSeedRefusal(t *testing.T) {
	c, root, _ := licenseConfigFixture(t)
	original := c.Licensing.TrustFile
	if os.Chmod(original, 0644) != nil {
		t.Fatal("public fixture mode")
	}
	if _, e := c.LicenseTrust(); e == nil {
		t.Fatal("public trust mode accepted")
	}
	if os.Chmod(original, 0600) != nil {
		t.Fatal("restore fixture mode")
	}
	for _, kind := range []string{"symlink", "hardlink"} {
		path := filepath.Join(root, kind)
		if kind == "symlink" {
			if os.Symlink(original, path) != nil {
				t.Fatal("link fixture")
			}
		} else if os.Link(original, path) != nil {
			t.Fatal("link fixture")
		}
		c.Licensing.TrustFile = path
		if _, e := c.LicenseTrust(); e == nil {
			t.Fatal("untrusted link accepted")
		}
		if os.Remove(path) != nil {
			t.Fatal("remove exact disposable fixture link")
		}
	}
	private, _, e := wire.GenerateKey("synthetic-private-rejection")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "private-key.json")
	raw, _ := json.Marshal(private)
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("synthetic seed fixture")
	}
	c.Licensing.TrustFile = path
	if _, e = c.LicenseTrust(); e == nil || e.Error() != "CONFIG_LICENSING_TRUST_INVALID" || strings.Contains(e.Error(), private.Seed) {
		t.Fatal("private seed accepted/exposed as runtime trust", e)
	}
}
