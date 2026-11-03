// Package servingcheck compares explicit deployment intent with the typed
// serving configuration before delegating material verification to its TLS owner.
package servingcheck

import (
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	Command              = "verify-serving-transport"
	Success              = "CONTROL_SERVING_TRANSPORT_OK"
	MaximumExpectedBytes = 64 << 10

	expectedFormatVersion = 1
	expectationInvalid    = "SERVING_TRANSPORT_EXPECTATION_INVALID"
	configurationInvalid  = "SERVING_TRANSPORT_CONFIGURATION_INVALID"
	transportMismatch     = "SERVING_TRANSPORT_MISMATCH"
)

// Expected is versioned public intent, not an alternate runtime policy source.
// Empty TLS references and an empty proxy array are still required JSON fields.
type Expected struct {
	FormatVersion      int                  `json:"format_version"`
	TransportMode      config.TransportMode `json:"transport_mode"`
	ListenAddress      string               `json:"listen_address"`
	PortalOrigin       string               `json:"portal_origin"`
	AdminOrigin        string               `json:"admin_origin"`
	TLSCertificateFile string               `json:"tls_certificate_file"`
	TLSPrivateKeyFile  string               `json:"tls_private_key_file"`
	TrustedProxyCIDRs  []string             `json:"trusted_proxy_cidrs"`
}

// ParseExpected performs bounded strict parsing and validation without I/O.
func ParseExpected(raw []byte) (Expected, error) {
	bad := errors.New(expectationInvalid)
	var expected Expected
	if len(raw) == 0 || len(raw) > MaximumExpectedBytes || release.StrictJSON(raw, &expected) != nil {
		return Expected{}, bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 8 {
		return Expected{}, bad
	}
	for _, name := range []string{"format_version", "transport_mode", "listen_address", "portal_origin", "admin_origin", "tls_certificate_file", "tls_private_key_file", "trusted_proxy_cidrs"} {
		if _, exists := fields[name]; !exists {
			return Expected{}, bad
		}
	}
	if expected.Validate() != nil {
		return Expected{}, bad
	}
	return expected, nil
}

// ParseExpectedMachine uses the versioned machine configuration's exact public
// fields as intent. Absence is represented by an omitted CLI flag, never null.
func ParseExpectedMachine(raw []byte) (config.MachineServerConfiguration, error) {
	bad := errors.New(expectationInvalid)
	var expected config.MachineServerConfiguration
	if len(raw) == 0 || len(raw) > MaximumExpectedBytes || release.StrictJSON(raw, &expected) != nil {
		return config.MachineServerConfiguration{}, bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 {
		return config.MachineServerConfiguration{}, bad
	}
	for _, name := range []string{"listen_address", "origin", "tls_certificate_file", "tls_private_key_file"} {
		if _, exists := fields[name]; !exists {
			return config.MachineServerConfiguration{}, bad
		}
	}
	if expected.Validate() != nil {
		return config.MachineServerConfiguration{}, bad
	}
	return expected, nil
}

// Validate reuses the existing transport policy and does not open references.
func (expected Expected) Validate() error {
	bad := errors.New(expectationInvalid)
	if expected.FormatVersion != expectedFormatVersion || expected.TrustedProxyCIDRs == nil || expected.TransportMode != config.TransportHTTP && expected.TransportMode != config.TransportDirectHTTPS && expected.TransportMode != config.TransportTrustedProxyHTTPS {
		return bad
	}
	proxies := expected.TrustedProxyCIDRs
	if len(proxies) == 0 {
		// The wire requires an explicit empty array; the existing runtime uses
		// nil for modes that must not configure proxies. Do not mutate intent.
		proxies = nil
	}
	server := config.ServerConfiguration{TransportMode: expected.TransportMode,
		ListenAddress: expected.ListenAddress, PortalOrigin: expected.PortalOrigin,
		AdminOrigin: expected.AdminOrigin, TLSCertificateFile: expected.TLSCertificateFile,
		TLSPrivateKeyFile: expected.TLSPrivateKeyFile, TrustedProxyCIDRs: proxies}
	if server.ValidateTransport() != nil || expected.TransportMode == config.TransportDirectHTTPS && (expected.TLSCertificateFile == "" || expected.TLSPrivateKeyFile == "") {
		return bad
	}
	return nil
}

func sameProxySet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	set := make(map[string]bool, len(actual))
	for _, prefix := range actual {
		set[prefix] = true
	}
	for _, prefix := range expected {
		if !set[prefix] {
			return false
		}
	}
	return true
}

// Verify revalidates complete configuration and intent, then compares all public
// transport fields before material I/O. Config.TLSConfig remains the sole TLS
// byte-read, keypair, validity and DNS/IP SAN authority for every transport mode.
func Verify(configuration config.Config, expected Expected) error {
	return VerifyWithMachine(configuration, expected, nil)
}

// VerifyWithMachine compares both public intents before reading either keypair.
// A nil machine expectation asserts absence; it never ignores a configured group.
func VerifyWithMachine(configuration config.Config, expected Expected, machine *config.MachineServerConfiguration) error {
	if expected.Validate() != nil {
		return errors.New(expectationInvalid)
	}
	if machine != nil && machine.Validate() != nil {
		return errors.New(expectationInvalid)
	}
	if configuration.Validate() != nil {
		return errors.New(configurationInvalid)
	}
	actual := configuration.Server
	if actual.EffectiveTransportMode() != expected.TransportMode || actual.ListenAddress != expected.ListenAddress || actual.PortalOrigin != expected.PortalOrigin || actual.AdminOrigin != expected.AdminOrigin || actual.TLSCertificateFile != expected.TLSCertificateFile || actual.TLSPrivateKeyFile != expected.TLSPrivateKeyFile || !sameProxySet(actual.TrustedProxyCIDRs, expected.TrustedProxyCIDRs) {
		return errors.New(transportMismatch)
	}
	if (configuration.MachineServer == nil) != (machine == nil) || machine != nil && *configuration.MachineServer != *machine {
		return errors.New(transportMismatch)
	}
	tlsConfiguration, e := configuration.TLSConfig()
	if e != nil || (expected.TransportMode == config.TransportDirectHTTPS) != (tlsConfiguration != nil) {
		return errors.New(configurationInvalid)
	}
	machineTLS, e := configuration.MachineTLSConfig()
	if e != nil || (machine != nil) != (machineTLS != nil) {
		return errors.New(configurationInvalid)
	}
	return nil
}
