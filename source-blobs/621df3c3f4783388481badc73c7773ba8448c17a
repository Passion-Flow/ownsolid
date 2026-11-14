package config

import (
	"errors"
	"time"
)

const (
	defaultGatewayCertificateLifetimeHours = 8760
	minimumGatewayCertificateLifetimeHours = 24
	maximumGatewayCertificateLifetimeHours = 8760
)

// ModelGatewayIdentityConfiguration names the CA that signs the serving certificates of the
// model gateways agents run on serving nodes (GW-114, ADR 0052). It is kept apart from the
// agent CA and from the console's serving certificate, and callers of a node's gateway trust
// its bundle. Without it, a gateway is registered the way it was before agents ran them: an
// administrator receives its credential once.
type ModelGatewayIdentityConfiguration struct {
	// CACertificateFile is a PEM bundle: the issuer first, then any CA still trusted while
	// an old one is replaced.
	CACertificateFile string `yaml:"ca_certificate_file"`
	// CAPrivateKeyFile is the issuer's key, which only control reads.
	CAPrivateKeyFile string `yaml:"ca_private_key_file"`
	// CertificateLifetimeHours is how long an issued certificate lasts; zero means 8760.
	// Publishing the deployment again issues a new one (GW-124 renews them on their own).
	CertificateLifetimeHours int `yaml:"certificate_lifetime_hours,omitempty"`
}

// Validate accepts an absent section and refuses an incomplete one.
func (g *ModelGatewayIdentityConfiguration) Validate() error {
	if g == nil {
		return nil
	}
	if !serverReference(g.CACertificateFile) || !serverReference(g.CAPrivateKeyFile) || g.CAPrivateKeyFile == g.CACertificateFile ||
		g.CertificateLifetimeHours != 0 && (g.CertificateLifetimeHours < minimumGatewayCertificateLifetimeHours || g.CertificateLifetimeHours > maximumGatewayCertificateLifetimeHours) {
		return errors.New("CONFIG_MODEL_GATEWAY_IDENTITY_INVALID")
	}
	return nil
}

// Lifetime is how long an issued certificate lasts.
func (g *ModelGatewayIdentityConfiguration) Lifetime() time.Duration {
	hours := g.CertificateLifetimeHours
	if hours == 0 {
		hours = defaultGatewayCertificateLifetimeHours
	}
	return time.Duration(hours) * time.Hour
}

// AgentsRunGateways reports whether agents may run the model gateways of their nodes. Only
// PostgreSQL records their delivery, like the agent certificates they are bound to.
func (c Config) AgentsRunGateways() bool {
	return c.ModelGatewayIdentity != nil && c.DatabaseEngine() == PostgreSQLDatabaseEngine
}

// ModelGatewayAuthorityMaterial reads the gateway CA bundle and its key for signing.
func (c Config) ModelGatewayAuthorityMaterial() (bundlePEM, keyPEM []byte, e error) {
	if c.ModelGatewayIdentity == nil {
		return nil, nil, errors.New("CONFIG_MODEL_GATEWAY_IDENTITY_REQUIRED")
	}
	return readTLSMaterialPair(c.ModelGatewayIdentity.CACertificateFile, c.ModelGatewayIdentity.CAPrivateKeyFile)
}
