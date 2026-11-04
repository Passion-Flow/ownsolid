package config

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

type TransportMode string

const (
	TransportHTTP              TransportMode = "http"
	TransportDirectHTTPS       TransportMode = "direct_https"
	TransportTrustedProxyHTTPS TransportMode = "trusted_proxy_https"

	maximumBrowserOriginBytes          = 2048
	maximumServerReferenceBytes        = 1024
	browserHTTPPort             uint16 = 80
	browserHTTPSPort            uint16 = 443

	transportModeEnvironment  = "PROJECT_ONE_TRANSPORT_MODE"
	listenAddressEnvironment  = "PROJECT_ONE_LISTEN_ADDRESS"
	portalOriginEnvironment   = "PROJECT_ONE_PORTAL_ORIGIN"
	adminOriginEnvironment    = "PROJECT_ONE_ADMIN_ORIGIN"
	tlsCertificateEnvironment = "PROJECT_ONE_TLS_CERTIFICATE_FILE"
	tlsPrivateKeyEnvironment  = "PROJECT_ONE_TLS_PRIVATE_KEY_FILE"
	trustedProxyEnvironment   = "PROJECT_ONE_TRUSTED_PROXY_CIDRS"
)

// ServerConfiguration is the sole control authority for browser origins and transport.
type ServerConfiguration struct {
	ListenAddress      string        `yaml:"listen_address"`
	PortalOrigin       string        `yaml:"portal_origin"`
	AdminOrigin        string        `yaml:"admin_origin"`
	TLSCertificateFile string        `yaml:"tls_certificate_file"`
	TLSPrivateKeyFile  string        `yaml:"tls_private_key_file"`
	PortalAssets       string        `yaml:"portal_assets"`
	AdminAssets        string        `yaml:"admin_assets"`
	TransportMode      TransportMode `yaml:"transport_mode,omitempty"`
	TrustedProxyCIDRs  []string      `yaml:"trusted_proxy_cidrs,omitempty"`
}

// BrowserOrigin retains the selected canonical authority; Hostname excludes brackets and port.
type BrowserOrigin struct {
	Scheme    string
	Authority string
	Hostname  string
	Port      uint16
}

func (s ServerConfiguration) EffectiveTransportMode() TransportMode {
	if s.TransportMode == "" {
		return TransportDirectHTTPS
	}
	return s.TransportMode
}

