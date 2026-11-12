package config

import (
	"crypto/tls"
	"errors"
	"time"
)

// AgentGatewayConfiguration is the listener of an agent gateway process (ADR 0048), which
// `serve-agent-gateway` starts beside control. A gateway serves only the agent session stream,
// for the agent entrance's origin, from this installation's database. Control ignores the
// section, so one configuration file can serve both.
type AgentGatewayConfiguration struct {
	// ListenAddress is the host:port the gateway binds; agents list it in
	// session_addresses as the address they reach it on.
	ListenAddress string `yaml:"listen_address"`
	// TLSCertificateFile and TLSPrivateKeyFile are the gateway's certificate pair. The
	// certificate must name the agent entrance's host, because agents verify that name.
	TLSCertificateFile string `yaml:"tls_certificate_file"`
	TLSPrivateKeyFile  string `yaml:"tls_private_key_file"`
	// ConnectionMaxAgeSeconds bounds when a dedicated connection begins draining. Zero selects the default.
	ConnectionMaxAgeSeconds int `yaml:"connection_max_age_seconds,omitempty"`
}

// MaxConnectionAge is the latest age at which a dedicated connection begins draining.
func (g AgentGatewayConfiguration) MaxConnectionAge() time.Duration {
	if g.ConnectionMaxAgeSeconds == 0 {
		return 30 * time.Minute
	}
	return Seconds(g.ConnectionMaxAgeSeconds)
}

// Validate accepts an absent section and refuses an incomplete one.
func (g *AgentGatewayConfiguration) Validate() error {
	if g == nil {
		return nil
	}
	if validateServerListener(g.ListenAddress) != nil || !serverReference(g.TLSCertificateFile) || !serverReference(g.TLSPrivateKeyFile) || g.TLSCertificateFile == g.TLSPrivateKeyFile ||
		g.ConnectionMaxAgeSeconds != 0 && (g.ConnectionMaxAgeSeconds < 60 || g.ConnectionMaxAgeSeconds > 86400) {
		return errors.New("CONFIG_AGENT_GATEWAY_INVALID")
	}
	return nil
}

// validateAgentGateway refuses a gateway that would bind a listener control binds.
func (c Config) validateAgentGateway() error {
	if c.AgentGateway == nil {
		return nil
	}
	if e := c.AgentGateway.Validate(); e != nil {
		return e
	}
	if c.AgentGateway.ListenAddress == c.Server.ListenAddress || c.AgentGateway.ListenAddress == c.Server.PortalListenAddress || c.MachineServer != nil && c.AgentGateway.ListenAddress == c.MachineServer.ListenAddress {
		return errors.New("CONFIG_AGENT_GATEWAY_INVALID")
	}
	return nil
}

// AgentGatewayServing returns what a gateway process serves with: the agent entrance's
// origin and a TLS configuration whose certificate names that origin's host.
func (c Config) AgentGatewayServing() (string, *tls.Config, error) {
	if c.AgentGateway == nil {
		return "", nil, errors.New("CONFIG_AGENT_GATEWAY_REQUIRED")
	}
	if e := c.validateAgentGateway(); e != nil {
		return "", nil, e
	}
	if c.DatabaseEngine() != PostgreSQLDatabaseEngine {
		return "", nil, errors.New("AGENT_GATEWAY_POSTGRESQL_REQUIRED")
	}
	if e := c.CheckAgentIdentity(); e != nil {
		return "", nil, e
	}
	origin, e := c.MachineOrigin()
	if e != nil {
		return "", nil, e
	}
	tlsConfig, e := tlsConfigForOrigins(c.AgentGateway.TLSCertificateFile, c.AgentGateway.TLSPrivateKeyFile, []string{origin})
	if e != nil {
		return "", nil, e
	}
	if tlsConfig, e = c.requestAgentCertificates(tlsConfig); e != nil {
		return "", nil, e
	}
	return origin, tlsConfig, nil
}
