package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func transportTestServer(mode TransportMode) ServerConfiguration {
	scheme := "https"
	if mode == TransportHTTP {
		scheme = "http"
	}
	s := ServerConfiguration{ListenAddress: "127.0.0.1:18443", PortalOrigin: scheme + "://localhost:18443", AdminOrigin: scheme + "://127.0.0.1:18443", TransportMode: mode}
	if mode == TransportTrustedProxyHTTPS {
		s.TrustedProxyCIDRs = []string{"127.0.0.1/32", "::1/128"}
	}
	return s
}

func transportConfigYAML(t *testing.T, c Config) []byte {
	t.Helper()
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return []byte(strings.Replace(string(raw), "runtime: null\n", "", 1))
}

func TestBrowserOriginCanonicalAddressMatrix(t *testing.T) {
	for _, host := range []string{"portal.example.invalid", "localhost", "127.0.0.1", "192.0.2.10", "::1", "2001:db8::10"} {
		for _, scheme := range []string{"http", "https"} {
			for _, port := range []string{"", "19443"} {
				authority := host
				if strings.Contains(host, ":") {
					authority = "[" + host + "]"
				}
				if port != "" {
					authority = net.JoinHostPort(host, port)
				}
				raw := scheme + "://" + authority
				t.Run(raw, func(t *testing.T) {
					got, e := ParseBrowserOrigin(raw)
					wantedPort := uint16(80)
					if scheme == "https" {
						wantedPort = 443
					}
					if port != "" {
						wantedPort = 19443
					}
					if e != nil || got != (BrowserOrigin{Scheme: scheme, Authority: authority, Hostname: host, Port: wantedPort}) {
						t.Fatal("canonical origin was rewritten or rejected", got, e)
					}
				})
			}
		}
	}
}

func TestBrowserOriginRejectsAmbiguousAndNoncanonicalInputs(t *testing.T) {
	for index, raw := range []string{
		"", "https://", "ftp://localhost", "HTTPS://localhost", "https://LOCALHOST", "https://localhost.",
		"https://localhost/", "https://localhost/path", "https://localhost?", "https://localhost#", "https://localhost?x=1", "https://localhost#token",
		"https://actor:secret@localhost", "https://localhost ", "https://localhost\r\n", "https://local\\host", "https://local%68ost",
		"https://localhost:", "https://localhost:0", "https://localhost:65536", "https://localhost:0443", "https://localhost:443", "http://localhost:80",
		"https://127.1", "https://2130706433", "https://0x7f000001", "https://0x", "https://0127.0.0.1", "https://1.2.3.999", "https://1.2.3.4.5",
		"https://999999999999999999999999999999", "https://0xfffffffffffffffffffffffffffff", "https://0.0.0.0", "https://255.255.255.255", "https://224.0.0.1",
		"https://::1", "https://[::]", "https://[ff02::1]", "https://[fe80::1%25en0]", "https://[::ffff:127.0.0.1]", "https://[2001:0db8::1]", "https://[2001:DB8::1]", "https://[localhost]",
		"https://-portal.invalid", "https://portal-.invalid", "https://portal..invalid", "https://portal_1.invalid", "https://\u7528\u6237.invalid",
		"https://" + strings.Repeat("a", 64) + ".invalid", "https://" + strings.Repeat("a", 2048),
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			got, e := ParseBrowserOrigin(raw)
			if e == nil || e.Error() != "ORIGIN_CONFIG_INVALID" || got != (BrowserOrigin{}) || strings.Contains(e.Error(), "secret") {
				t.Fatal("ambiguous origin accepted or input leaked", got, e)
			}
		})
	}
}

