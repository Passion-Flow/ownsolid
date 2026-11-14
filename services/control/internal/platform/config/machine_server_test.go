package config

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func machineTestConfiguration(mode TransportMode) Config {
	c := validConfig()
	c.Server = transportTestServer(mode)
	c.MachineServer = &MachineServerConfiguration{
		ListenAddress: "127.0.0.1:29443", Origin: "https://machine.example.invalid:29443",
		TLSCertificateFile: "/synthetic/machine-certificate.pem", TLSPrivateKeyFile: "/synthetic/machine-key.pem",
	}
	return c
}

func machineTestDecode(t *testing.T, c Config) Config {
	t.Helper()
	value, e := Decode(transportConfigYAML(t, c))
	if e != nil {
		t.Fatal(e)
	}
	return value
}

func machineTestSource(t *testing.T, relative string) []byte {
	t.Helper()
	root, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	for {
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if e == nil {
			return raw
		}
		if !os.IsNotExist(e) || filepath.Dir(root) == root {
			t.Fatal("declared source input unavailable", relative, e)
		}
		root = filepath.Dir(root)
	}
}

func TestMachineServerConfigurationStrictStructureAndIsolation(t *testing.T) {
	t.Run("schema_example_and_refusals", func(t *testing.T) {
		var definition, example any
		if e := json.Unmarshal(machineTestSource(t, "api/configuration/v1/control-machine-server/schema.json"), &definition); e != nil {
			t.Fatal(e)
		}
		exampleRaw := machineTestSource(t, "services/control/config/examples/control/machine-server.example.json")
		if e := json.Unmarshal(exampleRaw, &example); e != nil {
			t.Fatal(e)
		}
		compiler := jsonschema.NewCompiler()
		const identifier = "https://projectone.invalid/configuration/control-machine-server/v1"
		if e := compiler.AddResource(identifier, definition); e != nil {
			t.Fatal(e)
		}
		schema, e := compiler.Compile(identifier)
		if e != nil {
			t.Fatal("versioned schema compile", e)
		}
		if e = schema.Validate(example); e != nil {
			t.Fatal("versioned schema/example mismatch", e)
		}
		var selected MachineServerConfiguration
		if e = json.Unmarshal(exampleRaw, &selected); e != nil || selected.Validate() != nil {
			t.Fatal("actual runtime group rejected the source example", e)
		}
		c := machineTestConfiguration(TransportHTTP)
		c.MachineServer = &selected
		environment := workspaceEnvironmentFixture(t, string(machineTestSource(t, "services/control/config/examples/control/machine-server.env.example")))
		decoded, e := decodeWithEnvironment(transportConfigYAML(t, c), environment)
		if e != nil || decoded.MachineServer == nil || *decoded.MachineServer != selected {
			t.Fatal("commented environment example drifted from the source group", e)
		}
		fields := example.(map[string]any)
		for field := range fields {
			for _, variant := range []string{"missing", "null", "wrong_type"} {
				t.Run(field+"_"+variant, func(t *testing.T) {
					value := make(map[string]any, len(fields))
					for key, entry := range fields {
						value[key] = entry
					}
					switch variant {
					case "missing":
						delete(value, field)
					case "null":
						value[field] = nil
					case "wrong_type":
						value[field] = false
					}
					if schema.Validate(value) == nil {
						t.Fatal("invalid schema group accepted")
					}
				})
			}
		}
		for _, value := range []any{nil, map[string]any{}, map[string]any{"listen_address": selected.ListenAddress, "origin": selected.Origin, "tls_certificate_file": selected.TLSCertificateFile, "tls_private_key_file": selected.TLSPrivateKeyFile, "enabled": true}} {
			if schema.Validate(value) == nil {
				t.Fatal("null/empty/unknown schema group accepted")
			}
		}
	})
	t.Run("legacy_group_omitted", func(t *testing.T) {
		c := validConfig()
		decoded := machineTestDecode(t, c)
		if decoded.MachineServer != nil {
			t.Fatal("omission created a machine group")
		}
		if got, e := decoded.MachineTLSConfig(); e != nil || got != nil {
			t.Fatal("omission reread the browser TLS pair", e)
		}
	})
	for _, host := range []string{"machine.example.invalid", "localhost", "127.0.0.1", "192.0.2.20", "::1", "2001:db8::20"} {
		t.Run("canonical_"+host, func(t *testing.T) {
			c := machineTestConfiguration(TransportHTTP)
			c.MachineServer.ListenAddress = net.JoinHostPort(host, "29443")
			c.MachineServer.Origin = "https://" + net.JoinHostPort(host, "29443")
			decoded := machineTestDecode(t, c)
			if decoded.MachineServer == nil || *decoded.MachineServer != *c.MachineServer {
				t.Fatal("explicit machine identity was rewritten")
			}
			origin, e := decoded.MachineOrigin()
			if e != nil || origin != c.MachineServer.Origin {
				t.Fatal("selected exact machine origin changed", e)
			}
		})
	}
	for name, mutate := range map[string]func(*Config){
		"empty_listener":       func(c *Config) { c.MachineServer.ListenAddress = "" },
		"noncanonical_port":    func(c *Config) { c.MachineServer.ListenAddress = "127.0.0.1:029443" },
		"empty_bind":           func(c *Config) { c.MachineServer.ListenAddress = ":29443" },
		"plaintext_origin":     func(c *Config) { c.MachineServer.Origin = "http://machine.example.invalid:29443" },
		"uppercase_origin":     func(c *Config) { c.MachineServer.Origin = "https://MACHINE.example.invalid:29443" },
		"default_port_alias":   func(c *Config) { c.MachineServer.Origin = "https://machine.example.invalid:443" },
		"origin_path":          func(c *Config) { c.MachineServer.Origin += "/" },
		"origin_userinfo":      func(c *Config) { c.MachineServer.Origin = "https://actor:synthetic@machine.example.invalid" },
		"wildcard_origin":      func(c *Config) { c.MachineServer.Origin = "https://0.0.0.0:29443" },
		"ipv6_zone":            func(c *Config) { c.MachineServer.Origin = "https://[fe80::1%25en0]:29443" },
		"missing_key":          func(c *Config) { c.MachineServer.TLSPrivateKeyFile = "" },
		"aliased_tls_paths":    func(c *Config) { c.MachineServer.TLSPrivateKeyFile = c.MachineServer.TLSCertificateFile },
		"relative_certificate": func(c *Config) { c.MachineServer.TLSCertificateFile = "certificate.pem" },
		"unclean_key":          func(c *Config) { c.MachineServer.TLSPrivateKeyFile = "/synthetic/../key.pem" },
		"browser_listener":     func(c *Config) { c.MachineServer.ListenAddress = c.Server.ListenAddress },
		"portal_origin_alias":  func(c *Config) { c.MachineServer.Origin = c.Server.PortalOrigin },
		"admin_origin_alias":   func(c *Config) { c.MachineServer.Origin = c.Server.AdminOrigin },
	} {
		t.Run(name, func(t *testing.T) {
			c := machineTestConfiguration(TransportDirectHTTPS)
			mutate(&c)
			if e := c.Validate(); e == nil || e.Error() != "CONFIG_MACHINE_SERVER_INVALID" {
				t.Fatal("invalid machine structure accepted or error leaked", e)
			}
		})
	}
	base := machineTestConfiguration(TransportHTTP)
	base.MachineServer = nil
	validGroup := "machine_server:\n  listen_address: 127.0.0.1:29443\n  origin: https://machine.example.invalid:29443\n  tls_certificate_file: /synthetic/certificate.pem\n  tls_private_key_file: /synthetic/key.pem\n"
	for name, group := range map[string]string{
		"null_group": "machine_server: null\n", "empty_group": "machine_server: {}\n",
		"boolean_group": "machine_server: false\n", "sequence_group": "machine_server: []\n",
		"partial_group":   "machine_server: {origin: 'https://machine.example.invalid'}\n",
		"unknown_field":   validGroup + "  enabled: true\n",
		"duplicate_field": validGroup + "  origin: https://other.example.invalid\n",
		"duplicate_group": validGroup + validGroup,
		"null_field":      strings.Replace(validGroup, "origin: https://machine.example.invalid:29443", "origin: null", 1),
		"wrong_scalar":    strings.Replace(validGroup, "listen_address: 127.0.0.1:29443", "listen_address: true", 1),
		"alias_field":     strings.Replace(strings.Replace(validGroup, "listen_address: 127.0.0.1:29443", "listen_address: &selected 127.0.0.1:29443", 1), "origin: https://machine.example.invalid:29443", "origin: *selected", 1),
	} {
		t.Run(name, func(t *testing.T) {
			raw := append(transportConfigYAML(t, base), []byte(group)...)
			code := "CONFIG_MACHINE_SERVER_INVALID"
			if name == "partial_group" || name == "empty_group" {
				// These shapes reach UnmarshalYAML; the outer decoder redacts its error.
				code = "CONFIG_INVALID"
			}
			if _, e := Decode(raw); e == nil || e.Error() != code {
				t.Fatal("malformed machine group accepted or error leaked", e)
			}
		})
	}
}

