package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"
)

const (
	defaultAgentCertificateLifetimeHours = 720
	minimumAgentCertificateLifetimeHours = 24
	maximumAgentCertificateLifetimeHours = 8760
)

// AgentIdentityConfiguration names the agent CA (ADR 0050). Serving agents on PostgreSQL
// requires it, so client certificates are not something a deployment can leave out.
type AgentIdentityConfiguration struct {
	// CACertificateFile is a PEM bundle: the issuer first, then any CA still trusted while
	// an old one is replaced.
	CACertificateFile string `yaml:"ca_certificate_file"`
	// CAPrivateKeyFile is the issuer's key. Control needs it to sign. A host that runs only
	// agent gateways leaves it out, so it cannot sign.
	CAPrivateKeyFile string `yaml:"ca_private_key_file,omitempty"`
	// CertificateLifetimeHours is how long an issued certificate lasts; zero means 720.
	CertificateLifetimeHours int `yaml:"certificate_lifetime_hours,omitempty"`
}

// Validate accepts an absent section and refuses an incomplete one.
func (a *AgentIdentityConfiguration) Validate() error {
	if a == nil {
		return nil
	}
	if !serverReference(a.CACertificateFile) || a.CAPrivateKeyFile != "" && (!serverReference(a.CAPrivateKeyFile) || a.CAPrivateKeyFile == a.CACertificateFile) ||
		a.CertificateLifetimeHours != 0 && (a.CertificateLifetimeHours < minimumAgentCertificateLifetimeHours || a.CertificateLifetimeHours > maximumAgentCertificateLifetimeHours) {
		return errors.New("CONFIG_AGENT_IDENTITY_INVALID")
	}
	return nil
}

// Lifetime is how long an issued certificate lasts.
func (a *AgentIdentityConfiguration) Lifetime() time.Duration {
	hours := a.CertificateLifetimeHours
	if hours == 0 {
		hours = defaultAgentCertificateLifetimeHours
	}
	return time.Duration(hours) * time.Hour
}

// servesAgents reports whether this configuration has an agent entrance on PostgreSQL, the
// engine that stores certificate pins until CMP-201 and CMP-202.
func (c Config) servesAgents() bool {
	if c.DatabaseEngine() != PostgreSQLDatabaseEngine {
		return false
	}
	_, e := c.MachineOrigin()
	return e == nil
}

// CheckAgentIdentity refuses to serve agents without certificates. A proxy ending TLS in
// front of the agent entrance would hide them, so that entrance needs a socket of its own.
func (c Config) CheckAgentIdentity() error {
	if !c.servesAgents() {
		return nil
	}
	if c.AgentIdentity == nil {
		return errors.New("CONFIG_AGENT_IDENTITY_REQUIRED")
	}
	if c.MachineServer == nil && c.Server.EffectiveTransportMode() != TransportDirectHTTPS {
		return errors.New("CONFIG_AGENT_IDENTITY_TRANSPORT_INVALID")
	}
	return nil
}

// AgentAuthorityMaterial reads the CA bundle and its key for signing.
func (c Config) AgentAuthorityMaterial() (bundlePEM, keyPEM []byte, e error) {
	if c.AgentIdentity == nil || c.AgentIdentity.CAPrivateKeyFile == "" {
		return nil, nil, errors.New("CONFIG_AGENT_IDENTITY_REQUIRED")
	}
	return readTLSMaterialPair(c.AgentIdentity.CACertificateFile, c.AgentIdentity.CAPrivateKeyFile)
}

// requestAgentCertificates asks connections for an agent certificate and verifies any that
// is sent. A connection may send none: enrollment has no certificate yet, and browsers share
// the listener by default. They are never prompted, because the request names only the agent
// CA, which no browser holds. Each path decides whether it needs one.
func (c Config) requestAgentCertificates(t *tls.Config) (*tls.Config, error) {
	if t == nil || c.AgentIdentity == nil {
		return t, nil
	}
	bundle, e := readTLSMaterialFile(c.AgentIdentity.CACertificateFile)
	if e != nil {
		return nil, e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bundle) {
		return nil, errors.New("AGENT_IDENTITY_MATERIAL_INVALID")
	}
	t.ClientAuth = tls.VerifyClientCertIfGiven
	t.ClientCAs = pool
	return t, nil
}

// readTLSMaterialFile reads one public PEM file with the checks of a TLS pair.
func readTLSMaterialFile(path string) (raw []byte, err error) {
	file, e := pinTLSMaterial(path, false)
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := file.close(); e != nil && err == nil {
			raw, err = nil, e
		}
	}()
	if e = file.open(); e != nil {
		return nil, e
	}
	return file.read()
}