func TestServerTransportModesLegacyDefaultAndStructuralValidation(t *testing.T) {
	for _, mode := range []TransportMode{"", TransportHTTP, TransportDirectHTTPS, TransportTrustedProxyHTTPS} {
		s := transportTestServer(mode)
		if e := s.ValidateTransport(); e != nil {
			t.Fatal("valid mode", mode, e)
		}
		if mode == "" && s.EffectiveTransportMode() != TransportDirectHTTPS {
			t.Fatal("legacy default changed")
		}
		if mode != "" && s.EffectiveTransportMode() != mode {
			t.Fatal("explicit mode changed")
		}
		c := validConfig()
		c.Server = s
		if e := c.Validate(); e != nil {
			t.Fatal("mode did not reach full typed config", mode, e)
		}
	}
	for _, mode := range []TransportMode{TransportHTTP, TransportDirectHTTPS, TransportTrustedProxyHTTPS} {
		for _, host := range []string{"portal.example.invalid", "localhost", "127.0.0.1", "192.0.2.10", "::1", "2001:db8::10"} {
			s := transportTestServer(mode)
			scheme := "https"
			if mode == TransportHTTP {
				scheme = "http"
			}
			s.PortalOrigin = scheme + "://" + net.JoinHostPort(host, "19443")
			s.AdminOrigin = scheme + "://" + net.JoinHostPort(host, "29443")
			if e := s.ValidateTransport(); e != nil {
				t.Fatal("declared mode/address combination", mode, host, e)
			}
		}
	}
	for _, bind := range []string{"0.0.0.0:19443", "[::]:19443", "[::1]:19443", "localhost:19443", "control.example.invalid:19443"} {
		s := transportTestServer(TransportHTTP)
		s.ListenAddress = bind
		if e := s.ValidateTransport(); e != nil {
			t.Fatal("explicit bind rejected", bind, e)
		}
	}
	s := transportTestServer(TransportDirectHTTPS)
	s.TLSCertificateFile = "/not-installed/synthetic-certificate.pem"
	s.TLSPrivateKeyFile = "/not-installed/synthetic-private-key.pem"
	if e := s.ValidateTransport(); e != nil {
		t.Fatal("structure validation unexpectedly opens protected material", e)
	}
}

func TestServerTransportRefusesContradictoryModesTLSAndListeners(t *testing.T) {
	for index, mutate := range []func(*ServerConfiguration){
		func(s *ServerConfiguration) { s.TransportMode = "false" },
		func(s *ServerConfiguration) { s.TransportMode = TransportHTTP },
		func(s *ServerConfiguration) { s.PortalOrigin = "http://localhost:18443" },
		func(s *ServerConfiguration) { s.AdminOrigin = s.PortalOrigin },
		func(s *ServerConfiguration) { s.TrustedProxyCIDRs = []string{} },
		func(s *ServerConfiguration) { s.TrustedProxyCIDRs = []string{"127.0.0.1/32"} },
		func(s *ServerConfiguration) { s.TLSCertificateFile = "/synthetic/certificate.pem" },
		func(s *ServerConfiguration) { s.TLSPrivateKeyFile = "/synthetic/private-key.pem" },
		func(s *ServerConfiguration) {
			s.TLSCertificateFile = "relative-certificate"
			s.TLSPrivateKeyFile = "relative-key"
		},
		func(s *ServerConfiguration) { s.TLSCertificateFile = "/same-file"; s.TLSPrivateKeyFile = "/same-file" },
		func(s *ServerConfiguration) {
			s.TLSCertificateFile = "/synthetic/../certificate"
			s.TLSPrivateKeyFile = "/synthetic/key"
		},
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			s := transportTestServer(TransportDirectHTTPS)
			mutate(&s)
			if e := s.ValidateTransport(); e == nil {
				t.Fatal("contradiction accepted")
			}
		})
	}
	for _, bind := range []string{"", ":18443", "localhost", "localhost:0", "localhost:01", "localhost:65536", "localhost:service", "[::1%en0]:19443", "::1:19443", "[ff02::1]:19443", "127.1:19443", "localhost:19443\n"} {
		s := transportTestServer(TransportHTTP)
		s.ListenAddress = bind
		if e := s.ValidateTransport(); e == nil || e.Error() != "LISTEN_CONFIG_INVALID" {
			t.Fatal("invalid listener", bind, e)
		}
	}
	for _, mode := range []TransportMode{TransportHTTP, TransportTrustedProxyHTTPS} {
		s := transportTestServer(mode)
		s.TLSCertificateFile = "/synthetic/certificate.pem"
		s.TLSPrivateKeyFile = "/synthetic/key.pem"
		if e := s.ValidateTransport(); e == nil {
			t.Fatal("non-direct TLS references ignored", mode)
		}
	}
}

