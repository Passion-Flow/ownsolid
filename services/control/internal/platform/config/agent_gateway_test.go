package config

import (
	"crypto/tls"
	"testing"
	"time"
)

func agentGatewayTestConfiguration(t *testing.T, names []string) Config {
	t.Helper()
	c := machineTestConfiguration(TransportHTTP)
	certificate, key := transportTLSFixture(t, names, nil, false)
	c.AgentGateway = &AgentGatewayConfiguration{ListenAddress: "127.0.0.1:39443", TLSCertificateFile: certificate, TLSPrivateKeyFile: key}
	c.AgentIdentity = &AgentIdentityConfiguration{CACertificateFile: certificate}
	return c
}

func TestAgentGatewayConfigurationIsCompleteAndApart(t *testing.T) {
	c := agentGatewayTestConfiguration(t, []string{"machine.example.invalid"})
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if decoded := machineTestDecode(t, c); decoded.AgentGateway == nil || *decoded.AgentGateway != *c.AgentGateway {
		t.Fatalf("section lost in decoding: %+v", decoded.AgentGateway)
	}
	for name, change := range map[string]func(*Config){
		"no listener":      func(c *Config) { c.AgentGateway.ListenAddress = "" },
		"bad listener":     func(c *Config) { c.AgentGateway.ListenAddress = "127.0.0.1:0" },
		"relative pair":    func(c *Config) { c.AgentGateway.TLSCertificateFile = "certificate.pem" },
		"one file":         func(c *Config) { c.AgentGateway.TLSPrivateKeyFile = c.AgentGateway.TLSCertificateFile },
		"control listener": func(c *Config) { c.AgentGateway.ListenAddress = c.Server.ListenAddress },
		"machine listener": func(c *Config) { c.AgentGateway.ListenAddress = c.MachineServer.ListenAddress },
	} {
		c := agentGatewayTestConfiguration(t, []string{"machine.example.invalid"})
		change(&c)
		if e := c.Validate(); e == nil || e.Error() != "CONFIG_AGENT_GATEWAY_INVALID" {
			t.Errorf("%s: %v", name, e)
		}
	}
}

func TestAgentGatewayConnectionMaxAgeDefaultsAndBounds(t *testing.T) {
	c := agentGatewayTestConfiguration(t, []string{"machine.example.invalid"})
	if got := c.AgentGateway.MaxConnectionAge(); got != 30*time.Minute {
		t.Fatalf("legacy gateway age: %v", got)
	}
	for _, value := range []int{60, 1200, 86400} {
		c.AgentGateway.ConnectionMaxAgeSeconds = value
		decoded := machineTestDecode(t, c)
		if e := decoded.Validate(); e != nil || decoded.AgentGateway.MaxConnectionAge() != Seconds(value) {
			t.Fatalf("connection age %d: %v", value, e)
		}
	}
	for _, value := range []int{-1, 59, 86401} {
		c.AgentGateway.ConnectionMaxAgeSeconds = value
		if e := c.Validate(); e == nil || e.Error() != "CONFIG_AGENT_GATEWAY_INVALID" {
			t.Fatalf("invalid connection age %d: %v", value, e)
		}
	}
}

func TestAgentGatewayServesTheAgentOriginWithItsCertificate(t *testing.T) {
	c := agentGatewayTestConfiguration(t, []string{"machine.example.invalid"})
	origin, tlsConfig, e := c.AgentGatewayServing()
	if e != nil || origin != c.MachineServer.Origin || tlsConfig == nil || len(tlsConfig.Certificates) != 1 || tlsConfig.NextProtos[0] != "h2" {
		t.Fatalf("origin %q tls %v: %v", origin, tlsConfig, e)
	}
	// A gateway verifies agents' certificates with the CA alone, and enrollment never comes here.
	if tlsConfig.ClientAuth != tls.VerifyClientCertIfGiven || tlsConfig.ClientCAs == nil {
		t.Fatalf("client auth %v", tlsConfig.ClientAuth)
	}
	unidentified := agentGatewayTestConfiguration(t, []string{"machine.example.invalid"})
	unidentified.AgentIdentity = nil
	if _, _, e := unidentified.AgentGatewayServing(); e == nil || e.Error() != "CONFIG_AGENT_IDENTITY_REQUIRED" {
		t.Fatalf("no agent CA: %v", e)
	}
	// Agents verify the origin's host name, so a gateway certificate without it is refused.
	other := agentGatewayTestConfiguration(t, []string{"gateway-1.example.invalid"})
	if _, _, e := other.AgentGatewayServing(); e == nil || e.Error() != "TLS_HOSTNAME_INVALID" {
		t.Fatalf("certificate for another name: %v", e)
	}
	absent := machineTestConfiguration(TransportHTTP)
	if _, _, e := absent.AgentGatewayServing(); e == nil || e.Error() != "CONFIG_AGENT_GATEWAY_REQUIRED" {
		t.Fatalf("no section: %v", e)
	}
	mysql := agentGatewayTestConfiguration(t, []string{"machine.example.invalid"})
	mysql.Database.Engine = MySQLDatabaseEngine
	if _, _, e := mysql.AgentGatewayServing(); e == nil || e.Error() != "AGENT_GATEWAY_POSTGRESQL_REQUIRED" {
		t.Fatalf("MySQL: %v", e)
	}
}

func TestRoleConnectionLimitCoversThePool(t *testing.T) {
	c := validConfig()
	if c.RoleConnections() != c.Database.MaxOpenConnections {
		t.Fatalf("default limit %d for a pool of %d", c.RoleConnections(), c.Database.MaxOpenConnections)
	}
	for limit, valid := range map[int32]bool{0: true, c.Database.MaxOpenConnections: true, 30: true, MaximumRoleConnections: true, c.Database.MaxOpenConnections - 1: false, MaximumRoleConnections + 1: false} {
		c.Database.RoleConnectionLimit = limit
		if e := c.Validate(); (e == nil) != valid {
			t.Errorf("limit %d: %v", limit, e)
		}
	}
	c.Database.RoleConnectionLimit = 30
	if c.RoleConnections() != 30 {
		t.Fatal(c.RoleConnections())
	}
}
