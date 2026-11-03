package privateclusters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syntheticProfile() Profile {
	return Profile{ID: "synthetic", Name: "Synthetic config contract only", ServerOrigin: "https://127.0.0.1:6443", TLSServerName: "127.0.0.1", AllowedCIDRs: []string{"127.0.0.0/8"}, CAFile: "/synthetic/ca.pem", TokenFile: "/synthetic/token", ClusterUID: "11111111-1111-4111-8111-111111111111", Namespace: "projectone-test-synthetic", NamespaceUID: "22222222-2222-4222-8222-222222222222", ControlServiceAccountUID: "33333333-3333-4333-8333-333333333333", InstallationID: "44444444-4444-4444-8444-444444444444", WorkspaceID: "55555555-5555-4555-8555-555555555555", Profile: LocalARM64Profile, ImageRepository: "registry.example.invalid/probe", ImageProofFile: "/synthetic/proof.json"}
}
func syntheticConfig() Config {
	c := Defaults()
	c.Profiles = []Profile{syntheticProfile()}
	c.Verification.MountRoot = "/synthetic"
	c.Verification.TrustFile = "/synthetic/trust.json"
	return c
}

func TestPrivateClusterJSONRequiredDefaultsNullAndFalse(t *testing.T) {
	c := syntheticConfig()
	c.Policy.RequestSeconds, c.Policy.OperationSeconds = 2, 8
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := Decode(raw)
	if e != nil || actual.Policy.RequestSeconds != 2 || actual.Verification.AllowTestFixture {
		t.Fatal("nondefault policy or explicit false changed", e)
	}
	fields := func() map[string]json.RawMessage {
		var v map[string]json.RawMessage
		if e := json.Unmarshal(raw, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	refuse := func(v map[string]json.RawMessage) {
		t.Helper()
		body, _ := json.Marshal(v)
		if _, e := Decode(body); e == nil {
			t.Fatal("invalid or missing configuration field accepted")
		}
	}
	for _, name := range []string{"format_version", "profiles", "verification"} {
		v := fields()
		delete(v, name)
		refuse(v)
	}
	for _, name := range []string{"format_version", "profiles", "verification", "policy"} {
		for _, invalid := range []string{"null", "false", "\"false\""} {
			v := fields()
			v[name] = json.RawMessage(invalid)
			refuse(v)
		}
	}
	for _, name := range []string{"policy", "verification"} {
		var members map[string]json.RawMessage
		json.Unmarshal(fields()[name], &members)
		for key := range members {
			v := fields()
			var changed map[string]json.RawMessage
			json.Unmarshal(v[name], &changed)
			changed[key] = json.RawMessage("null")
			v[name], _ = json.Marshal(changed)
			refuse(v)
		}
	}
	for _, required := range []string{"mount_root", "trust_file"} {
		v := fields()
		var verification map[string]json.RawMessage
		json.Unmarshal(v["verification"], &verification)
		delete(verification, required)
		v["verification"], _ = json.Marshal(verification)
		refuse(v)
	}
	v := fields()
	delete(v, "policy")
	body, _ := json.Marshal(v)
	if actual, e := Decode(body); e != nil || actual.Policy != Defaults().Policy {
		t.Fatal("omitted optional policy defaults changed", e)
	}
}

func TestTypedProfileAndPolicyRefusals(t *testing.T) {
	c := syntheticConfig()
	c.Policy.RequestSeconds = 2
	c.Policy.OperationSeconds = 8
	c.Policy.CPUMilli = 250
	c.Policy.MemoryMiB = 64
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Profiles[0].ServerOrigin = "http://127.0.0.1" }, func(c *Config) { c.Profiles[0].Namespace = "production" }, func(c *Config) { c.Profiles[0].AllowedCIDRs = []string{"10.0.0.0/7"} }, func(c *Config) { c.Profiles[0].AllowedCIDRs = []string{"169.254.0.0/16"} }, func(c *Config) { c.Profiles[0].ServerOrigin = "https://127.0.0.1:" }, func(c *Config) { c.Profiles[0].Profile = "automatic" }, func(c *Config) { c.Policy.OperationSeconds = 1 }, func(c *Config) { c.Policy.RunAsUser = 0 }, func(c *Config) { c.Verification.AllowTestFixture = true }, func(c *Config) { c.Profiles[0].TokenFile = c.Profiles[0].CAFile }} {
		bad := syntheticConfig()
		mutate(&bad)
		if e := bad.Validate(); e == nil {
			t.Fatal("unsafe profile/policy accepted")
		}
	}
	c = syntheticConfig()
	c.Profiles[0].ServerOrigin = "https://[::1]:6443"
	c.Profiles[0].TLSServerName = "::1"
	c.Profiles[0].AllowedCIDRs = []string{"::1/128"}
	if e := c.Validate(); e != nil {
		t.Fatal("explicit IPv6 configuration", e)
	}
	if c.Verification.AllowTestFixture {
		t.Fatal("false manufacturer fixture switch became true")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, _ := json.Marshal(c)
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadEnvironment(map[string]string{ConfigEnvironment: path}); e != nil {
		t.Fatal(e)
	}
	for _, input := range []map[string]string{{}, {ConfigEnvironment: ""}, {ConfigEnvironment: path, "UNKNOWN": "false"}} {
		if _, e := LoadEnvironment(input); e == nil {
			t.Fatal("unknown or empty input accepted")
		}
	}
	for _, bad := range []string{strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"unknown":false`, 1), strings.Replace(string(raw), `"allow_test_fixture":false`, `"allow_test_fixture":"false"`, 1), strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"format_version":1`, 1)} {
		if e := os.WriteFile(path, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(path); e == nil {
			t.Fatal("unknown/type/duplicate input accepted")
		}
	}
}
func TestQuotaQuantityRejectsOverflow(t *testing.T) {
	for _, raw := range []string{"17179869184Gi", "999999999999999999999999Mi", "-1Mi", "NaN"} {
		if _, ok := quantity(raw, true); ok {
			t.Fatal("invalid quantity accepted")
		}
	}
	if v, ok := quantity("64Mi", true); !ok || v != 64<<20 {
		t.Fatal("valid quantity refused")
	}
}

func TestPrivateClusterConfigurationRejectsCaseAliases(t *testing.T) {
	c := syntheticConfig()
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2]string{{"format_version", "Format_version"}, {"policy", "Policy"}, {"request_seconds", "Request_seconds"}, {"profiles", "Profiles"}, {"server_origin", "Server_origin"}, {"namespace_uid", "Namespace_uid"}, {"verification", "Verification"}, {"trust_file", "Trust_file"}} {
		t.Run(pair[0], func(t *testing.T) {
			changed := strings.Replace(string(raw), `"`+pair[0]+`":`, `"`+pair[1]+`":`, 1)
			if changed == string(raw) {
				t.Fatal("test field absent")
			}
			if _, e := Decode([]byte(changed)); e == nil {
				t.Fatal("aliased protected field accepted")
			}
		})
	}
	changed := strings.Replace(string(raw), `"policy":{`, `"Policy":{"request_seconds":1},"policy":{`, 1)
	if _, e := Decode([]byte(changed)); e == nil {
		t.Fatal("canonical plus aliased policy accepted")
	}
}
