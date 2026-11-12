package config

import (
	"crypto/tls"
	"errors"
	"reflect"

	"go.yaml.in/yaml/v3"
)

const (
	machineListenAddressEnvironment  = "PROJECT_ONE_MACHINE_LISTEN_ADDRESS"
	machineOriginEnvironment         = "PROJECT_ONE_MACHINE_ORIGIN"
	machineTLSCertificateEnvironment = "PROJECT_ONE_MACHINE_TLS_CERTIFICATE_FILE"
	machineTLSPrivateKeyEnvironment  = "PROJECT_ONE_MACHINE_TLS_PRIVATE_KEY_FILE"
)

// MachineServerConfiguration selects one dedicated direct-HTTPS socket within
// the existing control process. Presence enables the complete group; no field
// has a default, ambient environment authority or verification bypass.
type MachineServerConfiguration struct {
	ListenAddress      string `yaml:"listen_address" json:"listen_address"`
	Origin             string `yaml:"origin" json:"origin"`
	TLSCertificateFile string `yaml:"tls_certificate_file" json:"tls_certificate_file"`
	TLSPrivateKeyFile  string `yaml:"tls_private_key_file" json:"tls_private_key_file"`
}

func (m MachineServerConfiguration) Validate() error {
	bad := errors.New("CONFIG_MACHINE_SERVER_INVALID")
	origin, e := ParseBrowserOrigin(m.Origin)
	if e != nil || origin.Scheme != "https" || validateServerListener(m.ListenAddress) != nil || !serverReference(m.TLSCertificateFile) || !serverReference(m.TLSPrivateKeyFile) || m.TLSCertificateFile == m.TLSPrivateKeyFile {
		return bad
	}
	return nil
}

func (m *MachineServerConfiguration) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_MACHINE_SERVER_INVALID")
	if strictKnowledgeYAML(node, reflect.TypeOf(*m)) != nil {
		return bad
	}
	type plain MachineServerConfiguration
	var value plain
	if node.Decode(&value) != nil || MachineServerConfiguration(value).Validate() != nil {
		return bad
	}
	*m = MachineServerConfiguration(value)
	return nil
}

// This checks exact configured identities without DNS, filesystem reads or
// socket allocation. The serving owner additionally prebinds both sockets to
// refuse overlapping wildcard/address aliases that differ as strings.
func (c Config) validateMachineServer() error {
	if c.MachineServer == nil {
		return nil
	}
	if c.MachineServer.Validate() != nil || c.MachineServer.ListenAddress == c.Server.ListenAddress || c.MachineServer.ListenAddress == c.Server.PortalListenAddress || c.MachineServer.Origin == c.Server.PortalOrigin || c.MachineServer.Origin == c.Server.AdminOrigin {
		return errors.New("CONFIG_MACHINE_SERVER_INVALID")
	}
	return nil
}

// MachineTLSConfig validates only the dedicated TLS material owner. Absence
// creates no second listener and does not reread the legacy browser key pair.
func (c Config) MachineTLSConfig() (*tls.Config, error) {
	if e := c.validateServerTransport(); e != nil {
		return nil, e
	}
	if c.MachineServer == nil {
		return nil, nil
	}
	m := c.MachineServer
	t, e := tlsConfigForOrigins(m.TLSCertificateFile, m.TLSPrivateKeyFile, []string{m.Origin})
	if e != nil {
		return nil, e
	}
	return c.requestAgentCertificates(t)
}

// MachineOrigin is the selected binding for new machine identities, not an
// operation on historical rows. Legacy selection validates the chosen secure
// Admin origin without acquiring a listener or reading private material.
func (c Config) MachineOrigin() (string, error) {
	if c.MachineServer != nil {
		if e := c.validateMachineServer(); e != nil {
			return "", e
		}
		return c.MachineServer.Origin, nil
	}
	mode := c.Server.EffectiveTransportMode()
	if mode == TransportHTTP {
		return "", errors.New("CONFIG_MACHINE_TRANSPORT_REQUIRED")
	}
	if mode != TransportDirectHTTPS && mode != TransportTrustedProxyHTTPS {
		return "", errors.New("CONFIG_MACHINE_SERVER_INVALID")
	}
	origin, e := ParseBrowserOrigin(c.Server.AdminOrigin)
	if e != nil || origin.Scheme != "https" {
		return "", errors.New("CONFIG_MACHINE_SERVER_INVALID")
	}
	return c.Server.AdminOrigin, nil
}

func machineServerEnvironment(key string) bool {
	switch key {
	case machineListenAddressEnvironment, machineOriginEnvironment, machineTLSCertificateEnvironment, machineTLSPrivateKeyEnvironment:
		return true
	}
	return false
}

func (m *MachineServerConfiguration) applyEnvironment(key, value string) error {
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if m == nil || value == "" {
		return bad
	}
	switch key {
	case machineListenAddressEnvironment:
		m.ListenAddress = value
	case machineOriginEnvironment:
		m.Origin = value
	case machineTLSCertificateEnvironment:
		m.TLSCertificateFile = value
	case machineTLSPrivateKeyEnvironment:
		m.TLSPrivateKeyFile = value
	default:
		return bad
	}
	return nil
}