func TestMachineServerExplicitProtectedEnvironmentMapping(t *testing.T) {
	t.Run("four_overrides_preserve_original_group", func(t *testing.T) {
		base := machineTestDecode(t, machineTestConfiguration(TransportHTTP))
		original := *base.MachineServer
		selected := base
		environment := workspaceEnvironmentFixture(t, machineListenAddressEnvironment+"=127.0.0.1:30443\n"+machineOriginEnvironment+"=https://rotated-machine.example.invalid:30443\n"+machineTLSCertificateEnvironment+"=/synthetic/rotated-certificate.pem\n"+machineTLSPrivateKeyEnvironment+"=/synthetic/rotated-key.pem\n")
		if e := selected.resolveWorkspaceEnvironment(environment); e != nil {
			t.Fatal(e)
		}
		wanted := MachineServerConfiguration{ListenAddress: "127.0.0.1:30443", Origin: "https://rotated-machine.example.invalid:30443", TLSCertificateFile: "/synthetic/rotated-certificate.pem", TLSPrivateKeyFile: "/synthetic/rotated-key.pem"}
		if *selected.MachineServer != wanted || *base.MachineServer != original || selected.MachineServer == base.MachineServer {
			t.Fatal("override changed another snapshot or ignored a selected field")
		}
	})
	t.Run("ambient_values_ignored", func(t *testing.T) {
		for _, key := range []string{machineListenAddressEnvironment, machineOriginEnvironment, machineTLSCertificateEnvironment, machineTLSPrivateKeyEnvironment} {
			t.Setenv(key, "ambient-value-must-not-be-used")
		}
		base := machineTestConfiguration(TransportHTTP)
		selected := machineTestDecode(t, base)
		if *selected.MachineServer != *base.MachineServer {
			t.Fatal("ambient environment selected machine policy")
		}
		base.MachineServer = nil
		if selected = machineTestDecode(t, base); selected.MachineServer != nil {
			t.Fatal("ambient values implicitly created a group")
		}
	})
	for name, contents := range map[string]string{
		"empty": machineOriginEnvironment + "=\n", "quoted_empty": machineOriginEnvironment + "=\"\"\n",
		"unknown_enable": "PROJECT_ONE_MACHINE_ENABLED=false\n",
		"duplicate":      machineOriginEnvironment + "=https://one.example.invalid\n" + machineOriginEnvironment + "=https://two.example.invalid\n",
		"plaintext":      machineOriginEnvironment + "=http://machine.example.invalid\n",
		"listener_alias": machineListenAddressEnvironment + "=127.0.0.1:18443\n",
		"origin_alias":   machineOriginEnvironment + "=https://localhost:18443\n",
		"key_alias":      machineTLSPrivateKeyEnvironment + "=/synthetic/machine-certificate.pem\n",
		"relative_key":   machineTLSPrivateKeyEnvironment + "=relative-key.pem\n",
	} {
		t.Run(name, func(t *testing.T) {
			base := machineTestConfiguration(TransportDirectHTTPS)
			if _, e := decodeWithEnvironment(transportConfigYAML(t, base), workspaceEnvironmentFixture(t, contents)); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
				t.Fatal("invalid explicit machine override accepted or error leaked", e)
			}
		})
	}
	for _, key := range []string{machineListenAddressEnvironment, machineOriginEnvironment, machineTLSCertificateEnvironment, machineTLSPrivateKeyEnvironment} {
		t.Run("false_"+key, func(t *testing.T) {
			base := machineTestConfiguration(TransportHTTP)
			if _, e := decodeWithEnvironment(transportConfigYAML(t, base), workspaceEnvironmentFixture(t, key+"=false\n")); e == nil {
				t.Fatal("false string silently enabled/defaulted machine policy")
			}
		})
	}
	t.Run("no_implicit_group_creation", func(t *testing.T) {
		base := machineTestConfiguration(TransportHTTP)
		base.MachineServer = nil
		if _, e := decodeWithEnvironment(transportConfigYAML(t, base), workspaceEnvironmentFixture(t, machineOriginEnvironment+"=https://machine.example.invalid\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
			t.Fatal("environment implicitly created a machine group", e)
		}
	})
	t.Run("invalid_base_cannot_be_repaired", func(t *testing.T) {
		base := machineTestConfiguration(TransportHTTP)
		base.MachineServer.Origin = "http://machine.example.invalid"
		if _, e := decodeWithEnvironment(transportConfigYAML(t, base), workspaceEnvironmentFixture(t, machineOriginEnvironment+"=https://machine.example.invalid\n")); e == nil || e.Error() != "CONFIG_INVALID" {
			t.Fatal("override concealed invalid base group", e)
		}
	})
}

