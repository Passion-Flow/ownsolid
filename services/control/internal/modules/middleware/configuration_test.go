package middleware

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedCompositionFilesAndExplicitOverrides(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private test directory required")
	}
	configuration, environment := filepath.Join(root, "configuration.json"), filepath.Join(root, "policy.env")
	raw, _ := json.Marshal(externalFixture())
	if os.WriteFile(configuration, raw, 0600) != nil || os.WriteFile(environment, []byte("PROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=17\n"), 0600) != nil {
		t.Fatal("private inputs required")
	}
	c, e := LoadFiles(configuration, environment)
	if e != nil || c.Policy.ProbeSeconds != 17 {
		t.Fatal("explicit protected composition lost override", e)
	}
	link := filepath.Join(root, "link.json")
	if os.Symlink(configuration, link) != nil {
		t.Fatal("test symlink required")
	}
	if out, e := LoadFiles(link, environment); e == nil || out.FormatVersion != 0 {
		t.Fatal("symlink composition input accepted")
	}
	if os.Chmod(environment, 0644) != nil {
		t.Fatal("test permission mutation required")
	}
	if out, e := LoadFiles(configuration, environment); e == nil || out.FormatVersion != 0 {
		t.Fatal("public policy input accepted")
	}
	if os.Chmod(environment, 0600) != nil || os.WriteFile(environment, []byte("PROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=false\n"), 0600) != nil {
		t.Fatal("test false input required")
	}
	if _, e := LoadFiles(configuration, environment); e == nil {
		t.Fatal("false numeric policy accepted")
	}
	c, e = LoadFiles(configuration, "")
	if e != nil || c.Policy.ProbeSeconds != DefaultPolicy().ProbeSeconds {
		t.Fatal("absent explicit override changed default", e)
	}
}

func externalFixture() Configuration {
	return Configuration{FormatVersion: 1, Policy: DefaultPolicy(), Profiles: []Profile{{Key: "external_readonly", Kind: CustomerExternal, Host: "127.0.0.1", Port: 5432, Database: "synthetic", User: "readonly", PasswordFile: "/private/operator/password", CAFile: "/private/operator/ca.pem", ExpectedMajor: 18}}}
}

func TestTypedMaintenanceNondefaultAndInvalidEnvironment(t *testing.T) {
	raw, _ := json.Marshal(externalFixture())
	c, e := Load(raw, []byte("PROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=17\nPROJECT_ONE_MIDDLEWARE_RENEW_SECONDS=20\nPROJECT_ONE_MIDDLEWARE_LEASE_SECONDS=75\nPROJECT_ONE_MIDDLEWARE_PAGE_SIZE=23\n"))
	if e != nil || c.Policy.ProbeSeconds != 17 || c.Policy.RenewSeconds != 20 || c.Policy.LeaseSeconds != 75 || c.Policy.PageSize != 23 {
		t.Fatal("explicit nondefault lost", e)
	}
	for _, env := range []string{"PROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=false", "PROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=0", "PROJECT_ONE_MIDDLEWARE_UNKNOWN=1", "PROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=10\nPROJECT_ONE_MIDDLEWARE_PROBE_SECONDS=11", "PROJECT_ONE_MIDDLEWARE_RENEW_SECONDS=120"} {
		if _, e = Load(raw, []byte(env)); e == nil {
			t.Fatal("invalid env accepted", env)
		}
	}
	if _, e = Load([]byte(strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"format_version":1`, 1)), nil); e == nil {
		t.Fatal("duplicate JSON accepted")
	}
	if _, e = Load([]byte(strings.Replace(string(raw), `"expected_major":18`, `"expected_major":false`, 1)), nil); e == nil {
		t.Fatal("false version accepted")
	}
}

func TestExternalProfileCannotAcquireMutationAuthority(t *testing.T) {
	c := externalFixture()
	p := c.Profiles[0]
	if p.Validate() != nil {
		t.Fatal("valid external profile")
	}
	p.OperatorConfigurationFile = "/private/operator/control.json"
	if e := p.Validate(); e == nil || e.Error() != "MIDDLEWARE_EXTERNAL_MUTATION_REFUSED" {
		t.Fatal("external mutation configuration accepted", e)
	}
	c.Profiles = append(c.Profiles, c.Profiles[0])
	if c.Validate() == nil {
		t.Fatal("duplicate profile accepted")
	}
	p = externalFixture().Profiles[0]
	p.Host = "host/path"
	if p.Validate() == nil {
		t.Fatal("path-like host accepted")
	}
	p = externalFixture().Profiles[0]
	p.Kind = platformOwned
	if p.Validate() == nil {
		t.Fatal("platform scope inferred from external profile")
	}
}

func TestPlatformOwnedProfilesAreNotSupported(t *testing.T) {
	p := externalFixture().Profiles[0]
	p.Kind = platformOwned
	p.OperatorConfigurationFile = "/private/operator/scope.json"
	p.BackupRequestFile = "/private/operator/backup.json"
	p.ArtifactRoot = "/private/artifacts"
	p.ExpectedTenantID = "11111111-1111-1111-1111-111111111111"
	p.ExpectedScopeID = "0123456789abcdef0123456789abcdef"
	if e := p.Validate(); e == nil || e.Error() != "MIDDLEWARE_PLATFORM_OWNED_NOT_SUPPORTED" {
		t.Fatal("platform-owned profile admitted", e)
	}
}