func canonicalBrowserHostname(host string) bool {
	if host == "" || len(host) > 253 || strings.ToLower(host) != host {
		return false
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		return ip.Zone() == "" && !ip.Is4In6() && ip.String() == host && (ip.IsGlobalUnicast() || ip.IsLoopback())
	}
	labels := strings.Split(host, ".")
	// Browsers interpret numeric final labels using IPv4 URL rules, not ordinary DNS.
	last := labels[len(labels)-1]
	decimal := last != ""
	for _, ch := range last {
		decimal = decimal && ch >= '0' && ch <= '9'
	}
	if decimal {
		return false
	}
	hexadecimal := strings.HasPrefix(last, "0x")
	if hexadecimal {
		for _, ch := range last[2:] {
			hexadecimal = hexadecimal && (ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f')
		}
		if hexadecimal {
			return false
		}
	}
	numeric := true
	for _, ch := range last {
		if ch < '0' || ch > '9' {
			numeric = false
		}
	}
	if numeric {
		return false
	}
	if strings.HasPrefix(last, "0x") {
		hexadecimal := true
		for _, ch := range last[2:] {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
				hexadecimal = false
			}
		}
		if hexadecimal {
			return false
		}
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

// ParseBrowserOrigin rejects noncanonical input rather than changing a deployment address.
func ParseBrowserOrigin(raw string) (BrowserOrigin, error) {
	bad := errors.New("ORIGIN_CONFIG_INVALID")
	if raw == "" || len(raw) > maximumBrowserOriginBytes || strings.ContainsAny(raw, "\\%?#") || strings.ContainsFunc(raw, func(ch rune) bool { return unicode.IsSpace(ch) || unicode.IsControl(ch) }) {
		return BrowserOrigin{}, bad
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != raw {
		return BrowserOrigin{}, bad
	}
	host := u.Hostname()
	if !canonicalBrowserHostname(host) {
		return BrowserOrigin{}, bad
	}
	port := browserHTTPPort
	if u.Scheme == "https" {
		port = browserHTTPSPort
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if value := u.Port(); value != "" {
		n, e := strconv.ParseUint(value, 10, 16)
		if e != nil || n == 0 || uint16(n) == port || strconv.FormatUint(n, 10) != value {
			return BrowserOrigin{}, bad
		}
		port = uint16(n)
		authority = net.JoinHostPort(host, value)
	}
	if u.Host != authority {
		return BrowserOrigin{}, bad
	}
	return BrowserOrigin{Scheme: u.Scheme, Authority: authority, Hostname: host, Port: port}, nil
}

func serverReference(value string) bool {
	return len(value) <= maximumServerReferenceBytes && filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validateServerListener(value string) error {
	bad := errors.New("LISTEN_CONFIG_INVALID")
	if strings.ContainsFunc(value, func(ch rune) bool { return unicode.IsSpace(ch) || unicode.IsControl(ch) }) || strings.ContainsAny(value, "\\%/?#@") {
		return bad
	}
	host, port, e := net.SplitHostPort(value)
	if e != nil || host == "" {
		return bad
	}
	n, e := strconv.ParseUint(port, 10, 16)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != port {
		return bad
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		if ip.Zone() != "" || ip.Is4In6() || ip.String() != host || !(ip.IsUnspecified() || ip.IsGlobalUnicast() || ip.IsLoopback()) {
			return bad
		}
	} else if !canonicalBrowserHostname(host) {
		return bad
	}
	if value != net.JoinHostPort(host, port) {
		return bad
	}
	return nil
}

// ValidateTransport validates structure only; protected files are opened at serving startup.
func (s ServerConfiguration) ValidateTransport() error {
	bad := errors.New("CONFIG_TRANSPORT_INVALID")
	mode := s.EffectiveTransportMode()
	if mode != TransportHTTP && mode != TransportDirectHTTPS && mode != TransportTrustedProxyHTTPS {
		return bad
	}
	portal, e := ParseBrowserOrigin(s.PortalOrigin)
	if e != nil {
		return e
	}
	admin, e := ParseBrowserOrigin(s.AdminOrigin)
	if e != nil {
		return e
	}
	if portal == admin {
		return errors.New("ORIGINS_MUST_DIFFER")
	}
	wantedScheme := "https"
	if mode == TransportHTTP {
		wantedScheme = "http"
	}
	if portal.Scheme != wantedScheme || admin.Scheme != wantedScheme {
		return bad
	}
	if e := validateServerListener(s.ListenAddress); e != nil {
		return e
	}
	if mode == TransportDirectHTTPS {
		// Legacy recovery/test snapshots may omit both references; TLSConfig still requires actual material.
		if s.TrustedProxyCIDRs != nil || (s.TLSCertificateFile == "") != (s.TLSPrivateKeyFile == "") {
			return bad
		}
		if s.TLSCertificateFile != "" && (!serverReference(s.TLSCertificateFile) || !serverReference(s.TLSPrivateKeyFile) || s.TLSCertificateFile == s.TLSPrivateKeyFile) {
			return bad
		}
		return nil
	}
	if s.TLSCertificateFile != "" || s.TLSPrivateKeyFile != "" {
		return bad
	}
	if mode == TransportHTTP {
		if s.TrustedProxyCIDRs != nil {
			return bad
		}
		return nil
	}
	if len(s.TrustedProxyCIDRs) == 0 {
		return bad
	}
	seen := make(map[netip.Prefix]bool)
	for _, raw := range s.TrustedProxyCIDRs {
		prefix, e := netip.ParsePrefix(raw)
		if e != nil || prefix.String() != raw || prefix.Masked() != prefix || prefix.Bits() == 0 || prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() || !(prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsLoopback()) || prefix.Contains(netip.MustParseAddr("224.0.0.0")) || prefix.Contains(netip.MustParseAddr("ff00::")) || seen[prefix] {
			return bad
		}
		seen[prefix] = true
	}
	return nil
}

func (c Config) validateServerTransport() error {
	if e := c.Topology.Validate(); e != nil {
		return e
	}
	if e := c.validateComponentReferences(); e != nil {
		return e
	}
	if e := c.Server.ValidateTransport(); e != nil {
		return e
	}
	if c.SSO.Enabled && c.Server.EffectiveTransportMode() == TransportHTTP {
		return errors.New("CONFIG_TRANSPORT_INVALID")
	}
	return c.validateMachineServer()
}

func (s *ServerConfiguration) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_TRANSPORT_INVALID")
	if strictKnowledgeYAML(node, reflect.TypeOf(*s)) != nil {
		return bad
	}
	type plain ServerConfiguration
	var value plain
	if node.Decode(&value) != nil {
		return bad
	}
	for i := 0; i < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "transport_mode":
			if value.TransportMode == "" {
				return bad
			}
		case "trusted_proxy_cidrs":
			if len(value.TrustedProxyCIDRs) == 0 {
				return bad
			}
		}
	}
	*s = ServerConfiguration(value)
	return nil
}

// Null server groups bypass UnmarshalYAML in the YAML library, so inspect them before decoding.
func validateServerTransportYAML(raw []byte) error {
	var node yaml.Node
	bad := errors.New("CONFIG_TRANSPORT_INVALID")
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return bad
	}
	root := node.Content[0]
	seenMachine := false
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "server" && strictKnowledgeYAML(root.Content[i+1], reflect.TypeOf(ServerConfiguration{})) != nil {
			return bad
		}
		if root.Content[i].Value == "machine_server" {
			if seenMachine || root.Content[i].Tag != "!!str" || strictKnowledgeYAML(root.Content[i+1], reflect.TypeOf(MachineServerConfiguration{})) != nil {
				return errors.New("CONFIG_MACHINE_SERVER_INVALID")
			}
			seenMachine = true
		}
	}
	return nil
}

func serverTransportEnvironment(key string) bool {
	switch key {
	case transportModeEnvironment, listenAddressEnvironment, portalOriginEnvironment, adminOriginEnvironment, tlsCertificateEnvironment, tlsPrivateKeyEnvironment, trustedProxyEnvironment:
		return true
	}
	return false
}

func (s *ServerConfiguration) applyTransportEnvironment(key, value string) error {
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if value == "" {
		return bad
	}
	switch key {
	case transportModeEnvironment:
		s.TransportMode = TransportMode(value)
	case listenAddressEnvironment:
		s.ListenAddress = value
	case portalOriginEnvironment:
		s.PortalOrigin = value
	case adminOriginEnvironment:
		s.AdminOrigin = value
	case tlsCertificateEnvironment:
		s.TLSCertificateFile = value
	case tlsPrivateKeyEnvironment:
		s.TLSPrivateKeyFile = value
	case trustedProxyEnvironment:
		var prefixes []string
		if json.Unmarshal([]byte(value), &prefixes) != nil || len(prefixes) == 0 {
			return bad
		}
		s.TrustedProxyCIDRs = prefixes
	default:
		return bad
	}
	return nil
}