func TestMachineTLSMaterialAndOriginSelection(t *testing.T) {
	t.Run("legacy_selected_origin_without_listener_or_material", func(t *testing.T) {
		for _, mode := range []TransportMode{"", TransportDirectHTTPS, TransportTrustedProxyHTTPS} {
			var c Config
			c.Server.TransportMode, c.Server.AdminOrigin = mode, "https://admin.example.invalid:19443"
			if got, e := c.MachineOrigin(); e != nil || got != c.Server.AdminOrigin {
				t.Fatal("legacy selected HTTPS origin acquired unrelated prerequisites", e)
			}
		}
	})
	t.Run("http_browser_requires_dedicated_machine_origin", func(t *testing.T) {
		c := machineTestConfiguration(TransportHTTP)
		c.MachineServer = nil
		if e := c.Validate(); e != nil {
			t.Fatal("browser-only HTTP configuration refused", e)
		}
		if got, e := c.MachineOrigin(); e == nil || e.Error() != "CONFIG_MACHINE_TRANSPORT_REQUIRED" || got != "" {
			t.Fatal("HTTP provisioned an insecure machine binding", e)
		}
	})
	for _, host := range []string{"machine.example.invalid", "127.0.0.1", "::1"} {
		t.Run("actual_valid_san_"+host, func(t *testing.T) {
			c := machineTestConfiguration(TransportHTTP)
			var dns []string
			var ips []net.IP
			if ip := net.ParseIP(host); ip != nil {
				ips = []net.IP{ip}
			} else {
				dns = []string{host}
			}
			c.MachineServer.Origin = "https://" + net.JoinHostPort(host, "29443")
			c.MachineServer.TLSCertificateFile, c.MachineServer.TLSPrivateKeyFile = transportTLSFixture(t, dns, ips, false)
			got, e := c.MachineTLSConfig()
			if e != nil || got == nil || got.MinVersion != tls.VersionTLS12 || len(got.Certificates) != 1 {
				t.Fatal("valid dedicated machine SAN/material refused", e)
			}
			if browser, e := c.TLSConfig(); e != nil || browser != nil {
				t.Fatal("browser HTTP unexpectedly became a machine TLS validator", e)
			}
		})
	}
	for _, kind := range []string{"wrong_san", "expired", "keypair_mismatch", "public_key", "writable_certificate", "missing_key", "key_fifo"} {
		t.Run(kind, func(t *testing.T) {
			c := machineTestConfiguration(TransportHTTP)
			dns := []string{"machine.example.invalid"}
			if kind == "wrong_san" {
				dns = []string{"other.example.invalid"}
			}
			certificate, key := transportTLSFixture(t, dns, nil, kind == "expired")
			code := "TLS_KEYPAIR_INVALID"
			switch kind {
			case "wrong_san":
				code = "TLS_HOSTNAME_INVALID"
			case "expired":
				code = "TLS_CERTIFICATE_INVALID"
			case "keypair_mismatch":
				_, key = transportTLSFixture(t, dns, nil, false)
			case "public_key":
				if e := os.Chmod(key, 0644); e != nil {
					t.Fatal(e)
				}
				code = "SECRET_FILE_INVALID"
			case "writable_certificate":
				if e := os.Chmod(certificate, 0664); e != nil {
					t.Fatal(e)
				}
			case "missing_key", "key_fifo":
				if e := os.Remove(key); e != nil {
					t.Fatal(e)
				}
				if kind == "key_fifo" {
					if e := syscall.Mkfifo(key, 0600); e != nil {
						t.Fatal(e)
					}
				}
				code = "SECRET_FILE_INVALID"
			}
			c.MachineServer.TLSCertificateFile, c.MachineServer.TLSPrivateKeyFile = certificate, key
			if browser, e := c.TLSConfig(); e != nil || browser != nil {
				t.Fatal("browser structural owner opened machine material", e)
			}
			if got, e := c.MachineTLSConfig(); e == nil || e.Error() != code || got != nil {
				t.Fatal(fmt.Sprintf("invalid machine material %s accepted or error leaked", kind), e)
			}
		})
	}
}
