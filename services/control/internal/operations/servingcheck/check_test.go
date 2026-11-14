package servingcheck

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// All accounts, references, certificates and keys are explicit synthetic test
// inputs. These controls open no database, listener, provider or serving owner.
func syntheticServingConfiguration(t *testing.T, mode config.TransportMode) config.Config {
	t.Helper()
	c, e := config.Decode([]byte(`server:
  transport_mode: http
  listen_address: 127.0.0.1:18080
  portal_origin: http://portal.example.invalid
  admin_origin: http://admin.example.invalid
database:
  dsn_file: /synthetic-serving-unavailable/database
  max_open_connections: 4
  max_idle_connections: 1
  statement_timeout_seconds: 5
security:
  auth_hmac_key_file: /synthetic-serving-unavailable/hmac
  mail_keyring_file: /synthetic-serving-unavailable/mail
  password_blocklist_file: /synthetic-serving-unavailable/blocklist
smtp:
  host: smtp.example.invalid
  port: 587
  from_address: synthetic-serving@example.invalid
`))
	if e != nil {
		t.Fatal("synthetic complete configuration", e)
	}
	c.Server.TransportMode = mode
	if mode != config.TransportHTTP {
		c.Server.ListenAddress = "127.0.0.1:18443"
		c.Server.PortalOrigin = "https://portal.example.invalid"
		c.Server.AdminOrigin = "https://admin.example.invalid"
	}
	if mode == config.TransportDirectHTTPS {
		c.Server.TLSCertificateFile = "/synthetic-serving-unavailable/certificate.pem"
		c.Server.TLSPrivateKeyFile = "/synthetic-serving-unavailable/private-key.pem"
	}
	if mode == config.TransportTrustedProxyHTTPS {
		c.Server.TrustedProxyCIDRs = []string{"127.0.0.1/32", "::1/128"}
	}
	if e := c.Validate(); e != nil {
		t.Fatal("synthetic typed transport configuration", e)
	}
	return c
}

func syntheticExpected(c config.Config) Expected {
	return Expected{FormatVersion: expectedFormatVersion, TransportMode: c.Server.EffectiveTransportMode(), ListenAddress: c.Server.ListenAddress,
		PortalOrigin: c.Server.PortalOrigin, AdminOrigin: c.Server.AdminOrigin,
		TLSCertificateFile: c.Server.TLSCertificateFile, TLSPrivateKeyFile: c.Server.TLSPrivateKeyFile,
		TrustedProxyCIDRs: append([]string{}, c.Server.TrustedProxyCIDRs...)}
}

func servingCheckFailure(t *testing.T, e error, expected string) {
	t.Helper()
	if e == nil || e.Error() != expected || errors.Unwrap(e) != nil {
		t.Fatalf("expected fixed redacted %s, observed %v", expected, e)
	}
}

func syntheticExpectedBytes(t *testing.T, expected Expected) []byte {
	t.Helper()
	raw, e := json.Marshal(expected)
	if e != nil {
		t.Fatal("synthetic public expectation", e)
	}
	return raw
}

