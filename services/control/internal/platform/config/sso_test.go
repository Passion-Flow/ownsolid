package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestM11SSOOperatorTransportDestinationsAndRecoveryConfiguration(t *testing.T) {
	c := SSOConfiguration{Enabled: true, KeyringFile: "/synthetic/private/sso-keyring.json", Providers: []SSOProvider{{Origin: "https://gitea.example.invalid:9443", Issuer: "https://identity.example.invalid/issuer", DialAddresses: []string{"127.0.0.1", "::1"}, CAFile: "/synthetic/private/provider-ca.pem"}}}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, origin := range []string{"http://gitea.example.invalid", "https://user:password@gitea.example.invalid", "https://Gitea.example.invalid", "https://gitea.example.invalid/", "https://gitea.example.invalid?", "https://gitea.example.invalid:443", "https://gitea.example.invalid:09443", "https://gitea.example.invalid:", "https://*.example.invalid", "https://127.1", "https://2130706433", "https://0x7f000001", "https://[fe80::1%25en0]", "https://gitea.example.invalid/#fragment"} {
		t.Run(origin, func(t *testing.T) {
			bad := c
			bad.Providers = append([]SSOProvider(nil), c.Providers...)
			bad.Providers[0].Origin = origin
			if bad.Validate() == nil {
				t.Fatal("noncanonical or unsafe destination accepted")
			}
		})
	}
	for _, address := range []string{"0.0.0.0", "::", "224.0.0.1", "255.255.255.255", "169.254.169.254", "::ffff:127.0.0.1", "fe80::1%en0", "gitea.example.invalid"} {
		bad := c
		bad.Providers = append([]SSOProvider(nil), c.Providers...)
		bad.Providers[0].DialAddresses = []string{address}
		if bad.Validate() == nil {
			t.Fatalf("unapproved address form accepted: %s", address)
		}
	}
	for _, mutate := range []func(*SSOConfiguration){func(c *SSOConfiguration) { c.KeyringFile = "" }, func(c *SSOConfiguration) { c.KeyringFile = "relative" }, func(c *SSOConfiguration) { c.Providers = nil }, func(c *SSOConfiguration) { c.Providers = append(c.Providers, c.Providers[0]) }} {
		bad := c
		bad.Providers = append([]SSOProvider(nil), c.Providers...)
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid enabled policy accepted")
		}
	}
	c.Enabled = false
	// No file lookup occurs in disabled recovery-mode parsing, including missing old key/CA references.
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if e := (SSOConfiguration{}).Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestM11SSOPolicyDefaultsOverridesAndStrictYAMLBeforeMerging(t *testing.T) {
	p := DefaultSSOPolicy()
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	if e := yaml.Unmarshal([]byte("flow_seconds: 180\nconnect_seconds: 2\nresponse_bytes: 32768\n"), &p); e != nil || p.FlowSeconds != 180 || p.OperationSeconds != 10 || p.ConnectSeconds != 2 || p.ResponseBytes != 32768 {
		t.Fatal("nondefault override or default preservation", e)
	}
	for _, raw := range []string{"sso: null", "sso: {enabled: 1}", "sso: {enabled: true, unknown: false}", "sso: {providers: null}", "sso: {providers: [null]}", "sso: {providers: [{dial_addresses: [12]}]}", "runtime: {sso: null}", "runtime: {sso: {flow_seconds: 1.5}}", "runtime: {sso: {flow_seconds: null}}", "runtime: {sso: {flow_seconds: 180, flow_seconds: 300}}", "runtime: {sso: {unknown: 10}}", "sso: &s {enabled: false}\nruntime: {sso: *s}"} {
		if e := validateSSOYAML([]byte(raw)); e == nil {
			t.Fatalf("coercion/null/unknown/alias accepted: %s", raw)
		}
	}
	for _, mutate := range []func(*SSOPolicy){func(p *SSOPolicy) { p.FlowSeconds = 59 }, func(p *SSOPolicy) { p.MaxConcurrentExchanges = 17 }, func(p *SSOPolicy) { p.ConnectSeconds = 10; p.OperationSeconds = 2 }, func(p *SSOPolicy) { p.ResponseBytes = 1048576 }, func(p *SSOPolicy) { p.CleanupBatchSize = 0 }} {
		bad := p
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("unbounded SSO policy accepted")
		}
	}
	// Exercise the actual Config.Load path rather than only custom unmarshaling.
	base, e := yaml.Marshal(validConfig())
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "control.yaml")
	if e = os.WriteFile(file, base, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(file)
	if e != nil || loaded.SSO.Enabled || loaded.Policy().SSO.FlowSeconds != 300 {
		t.Fatal("backward-compatible real loader", e)
	}
	if e = os.WriteFile(file, append(base, []byte("\nsso: null\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(file); e == nil {
		t.Fatal("actual loader accepted duplicate/null SSO group")
	}
	var parsed map[string]any
	if e = yaml.Unmarshal(base, &parsed); e != nil {
		t.Fatal(e)
	}
	parsed["sso"] = nil
	invalid, e := yaml.Marshal(parsed)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, invalid, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(file); e == nil {
		t.Fatal("actual loader accepted a single null SSO group")
	}
	parsed["sso"] = map[string]any{"enabled": false}
	parsed["runtime"] = map[string]any{"sso": nil}
	invalid, e = yaml.Marshal(parsed)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, invalid, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(file); e == nil {
		t.Fatal("actual loader accepted a single null SSO policy")
	}
}