func TestServerTransportCanonicalTrustedProxyCIDRs(t *testing.T) {
	for index, prefixes := range [][]string{
		nil, {}, {"0.0.0.0/0"}, {"::/0"}, {"0.0.0.0/8"}, {"224.0.0.0/4"}, {"ff00::/8"}, {"192.0.0.0/2"}, {"8000::/1"},
		{"127.0.0.1"}, {"127.0.0.1/08"}, {"192.0.2.1/24"}, {"2001:0db8::/32"}, {"2001:DB8::/32"}, {"::ffff:127.0.0.1/128"},
		{"127.0.0.1/32", "127.0.0.1/32"}, {"127.0.0.1/32,::1/128"}, {"sensitive-value"},
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			s := transportTestServer(TransportTrustedProxyHTTPS)
			s.TrustedProxyCIDRs = prefixes
			if e := s.ValidateTransport(); e == nil || e.Error() != "CONFIG_TRANSPORT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("unsafe trust accepted or leaked", e)
			}
		})
	}
	s := transportTestServer(TransportTrustedProxyHTTPS)
	s.TrustedProxyCIDRs = []string{"192.0.2.0/24", "2001:db8::/32", "127.0.0.1/32", "::1/128"}
	if e := s.ValidateTransport(); e != nil {
		t.Fatal("explicit canonical isolated proxy networks", e)
	}
}

func TestServerTransportStrictYAMLBeforeEnvironmentOverride(t *testing.T) {
	base := string(workspaceConfigInput(t))
	for index, extra := range []string{
		"transport_mode: null", "transport_mode: false", "transport_mode: 3", "transport_mode: ''", "transport_mode: invalid-mode",
		"transport_mode: direct_https\n    transport_mode: http", "trusted_proxy_cidrs: null", "trusted_proxy_cidrs: []", "trusted_proxy_cidrs: false",
		"trusted_proxy_cidrs: [null]", "trusted_proxy_cidrs: [false]", "trusted_proxy_cidrs: [127.0.0.1/32]", "unknown_transport_field: sensitive-value",
		"transport_mode: &mode direct_https\n    trusted_proxy_cidrs: [*mode]",
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			raw := []byte(strings.Replace(base, "server:\n", "server:\n    "+extra+"\n", 1))
			env := workspaceEnvironmentFixture(t, transportModeEnvironment+"=direct_https\n")
			if _, e := decodeWithEnvironment(raw, env); e == nil || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("invalid server YAML concealed or exposed", e)
			}
		})
	}
	var node yaml.Node
	if e := yaml.Unmarshal([]byte(base), &node); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < len(node.Content[0].Content); i += 2 {
		if node.Content[0].Content[i].Value == "server" {
			node.Content[0].Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		}
	}
	raw, e := yaml.Marshal(&node)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, transportModeEnvironment+"=direct_https\n")); e == nil {
		t.Fatal("null server accepted")
	}
	for _, mode := range []TransportMode{TransportHTTP, TransportTrustedProxyHTTPS} {
		c := validConfig()
		c.Server = transportTestServer(mode)
		env := portalOriginEnvironment + "=http://localhost:18443\n"
		if mode == TransportHTTP {
			c.Server.PortalOrigin = "https://localhost:18443"
		} else {
			c.Server.TrustedProxyCIDRs = []string{"192.0.2.1/24"}
			env = trustedProxyEnvironment + "='[\"127.0.0.1/32\"]'\n"
		}
		if _, e := decodeWithEnvironment(transportConfigYAML(t, c), workspaceEnvironmentFixture(t, env)); e == nil {
			t.Fatal("invalid base mode repaired by otherwise valid override")
		}
	}
}