func TestServingCheckStrictExpectedTransportContract(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run("valid_"+string(mode), func(t *testing.T) {
			expected := syntheticExpected(syntheticServingConfiguration(t, mode))
			raw := syntheticExpectedBytes(t, expected)
			before := append([]byte{}, raw...)
			actual, e := ParseExpected(raw)
			if e != nil || !reflect.DeepEqual(actual, expected) || !bytes.Equal(raw, before) {
				t.Fatal("strict parse changed public intent or opened missing TLS references", e)
			}
		})
	}
	valid := syntheticExpected(syntheticServingConfiguration(t, config.TransportHTTP))
	good := syntheticExpectedBytes(t, valid)
	for _, field := range []string{"format_version", "transport_mode", "listen_address", "portal_origin", "admin_origin", "tls_certificate_file", "tls_private_key_file", "trusted_proxy_cidrs"} {
		t.Run("required_"+field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(good, &fields); e != nil {
				t.Fatal(e)
			}
			delete(fields, field)
			raw, e := json.Marshal(fields)
			if e != nil {
				t.Fatal(e)
			}
			actual, e := ParseExpected(raw)
			servingCheckFailure(t, e, expectationInvalid)
			if !reflect.DeepEqual(actual, Expected{}) {
				t.Fatal("missing field projected partial intent")
			}
		})
	}
	for name, raw := range map[string][]byte{
		"empty": nil, "null": []byte(`null`), "array": []byte(`[]`),
		"unknown":            append([]byte(`{"unknown_sensitive":"synthetic-sensitive-value",`), good[1:]...),
		"duplicate":          bytes.Replace(good, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1),
		"wrong_case":         bytes.Replace(good, []byte(`"format_version"`), []byte(`"FORMAT_VERSION"`), 1),
		"null_field":         bytes.Replace(good, []byte(`"tls_private_key_file":""`), []byte(`"tls_private_key_file":null`), 1),
		"null_proxies":       bytes.Replace(good, []byte(`"trusted_proxy_cidrs":[]`), []byte(`"trusted_proxy_cidrs":null`), 1),
		"wrong_version_type": bytes.Replace(good, []byte(`"format_version":1`), []byte(`"format_version":"1"`), 1),
		"fraction_version":   bytes.Replace(good, []byte(`"format_version":1`), []byte(`"format_version":1.0`), 1),
		"wrong_proxy_type":   bytes.Replace(good, []byte(`"trusted_proxy_cidrs":[]`), []byte(`"trusted_proxy_cidrs":false`), 1),
		"trailing":           append(append([]byte{}, good...), []byte(` {}`)...),
		"oversized":          append(append([]byte{}, good...), bytes.Repeat([]byte(" "), MaximumExpectedBytes+1-len(good))...),
	} {
		t.Run(name, func(t *testing.T) {
			actual, e := ParseExpected(raw)
			servingCheckFailure(t, e, expectationInvalid)
			if !reflect.DeepEqual(actual, Expected{}) || strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("invalid JSON projected partial intent or leaked input")
			}
		})
	}
	t.Run("inclusive_size_bound", func(t *testing.T) {
		raw := append(append([]byte{}, good...), bytes.Repeat([]byte(" "), MaximumExpectedBytes-len(good))...)
		if actual, e := ParseExpected(raw); e != nil || !reflect.DeepEqual(actual, valid) {
			t.Fatal("inclusive expectation bound refused", e)
		}
	})
	for name, change := range map[string]func(*Expected){
		"version":         func(e *Expected) { e.FormatVersion = 2 },
		"missing_mode":    func(e *Expected) { e.TransportMode = "" },
		"unknown_mode":    func(e *Expected) { e.TransportMode = "https" },
		"bad_origin":      func(e *Expected) { e.PortalOrigin = "http://0.0.0.0" },
		"same_origin":     func(e *Expected) { e.AdminOrigin = e.PortalOrigin },
		"bad_listener":    func(e *Expected) { e.ListenAddress = "127.0.0.1:080" },
		"nil_proxy_array": func(e *Expected) { e.TrustedProxyCIDRs = nil },
		"http_proxy":      func(e *Expected) { e.TrustedProxyCIDRs = []string{"127.0.0.1/32"} },
		"http_tls":        func(e *Expected) { e.TLSCertificateFile = "/synthetic-serving-unavailable/cert.pem" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			change(&candidate)
			servingCheckFailure(t, candidate.Validate(), expectationInvalid)
		})
	}
	proxy := syntheticExpected(syntheticServingConfiguration(t, config.TransportTrustedProxyHTTPS))
	for name, values := range map[string][]string{"empty": {}, "duplicate": {"127.0.0.1/32", "127.0.0.1/32"}, "unmasked": {"10.0.0.1/24"}, "wildcard": {"0.0.0.0/0"}, "mapped": {"::ffff:127.0.0.1/128"}} {
		t.Run("proxy_"+name, func(t *testing.T) {
			candidate := proxy
			candidate.TrustedProxyCIDRs = values
			servingCheckFailure(t, candidate.Validate(), expectationInvalid)
		})
	}
	t.Run("direct_references_required_without_io", func(t *testing.T) {
		candidate := syntheticExpected(syntheticServingConfiguration(t, config.TransportDirectHTTPS))
		candidate.TLSCertificateFile, candidate.TLSPrivateKeyFile = "", ""
		servingCheckFailure(t, candidate.Validate(), expectationInvalid)
	})
}

