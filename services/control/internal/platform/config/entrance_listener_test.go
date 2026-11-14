package config

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPortalListenerTypedSelectionAndProtectedEnvironment(t *testing.T) {
	t.Setenv(portalListenAddressEnvironment, "127.0.0.1:31443")
	base := validConfig()
	for _, mode := range []TransportMode{TransportHTTP, TransportDirectHTTPS, TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			base.Server = transportTestServer(mode)
			omitted, err := decodeWithEnvironment(transportConfigYAML(t, base), "")
			if err != nil || omitted.Server.PortalListenAddress != "" {
				t.Fatal("omission or ignored ambient configuration changed", err)
			}
			base.Server.PortalListenAddress = "[::1]:29443"
			selected, err := decodeWithEnvironment(transportConfigYAML(t, base), "")
			if err != nil || selected.Server.PortalListenAddress != base.Server.PortalListenAddress {
				t.Fatal("explicit typed Portal listener lost", err)
			}
			override := workspaceEnvironmentFixture(t, portalListenAddressEnvironment+"=127.0.0.1:30443\n")
			selected, err = decodeWithEnvironment(transportConfigYAML(t, base), override)
			if err != nil || selected.Server.PortalListenAddress != "127.0.0.1:30443" || base.Server.PortalListenAddress != "[::1]:29443" {
				t.Fatal("protected override failed or mutated its input", err)
			}
		})
	}
}

func TestPortalListenerRejectsInvalidStructureAndCollisions(t *testing.T) {
	base := validConfig()
	raw := transportConfigYAML(t, base)
	for _, value := range []string{"null", "''", "false", "[]", "{}", "127.0.0.1:0", "127.0.0.1:029443", ":29443", "https://sensitive-value", base.Server.ListenAddress} {
		body := bytes.Replace(raw, []byte("server:\n"), []byte("server:\n    portal_listen_address: "+value+"\n"), 1)
		if _, err := decodeWithEnvironment(body, workspaceEnvironmentFixture(t, portalListenAddressEnvironment+"=127.0.0.1:30443\n")); err == nil || strings.Contains(err.Error(), "sensitive-value") {
			t.Fatal("invalid base listener was repaired or disclosed", value, err)
		}
	}
	for _, body := range []string{portalListenAddressEnvironment + "=\n", portalListenAddressEnvironment + "=false\n", portalListenAddressEnvironment + "=" + base.Server.ListenAddress + "\n", portalListenAddressEnvironment + "=127.0.0.1:29443\n" + portalListenAddressEnvironment + "=127.0.0.1:30443\n"} {
		if _, err := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); err == nil {
			t.Fatal("invalid explicit Portal listener environment accepted")
		}
	}
	base.Server.PortalListenAddress = "127.0.0.1:29443"
	base.MachineServer = &MachineServerConfiguration{ListenAddress: base.Server.PortalListenAddress, Origin: "https://machine.example.invalid", TLSCertificateFile: "/synthetic/certificate.pem", TLSPrivateKeyFile: "/synthetic/key.pem"}
	if err := base.Validate(); err == nil || err.Error() != "CONFIG_MACHINE_SERVER_INVALID" {
		t.Fatal("machine listener collision accepted", err)
	}
	base.MachineServer = nil
	base.AgentGateway = &AgentGatewayConfiguration{ListenAddress: base.Server.PortalListenAddress, TLSCertificateFile: "/synthetic/certificate.pem", TLSPrivateKeyFile: "/synthetic/key.pem"}
	if err := base.Validate(); err == nil || err.Error() != "CONFIG_AGENT_GATEWAY_INVALID" {
		t.Fatal("gateway listener collision accepted", err)
	}
}

func TestPortalListenerServingSchemaAndDocumentedEnvironment(t *testing.T) {
	raw, err := os.ReadFile("../../../../../api/configuration/v1/control-serving-expectation/schema.json")
	var schema struct {
		Properties map[string]struct {
			Type    string `json:"type"`
			Minimum int    `json:"minLength"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err != nil || json.Unmarshal(raw, &schema) != nil || schema.Properties["portal_listen_address"].Type != "string" || schema.Properties["portal_listen_address"].Minimum != 1 {
		t.Fatal("Portal listener intent schema differs from its typed owner", err)
	}
	for _, field := range schema.Required {
		if field == "portal_listen_address" {
			t.Fatal("optional listener became mandatory for old expectations")
		}
	}
	example, err := os.ReadFile("../../../config/examples/control/transport.env.example")
	if err != nil || !bytes.Contains(example, []byte("# "+portalListenAddressEnvironment+"=")) {
		t.Fatal("optional protected mapping is not documented", err)
	}
}