func TestServerTransportEnvironmentMappingsAndNoAmbientAuthority(t *testing.T) {
	for _, key := range []string{transportModeEnvironment, listenAddressEnvironment, portalOriginEnvironment, adminOriginEnvironment, tlsCertificateEnvironment, tlsPrivateKeyEnvironment, trustedProxyEnvironment} {
		t.Setenv(key, "sensitive-value")
	}
	base := workspaceConfigInput(t)
	c, e := decodeWithEnvironment(base, "")
	if e != nil || c.Server.EffectiveTransportMode() != TransportDirectHTTPS || c.Server.PortalOrigin != validConfig().Server.PortalOrigin {
		t.Fatal("ambient transport authority", e)
	}
	for _, mode := range []TransportMode{TransportHTTP, TransportDirectHTTPS, TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			scheme := "https"
			if mode == TransportHTTP {
				scheme = "http"
			}
			body := fmt.Sprintf("%s=%s\n%s=[::1]:19443\n%s=%s://localhost:19443\n%s=%s://[::1]:29443\n", transportModeEnvironment, mode, listenAddressEnvironment, portalOriginEnvironment, scheme, adminOriginEnvironment, scheme)
			if mode == TransportDirectHTTPS {
				body += tlsCertificateEnvironment + "=/synthetic/certificate.pem\n" + tlsPrivateKeyEnvironment + "='/synthetic/private-key.pem'\n"
			}
			if mode == TransportTrustedProxyHTTPS {
				body += trustedProxyEnvironment + "='[\"127.0.0.1/32\",\"::1/128\"]'\n"
			}
			got, e := decodeWithEnvironment(base, workspaceEnvironmentFixture(t, body))
			if e != nil || got.Server.EffectiveTransportMode() != mode || got.Server.ListenAddress != "[::1]:19443" || got.Server.PortalOrigin != scheme+"://localhost:19443" || got.Server.AdminOrigin != scheme+"://[::1]:29443" {
				t.Fatal("explicit typed override", e)
			}
			if mode == TransportDirectHTTPS && (got.Server.TLSCertificateFile != "/synthetic/certificate.pem" || got.Server.TLSPrivateKeyFile != "/synthetic/private-key.pem") {
				t.Fatal("TLS reference mapping")
			}
			if mode == TransportTrustedProxyHTTPS && (len(got.Server.TrustedProxyCIDRs) != 2 || got.Server.TrustedProxyCIDRs[1] != "::1/128") {
				t.Fatal("proxy CIDR mapping")
			}
		})
	}
}

func TestServerTransportEnvironmentRejectsInvalidWithoutInputDisclosure(t *testing.T) {
	for index, body := range []string{
		"PROJECT_ONE_TRANSPORT_UNKNOWN=sensitive-value", transportModeEnvironment + "=http\n" + transportModeEnvironment + "=direct_https",
		transportModeEnvironment + "=", transportModeEnvironment + "=false", transportModeEnvironment + "=$(sensitive-value)", transportModeEnvironment + "=\"http\" trailing",
		portalOriginEnvironment + "=https://sensitive-value#token", adminOriginEnvironment + "=https://localhost:8443", listenAddressEnvironment + "=:19443",
		tlsCertificateEnvironment + "=relative-sensitive-value", trustedProxyEnvironment + "=null", trustedProxyEnvironment + "=[]", trustedProxyEnvironment + "=[false]", trustedProxyEnvironment + "=[\"127.0.0.1/32\"] trailing",
		transportModeEnvironment + "=trusted_proxy_https\n" + trustedProxyEnvironment + "=[\"0.0.0.0/0\"]",
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			_, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, body+"\n"))
			if e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("invalid transport override accepted or disclosed", e)
			}
		})
	}
}