func syntheticServingPair(t *testing.T, configuration *config.Config, names []string, ips []net.IP, expired bool) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	public, private := filepath.Join(base, "public"), filepath.Join(base, "private")
	if e = os.Mkdir(public, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(public, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal(e)
	}
	serial.Add(serial, big.NewInt(1))
	before, after := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	if expired {
		before, after = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	}
	template := &x509.Certificate{SerialNumber: serial, NotBefore: before, NotAfter: after, DNSNames: names, IPAddresses: ips, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	certificate, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	privateKey, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	configuration.Server.TLSCertificateFile = filepath.Join(public, "certificate.pem")
	configuration.Server.TLSPrivateKeyFile = filepath.Join(private, "private-key.pem")
	if e = os.WriteFile(configuration.Server.TLSCertificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0644); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configuration.Server.TLSPrivateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestServingCheckMatchesEffectiveConfiguration(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			configuration := syntheticServingConfiguration(t, mode)
			if mode == config.TransportDirectHTTPS {
				syntheticServingPair(t, &configuration, []string{"portal.example.invalid", "admin.example.invalid"}, nil, false)
			}
			expected := syntheticExpected(configuration)
			before := syntheticExpectedBytes(t, expected)
			if e := Verify(configuration, expected); e != nil {
				t.Fatal("matching effective configuration refused", e)
			}
			if !bytes.Equal(before, syntheticExpectedBytes(t, expected)) {
				t.Fatal("verification mutated public intent")
			}
			if mode == config.TransportDirectHTTPS {
				configuration.Server.TransportMode = ""
				if e := Verify(configuration, expected); e != nil {
					t.Fatal("legacy zero mode lost effective direct HTTPS", e)
				}
				if e := os.Chmod(configuration.Server.TLSCertificateFile, 0444); e != nil {
					t.Fatal(e)
				}
				if e := os.Chmod(configuration.Server.TLSPrivateKeyFile, 0400); e != nil {
					t.Fatal(e)
				}
				if e := Verify(configuration, expected); e != nil {
					t.Fatal("real read-only public certificate/private key refused", e)
				}
			}
			if mode == config.TransportTrustedProxyHTTPS {
				expected.TrustedProxyCIDRs = []string{"::1/128", "127.0.0.1/32"}
				if e := Verify(configuration, expected); e != nil || configuration.Server.TrustedProxyCIDRs[0] != "127.0.0.1/32" || expected.TrustedProxyCIDRs[0] != "::1/128" {
					t.Fatal("proxy set ordering gained authority or input was sorted", e)
				}
			}
		})
	}
	t.Run("real_ip_sans", func(t *testing.T) {
		configuration := syntheticServingConfiguration(t, config.TransportDirectHTTPS)
		configuration.Server.PortalOrigin, configuration.Server.AdminOrigin = "https://127.0.0.1:19443", "https://[::1]:19443"
		syntheticServingPair(t, &configuration, nil, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, false)
		if e := Verify(configuration, syntheticExpected(configuration)); e != nil {
			t.Fatal("real IP SAN pair refused", e)
		}
	})
}

