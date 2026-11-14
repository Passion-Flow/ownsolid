package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func servingCLIExpectation(t *testing.T, server config.ServerConfiguration) string {
	t.Helper()
	prefixes := append([]string{}, server.TrustedProxyCIDRs...)
	raw, e := json.Marshal(servingcheck.Expected{FormatVersion: 1, TransportMode: server.EffectiveTransportMode(),
		ListenAddress: server.ListenAddress, PortalOrigin: server.PortalOrigin, AdminOrigin: server.AdminOrigin,
		TLSCertificateFile: server.TLSCertificateFile, TLSPrivateKeyFile: server.TLSPrivateKeyFile, TrustedProxyCIDRs: prefixes})
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func servingCLIArguments(t *testing.T, path string) []string {
	t.Helper()
	c, e := config.LoadProtectedWithEnvironment(path, "")
	if e != nil {
		t.Fatal("synthetic serving check fixture", e)
	}
	return []string{"--config", path, "--expected-server-json", servingCLIExpectation(t, c.Server)}
}

func TestServingCheckCLIActualDispatchBeforeDatabaseConstruction(t *testing.T) {
	// Existing explicit fixture has missing DSN/HMAC/mail/blocklist references.
	// No server listens at this address: verification must make no HTTP request.
	path := probeCLIConfiguration(t, "127.0.0.1:1")
	args := servingCLIArguments(t, path)
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = append([]string{"control", servingCheckCommand}, args...)
	if e := run(); e != nil {
		t.Fatal("actual verification dispatcher opened unavailable runtime owners", e)
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only verification changed its protected configuration", e)
	}
	for _, name := range []string{"unavailable-dsn", "unavailable-hmac", "unavailable-mail", "unavailable-blocklist"} {
		if _, e := os.Stat(filepath.Join(filepath.Dir(path), name)); !os.IsNotExist(e) {
			t.Fatal("verification created a runtime credential or state")
		}
	}
}

func TestServingCheckCLIRejectsArgumentsBeforeIO(t *testing.T) {
	servingExpectationSchemaControls(t)
	server := config.ServerConfiguration{TransportMode: config.TransportHTTP, ListenAddress: "127.0.0.1:1", PortalOrigin: "http://portal.example.invalid", AdminOrigin: "http://admin.example.invalid"}
	base := []string{"--config", filepath.Join(t.TempDir(), "synthetic-sensitive-missing.yaml"), "--expected-server-json", servingCLIExpectation(t, server)}
	cases := map[string][]string{
		"missing_all":        nil,
		"missing_config":     base[2:],
		"missing_expected":   base[:2],
		"unknown":            append(append([]string{}, base...), "--unowned=synthetic-sensitive-value"),
		"positional":         append(append([]string{}, base...), "synthetic-sensitive-value"),
		"duplicate_config":   append(append([]string{}, base...), "--config=/synthetic-sensitive-value"),
		"duplicate_expected": append(append([]string{}, base...), "--expected-server-json=synthetic-sensitive-value"),
		"duplicate_env":      append(append([]string{}, base...), "--env-file=/synthetic-one", "--env-file=/synthetic-two"),
		"empty_env":          append(append([]string{}, base...), "--env-file="),
		"relative_env":       append(append([]string{}, base...), "--env-file=relative-policy"),
		"unclean_env":        append(append([]string{}, base...), "--env-file=/private/./unowned"),
	}
	for name, args := range map[string][]string{"empty_config": {"--config", "", "--expected-server-json", base[3]}, "empty_expected": {"--config", base[1], "--expected-server-json", ""}, "relative_config": {"--config", "relative-control", "--expected-server-json", base[3]}} {
		cases[name] = args
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if e := runServingCheckCommand(args, &output); e == nil || e.Error() != "SERVING_TRANSPORT_ARGUMENT_INVALID" || output.Len() != 0 {
				t.Fatal("invalid arguments reached configuration I/O or disclosed input", e)
			}
		})
	}
	for _, raw := range []string{"false", "null", "{}", `{"synthetic_sensitive":"unowned-value"}`, strings.Repeat("x", servingcheck.MaximumExpectedBytes+1)} {
		var output bytes.Buffer
		args := append([]string{}, base...)
		args[3] = raw
		if e := runServingCheckCommand(args, &output); e == nil || e.Error() != "SERVING_TRANSPORT_EXPECTATION_INVALID" || output.Len() != 0 {
			t.Fatal("invalid public expectation reached configuration I/O or disclosed input", e)
		}
	}
}

func TestServingCheckCLIExplicitEnvironmentAndFixedErrors(t *testing.T) {
	path := probeCLIConfiguration(t, "127.0.0.1:1")
	args := servingCLIArguments(t, path)
	environment := filepath.Join(filepath.Dir(path), "serving-check.env")
	if e := os.WriteFile(environment, []byte("PROJECT_ONE_PORTAL_ORIGIN=http://overridden.example.invalid:31083\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := config.LoadProtectedWithEnvironment(path, environment)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PROJECT_ONE_PORTAL_ORIGIN", "http://ambient-untrusted.example.invalid")
	args[3] = servingCLIExpectation(t, c.Server)
	args = append(args, "--env-file", environment)
	var output bytes.Buffer
	if e := runServingCheckCommand(args, &output); e != nil || output.String() != "CONTROL_SERVING_TRANSPORT_OK\n" {
		t.Fatal("effective typed explicit environment or fixed success failed", e)
	}
	output.Reset()
	if e := runServingCheckCommand(args[:4], &output); e == nil || e.Error() != "SERVING_TRANSPORT_MISMATCH" || output.Len() != 0 {
		t.Fatal("comparison silently adopted ambient or base origin", e)
	}
	args[1] = filepath.Join(filepath.Dir(path), "synthetic-sensitive-missing")
	if e := runServingCheckCommand(args, &output); e == nil || e.Error() != "SERVING_TRANSPORT_CONFIGURATION_INVALID" || output.Len() != 0 {
		t.Fatal("configuration failure disclosed protected input", e)
	}
}

func TestServingCheckCLIOutputFailureIsRedacted(t *testing.T) {
	path := probeCLIConfiguration(t, "127.0.0.1:1")
	if e := runServingCheckCommand(servingCLIArguments(t, path), failedProbeOutput{}); e == nil || e.Error() != "SERVING_TRANSPORT_OUTPUT_FAILED" {
		t.Fatal("output failure passed or disclosed underlying data", e)
	}
}