func TestServerTransportHTTPRejectsEnabledCurrentSSO(t *testing.T) {
	c := validConfig()
	c.Server = transportTestServer(TransportHTTP)
	c.SSO = SSOConfiguration{Enabled: true, KeyringFile: "/synthetic/sso-keyring.json", Providers: []SSOProvider{{Origin: "https://identity.example.invalid", Issuer: "https://identity.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	if e := c.Validate(); e == nil || e.Error() != "CONFIG_TRANSPORT_INVALID" {
		t.Fatal("HTTP accepted HTTPS-only current SSO", e)
	}
	if _, e := decodeWithEnvironment(transportConfigYAML(t, c), workspaceEnvironmentFixture(t, transportModeEnvironment+"=direct_https\n"+portalOriginEnvironment+"=https://localhost:18443\n"+adminOriginEnvironment+"=https://127.0.0.1:18443\n")); e == nil {
		t.Fatal("override concealed invalid SSO/HTTP base")
	}
	c.SSO.Enabled = false
	if e := c.Validate(); e != nil {
		t.Fatal("HTTP local-account recovery configuration", e)
	}
	c.SSO.Enabled = true
	c.Server = transportTestServer(TransportTrustedProxyHTTPS)
	if e := c.Validate(); e != nil {
		t.Fatal("proxy HTTPS SSO configuration", e)
	}
}

// All keys and certificate bytes below are generated synthetic test material, never deployment assets.
func transportTLSFixture(t *testing.T, dnsNames []string, ips []net.IP, expired bool) (string, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-only"}, DNSNames: dnsNames, IPAddresses: ips, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if expired {
		leaf.NotBefore = now.Add(-2 * time.Hour)
		leaf.NotAfter = now.Add(-time.Hour)
	}
	der, e := x509.CreateCertificate(rand.Reader, leaf, leaf, public, private)
	if e != nil {
		t.Fatal(e)
	}
	key, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		t.Fatal(e)
	}
	certificateFile, keyFile := filepath.Join(root, "synthetic-certificate.pem"), filepath.Join(root, "synthetic-key.pem")
	if e := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); e != nil {
		t.Fatal(e)
	}
	return certificateFile, keyFile
}

func TestServerTransportTLSConfigPreservesActualMaterialValidation(t *testing.T) {
	for _, mode := range []TransportMode{TransportHTTP, TransportTrustedProxyHTTPS} {
		c := validConfig()
		c.Server = transportTestServer(mode)
		got, e := c.TLSConfig()
		if e != nil || got != nil {
			t.Fatal("validated non-direct mode loaded TLS material", mode, e)
		}
		c.Server.TLSPrivateKeyFile = "/must-not-be-opened/synthetic-key"
		if _, e := c.TLSConfig(); e == nil || e.Error() != "CONFIG_TRANSPORT_INVALID" {
			t.Fatal("invalid mode returned nil TLS success", e)
		}
	}
	c := validConfig()
	c.Server = transportTestServer(TransportDirectHTTPS)
	if _, e := c.TLSConfig(); e == nil {
		t.Fatal("direct HTTPS missing material accepted")
	}
	c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile = transportTLSFixture(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	got, e := c.TLSConfig()
	if e != nil || got == nil || len(got.Certificates) != 1 || got.MinVersion != tls.VersionTLS12 {
		t.Fatal("actual synthetic DNS/IP SAN pair", e)
	}
	ipv6 := c
	ipv6.Server.PortalOrigin = "https://[::1]:18443"
	ipv6.Server.AdminOrigin = "https://[2001:db8::10]:28443"
	ipv6.Server.TLSCertificateFile, ipv6.Server.TLSPrivateKeyFile = transportTLSFixture(t, nil, []net.IP{net.ParseIP("::1"), net.ParseIP("2001:db8::10")}, false)
	if got, e := ipv6.TLSConfig(); e != nil || got == nil {
		t.Fatal("actual synthetic IPv6 SAN pair", e)
	}
	c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile = transportTLSFixture(t, []string{"localhost"}, nil, false)
	if _, e := c.TLSConfig(); e == nil || e.Error() != "TLS_HOSTNAME_INVALID" {
		t.Fatal("missing IP SAN accepted", e)
	}
	c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile = transportTLSFixture(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, true)
	if _, e := c.TLSConfig(); e == nil || e.Error() != "TLS_CERTIFICATE_INVALID" {
		t.Fatal("expired certificate accepted", e)
	}
	c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile = transportTLSFixture(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	if e := os.Chmod(c.Server.TLSPrivateKeyFile, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := c.TLSConfig(); e == nil || e.Error() != "SECRET_FILE_INVALID" {
		t.Fatal("public private-key material accepted", e)
	}
}