func TestServingCheckRejectsMismatchedFieldsAndUnsafeMaterial(t *testing.T) {
	// Missing but canonical material proves that field mismatches return before
	// TLSConfig opens references. A premature read returns configuration error.
	for name, change := range map[string]func(*Expected){
		"listen_address":       func(e *Expected) { e.ListenAddress = "127.0.0.1:19443" },
		"portal_origin":        func(e *Expected) { e.PortalOrigin = "https://other.portal.example.invalid" },
		"admin_origin":         func(e *Expected) { e.AdminOrigin = "https://other.admin.example.invalid" },
		"tls_certificate_file": func(e *Expected) { e.TLSCertificateFile = "/synthetic-serving-unavailable/other-certificate.pem" },
		"tls_private_key_file": func(e *Expected) { e.TLSPrivateKeyFile = "/synthetic-serving-unavailable/other-private-key.pem" },
		"transport_mode": func(e *Expected) {
			e.TransportMode = config.TransportHTTP
			e.PortalOrigin = "http://portal.example.invalid"
			e.AdminOrigin = "http://admin.example.invalid"
			e.TLSCertificateFile = ""
			e.TLSPrivateKeyFile = ""
		},
	} {
		t.Run("mismatch_"+name, func(t *testing.T) {
			configuration := syntheticServingConfiguration(t, config.TransportDirectHTTPS)
			expected := syntheticExpected(configuration)
			change(&expected)
			servingCheckFailure(t, Verify(configuration, expected), transportMismatch)
		})
	}
	for name, prefixes := range map[string][]string{"missing_member": {"127.0.0.1/32"}, "different_member": {"127.0.0.1/32", "10.0.0.0/8"}} {
		t.Run("mismatch_trusted_proxy_cidrs_"+name, func(t *testing.T) {
			configuration := syntheticServingConfiguration(t, config.TransportTrustedProxyHTTPS)
			expected := syntheticExpected(configuration)
			expected.TrustedProxyCIDRs = prefixes
			servingCheckFailure(t, Verify(configuration, expected), transportMismatch)
		})
	}
	t.Run("revalidates_full_configuration", func(t *testing.T) {
		configuration := syntheticServingConfiguration(t, config.TransportHTTP)
		expected := syntheticExpected(configuration)
		configuration.Database.MaxOpenConnections = 0
		servingCheckFailure(t, Verify(configuration, expected), configurationInvalid)
	})
	t.Run("revalidates_direct_owner_expectation", func(t *testing.T) {
		configuration := syntheticServingConfiguration(t, config.TransportHTTP)
		expected := syntheticExpected(configuration)
		expected.FormatVersion = 0
		servingCheckFailure(t, Verify(configuration, expected), expectationInvalid)
	})
	for _, name := range []string{"missing_pair", "expired", "wrong_dns_san", "wrong_ip_san", "mismatched_pair", "public_writable_certificate", "group_readable_key", "symlink_key", "hardlink_key", "oversized_certificate"} {
		t.Run(name, func(t *testing.T) {
			configuration := syntheticServingConfiguration(t, config.TransportDirectHTTPS)
			if name != "missing_pair" {
				names, ips := []string{"portal.example.invalid", "admin.example.invalid"}, []net.IP(nil)
				if name == "wrong_dns_san" {
					names = []string{"portal.example.invalid"}
				}
				if name == "wrong_ip_san" {
					configuration.Server.PortalOrigin, configuration.Server.AdminOrigin = "https://127.0.0.1", "https://[::1]"
					ips = []net.IP{net.ParseIP("127.0.0.1")}
				}
				syntheticServingPair(t, &configuration, names, ips, name == "expired")
			}
			switch name {
			case "mismatched_pair":
				other := syntheticServingConfiguration(t, config.TransportDirectHTTPS)
				syntheticServingPair(t, &other, []string{"portal.example.invalid", "admin.example.invalid"}, nil, false)
				configuration.Server.TLSPrivateKeyFile = other.Server.TLSPrivateKeyFile
			case "public_writable_certificate":
				if e := os.Chmod(configuration.Server.TLSCertificateFile, 0664); e != nil {
					t.Fatal(e)
				}
			case "group_readable_key":
				if e := os.Chmod(configuration.Server.TLSPrivateKeyFile, 0640); e != nil {
					t.Fatal(e)
				}
			case "symlink_key":
				link := configuration.Server.TLSPrivateKeyFile + ".link"
				if e := os.Symlink(filepath.Base(configuration.Server.TLSPrivateKeyFile), link); e != nil {
					t.Fatal(e)
				}
				configuration.Server.TLSPrivateKeyFile = link
			case "hardlink_key":
				if e := os.Link(configuration.Server.TLSPrivateKeyFile, configuration.Server.TLSPrivateKeyFile+".unapproved"); e != nil {
					t.Fatal(e)
				}
			case "oversized_certificate":
				if e := os.WriteFile(configuration.Server.TLSCertificateFile, bytes.Repeat([]byte("x"), (1<<20)+1), 0644); e != nil {
					t.Fatal(e)
				}
			}
			servingCheckFailure(t, Verify(configuration, syntheticExpected(configuration)), configurationInvalid)
		})
	}
}
