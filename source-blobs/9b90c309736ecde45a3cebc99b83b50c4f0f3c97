package config

import (
	"crypto/tls"
	"testing"
)

func TestAgentIdentitySectionIsChecked(t *testing.T) {
	var absent *AgentIdentityConfiguration
	if absent.Validate() != nil {
		t.Fatal("an absent section is valid")
	}
	good := AgentIdentityConfiguration{CACertificateFile: "/run/ca.pem", CAPrivateKeyFile: "/run/ca-key.pem"}
	if good.Validate() != nil {
		t.Fatal("complete section")
	}
	gatewayOnly := AgentIdentityConfiguration{CACertificateFile: "/run/ca.pem"}
	if gatewayOnly.Validate() != nil {
		t.Fatal("a gateway needs no key")
	}
	if good.Lifetime().Hours() != defaultAgentCertificateLifetimeHours {
		t.Fatal(good.Lifetime())
	}
	for name, bad := range map[string]AgentIdentityConfiguration{
		"no bundle":      {CAPrivateKeyFile: "/run/ca-key.pem"},
		"relative":       {CACertificateFile: "ca.pem"},
		"key is bundle":  {CACertificateFile: "/run/ca.pem", CAPrivateKeyFile: "/run/ca.pem"},
		"lifetime short": {CACertificateFile: "/run/ca.pem", CertificateLifetimeHours: 23},
		"lifetime long":  {CACertificateFile: "/run/ca.pem", CertificateLifetimeHours: 8761},
	} {
		section := bad
		if section.Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAgentCertificatesAreOnlyAskedForWithACA(t *testing.T) {
	var c Config
	in := &tls.Config{}
	out, e := c.requestAgentCertificates(in)
	if e != nil || out.ClientAuth != tls.NoClientCert || out.ClientCAs != nil {
		t.Fatal(out, e)
	}
	if out, e := c.requestAgentCertificates(nil); e != nil || out != nil {
		t.Fatal(out, e)
	}
}

func TestAgentEntranceNeedsItsCertificates(t *testing.T) {
	direct := machineTestConfiguration(TransportDirectHTTPS)
	direct.MachineServer = nil
	if e := direct.CheckAgentIdentity(); e == nil || e.Error() != "CONFIG_AGENT_IDENTITY_REQUIRED" {
		t.Fatalf("direct HTTPS without a CA: %v", e)
	}
	direct.AgentIdentity = &AgentIdentityConfiguration{CACertificateFile: "/run/ca.pem"}
	if e := direct.CheckAgentIdentity(); e != nil {
		t.Fatal(e)
	}
	proxied := machineTestConfiguration(TransportTrustedProxyHTTPS)
	proxied.MachineServer = nil
	proxied.AgentIdentity = direct.AgentIdentity
	if e := proxied.CheckAgentIdentity(); e == nil || e.Error() != "CONFIG_AGENT_IDENTITY_TRANSPORT_INVALID" {
		t.Fatalf("a proxy that ends TLS hides the certificates: %v", e)
	}
	separate := machineTestConfiguration(TransportTrustedProxyHTTPS)
	separate.AgentIdentity = direct.AgentIdentity
	if e := separate.CheckAgentIdentity(); e != nil {
		t.Fatalf("a socket of its own sees them: %v", e)
	}
	plain := machineTestConfiguration(TransportHTTP)
	plain.MachineServer = nil
	if e := plain.CheckAgentIdentity(); e != nil {
		t.Fatalf("no agent entrance, nothing to require: %v", e)
	}
}
