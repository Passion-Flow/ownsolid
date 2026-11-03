package tlsidentity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func identityFixture(t *testing.T, mutate func(*x509.Certificate), intermediate bool) ([]byte, []byte, []byte, time.Time) {
	return identityFixtureWithAuthority(t, mutate, intermediate, "Project-one isolated identity root")
}

func TestMachineServingIdentitySingleOriginSharedVerification(t *testing.T) {
	for _, intermediate := range []bool{false, true} {
		for _, item := range []struct{ name, origin, host string }{
			{"dns", "https://machine.example.test:19443", "machine.example.test"},
			{"localhost", "https://localhost:19443", "localhost"},
			{"ipv4", "https://127.0.0.1:19443", "127.0.0.1"},
			{"ipv6", "https://[::1]:19443", "::1"},
		} {
			name := item.name + "_root"
			if intermediate {
				name = item.name + "_intermediate"
			}
			t.Run(name, func(t *testing.T) {
				certificate, key, trust, now := identityFixture(t, func(c *x509.Certificate) { c.DNSNames = []string{"machine.example.test", "localhost"} }, intermediate)
				identity, e := ValidateMachine(certificate, key, trust, item.origin, now)
				if e != nil || len(identity.Hosts) != 1 || identity.Hosts[0] != item.host {
					t.Fatal("explicit synthetic machine identity rejected", e)
				}
				hosts, e := MachineOriginHosts(item.origin)
				if e != nil || !reflect.DeepEqual(hosts, identity.Hosts) {
					t.Fatal("machine metadata identity differs from PKI admission")
				}
				legacy, e := Validate(certificate, key, trust, []string{item.origin, item.origin}, now)
				if e != nil || !reflect.DeepEqual(identity, legacy) {
					t.Fatal("machine path diverged from the existing shared cryptographic verification")
				}
				if _, e := Validate(certificate, key, trust, []string{"https://portal.example.test", "https://admin.example.test"}, now); e == nil {
					t.Fatal("machine SAN was treated as browser entrance authority")
				}
				raw, e := json.Marshal(identity)
				if e != nil || bytes.Contains(raw, key) || strings.Contains(string(raw), "PRIVATE KEY") {
					t.Fatal("private key entered machine identity output")
				}
			})
		}
	}
}

func TestMachineServingIdentityRefusesUnsafeInputs(t *testing.T) {
	certificate, key, trust, now := identityFixture(t, func(c *x509.Certificate) { c.DNSNames = []string{"machine.example.test"} }, false)
	_, otherKey, otherTrust, _ := identityFixture(t, nil, false)
	for _, fault := range []string{"wrong_key", "wrong_trust", "wrong_dns", "wrong_ip", "expired", "future", "zero_time", "client_only", "ca_leaf", "no_san", "certificate_prefix", "certificate_suffix", "trust_prefix", "empty_trust", "leaf_as_trust", "key_prefix", "key_suffix", "extra_key", "malformed_key", "empty_origin", "http", "userinfo", "path", "query", "fragment"} {
		t.Run(fault, func(t *testing.T) {
			selected, private, roots, at := certificate, key, trust, now
			origin := "https://machine.example.test:19443"
			switch fault {
			case "wrong_key":
				private = otherKey
			case "wrong_trust":
				roots = otherTrust
			case "wrong_dns":
				origin = "https://foreign.example.test:19443"
			case "wrong_ip":
				origin = "https://192.0.2.1:19443"
			case "expired":
				at = now.Add(25 * time.Hour)
			case "future":
				at = now.Add(-time.Hour)
			case "zero_time":
				at = time.Time{}
			case "client_only":
				selected, private, roots, _ = identityFixture(t, func(c *x509.Certificate) {
					c.DNSNames = []string{"machine.example.test"}
					c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
				}, false)
			case "ca_leaf":
				selected, private, roots, _ = identityFixture(t, func(c *x509.Certificate) {
					c.DNSNames = []string{"machine.example.test"}
					c.IsCA = true
					c.KeyUsage |= x509.KeyUsageCertSign
				}, false)
			case "no_san":
				selected, private, roots, _ = identityFixture(t, func(c *x509.Certificate) {
					c.DNSNames = nil
					c.IPAddresses = nil
					c.Subject.CommonName = "machine.example.test"
				}, false)
			case "certificate_prefix":
				selected = append([]byte("synthetic-unprinted-junk"), certificate...)
			case "certificate_suffix":
				selected = append(append([]byte(nil), certificate...), []byte("synthetic-unprinted-junk")...)
			case "trust_prefix":
				roots = append([]byte("synthetic-unprinted-junk"), trust...)
			case "empty_trust":
				roots = nil
			case "leaf_as_trust":
				roots = certificate
			case "key_prefix":
				private = append([]byte("synthetic-unprinted-junk"), key...)
			case "key_suffix":
				private = append(append([]byte(nil), key...), []byte("synthetic-unprinted-junk")...)
			case "extra_key":
				private = append(append([]byte(nil), key...), otherKey...)
			case "malformed_key":
				private = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("synthetic-invalid-key")})
			case "empty_origin":
				origin = ""
			case "http":
				origin = "http://machine.example.test"
			case "userinfo":
				origin = "https://synthetic-user@machine.example.test"
			case "path":
				origin += "/health/ready"
			case "query":
				origin += "?synthetic=true"
			case "fragment":
				origin += "#synthetic"
			}
			identity, e := ValidateMachine(selected, private, roots, origin, at)
			if e == nil || e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" || !reflect.DeepEqual(identity, Identity{}) {
				t.Fatal("unsafe machine identity returned data or a non-redacted error")
			}
			if fault == "empty_origin" || fault == "http" || fault == "userinfo" || fault == "path" || fault == "query" || fault == "fragment" {
				if hosts, e := MachineOriginHosts(origin); e == nil || hosts != nil || e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" {
					t.Fatal("metadata address path accepted an unsafe origin")
				}
			}
		})
	}
}

func TestServingIdentityExactTwoOriginContractRemains(t *testing.T) {
	certificate, key, trust, now := identityFixture(t, nil, false)
	for _, item := range []struct {
		name    string
		origins []string
	}{
		{"nil", nil},
		{"empty", []string{}},
		{"one", []string{"https://portal.example.test"}},
		{"three", []string{"https://portal.example.test", "https://admin.example.test", "https://127.0.0.1"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			if hosts, e := OriginHosts(item.origins); e == nil || hosts != nil || e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" {
				t.Fatal("legacy origin arity was widened")
			}
			if identity, e := Validate(certificate, key, trust, item.origins, now); e == nil || !reflect.DeepEqual(identity, Identity{}) {
				t.Fatal("legacy PKI arity was widened")
			}
		})
	}
	for _, item := range []struct {
		name           string
		origins, hosts []string
	}{
		{"two_distinct", []string{"https://portal.example.test:18443", "https://admin.example.test:18444"}, []string{"admin.example.test", "portal.example.test"}},
		{"two_shared_host", []string{"https://127.0.0.1:18443", "https://127.0.0.1:18444"}, []string{"127.0.0.1"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			hosts, e := OriginHosts(item.origins)
			if e != nil || !reflect.DeepEqual(hosts, item.hosts) {
				t.Fatal("legacy two-origin host order/deduplication changed")
			}
			identity, e := Validate(certificate, key, trust, item.origins, now)
			if e != nil || !reflect.DeepEqual(identity.Hosts, item.hosts) {
				t.Fatal("legacy exact-two serving identity changed", e)
			}
		})
	}
}
func identityFixtureWithAuthority(t *testing.T, mutate func(*x509.Certificate), intermediate bool, authority string) ([]byte, []byte, []byte, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	key := func() *ecdsa.PrivateKey {
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	rootKey := key()
	root := &x509.Certificate{Subject: pkix.Name{CommonName: authority}, SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	issue := func(c, p *x509.Certificate, k, signer *ecdsa.PrivateKey) []byte {
		der, e := x509.CreateCertificate(rand.Reader, c, p, &k.PublicKey, signer)
		if e != nil {
			t.Fatal(e)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	rootPEM := issue(root, root, rootKey, rootKey)
	parse := func(raw []byte) *x509.Certificate {
		block, _ := pem.Decode(raw)
		c, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	root = parse(rootPEM)
	parent, parentKey := root, rootKey
	extra := []byte{}
	if intermediate {
		parentKey = key()
		parent = &x509.Certificate{Subject: pkix.Name{CommonName: "Project-one isolated intermediate"}, SerialNumber: big.NewInt(2), NotBefore: root.NotBefore, NotAfter: root.NotAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		extra = issue(parent, root, parentKey, rootKey)
		parent = parse(extra)
	}
	leafKey := key()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), DNSNames: []string{"portal.example.test", "admin.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if mutate != nil {
		mutate(leaf)
	}
	certificate := append(issue(leaf, parent, leafKey, parentKey), extra...)
	der, e := x509.MarshalPKCS8PrivateKey(leafKey)
	if e != nil {
		t.Fatal(e)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), rootPEM, now
}
func TestServingIdentityRefusesUnnamedAuthority(t *testing.T) {
	c, key, ca, now := identityFixtureWithAuthority(t, nil, false, "")
	if _, e := Validate(c, key, ca, []string{"https://127.0.0.1:18443", "https://127.0.0.1:18444"}, now); e == nil {
		t.Fatal("empty authority identity admitted")
	}
}
func TestServingIdentityExplicitTrustBothOriginsAndNoPrivateOutput(t *testing.T) {
	for _, intermediate := range []bool{false, true} {
		c, k, ca, now := identityFixture(t, nil, intermediate)
		for _, origins := range [][]string{{"https://portal.example.test:18443", "https://admin.example.test:18444"}, {"https://127.0.0.1:18443", "https://127.0.0.1:18444"}, {"https://[::1]:18443", "https://[::1]:18444"}} {
			v, e := Validate(c, k, ca, origins, now)
			if e != nil || v.NotAfter == "" || len(v.Hosts) == 0 {
				t.Fatal(v, e)
			}
			raw, _ := json.Marshal(v)
			if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), string(k)) {
				t.Fatal("private key output")
			}
		}
	}
}
func TestServingIdentityRefusesUntrustedInvalidPairTimeHostsAndPEM(t *testing.T) {
	c, k, ca, now := identityFixture(t, nil, false)
	other, otherKey, otherCA, _ := identityFixture(t, nil, false)
	origins := []string{"https://portal.example.test", "https://admin.example.test"}
	for _, fault := range []string{"wrong-key", "wrong-CA", "second-host", "first-host", "expired", "future", "client-only", "CA-leaf", "no-SAN", "garbage-certificate", "trailing-certificate", "wrong-PEM", "bad-certificate", "empty-CA", "garbage-CA", "leaf-as-CA", "extra-key", "key-prefix", "key-suffix", "HTTP", "userinfo", "path", "fragment", "query", "missing-origin", "zero-time"} {
		t.Run(fault, func(t *testing.T) {
			cert, key, trust := c, k, ca
			hosts := append([]string{}, origins...)
			at := now
			switch fault {
			case "wrong-key":
				key = otherKey
			case "wrong-CA":
				trust = otherCA
			case "second-host":
				hosts[1] = "https://other.example.test"
			case "first-host":
				hosts[0] = "https://other.example.test"
			case "expired":
				at = now.Add(25 * time.Hour)
			case "future":
				at = now.Add(-time.Hour)
			case "client-only":
				cert, key, trust, _ = identityFixture(t, func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }, false)
			case "CA-leaf":
				cert, key, trust, _ = identityFixture(t, func(c *x509.Certificate) { c.IsCA = true; c.KeyUsage |= x509.KeyUsageCertSign }, false)
			case "no-SAN":
				cert, key, trust, _ = identityFixture(t, func(c *x509.Certificate) {
					c.DNSNames = nil
					c.IPAddresses = nil
					c.Subject.CommonName = "portal.example.test"
				}, false)
			case "garbage-certificate":
				cert = append([]byte("junk"), c...)
			case "trailing-certificate":
				cert = append(append([]byte{}, c...), []byte("junk")...)
			case "wrong-PEM":
				cert = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("bad")})
			case "bad-certificate":
				cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("bad")})
			case "empty-CA":
				trust = nil
			case "garbage-CA":
				trust = append([]byte("junk"), ca...)
			case "leaf-as-CA":
				trust = other
			case "extra-key":
				key = append(append([]byte{}, k...), otherKey...)
			case "key-prefix":
				key = append([]byte("junk"), k...)
			case "key-suffix":
				key = append(append([]byte{}, k...), []byte("junk")...)
			case "HTTP":
				hosts[0] = "http://portal.example.test"
			case "userinfo":
				hosts[0] = "https://user@portal.example.test"
			case "path":
				hosts[0] += "/path"
			case "fragment":
				hosts[0] += "#test"
			case "query":
				hosts[0] += "?test"
			case "missing-origin":
				hosts = hosts[:1]
			case "zero-time":
				at = time.Time{}
			}
			v, e := Validate(cert, key, trust, hosts, at)
			if e == nil || e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" || v.LeafSHA256 != "" {
				t.Fatal("invalid identity admitted or unsafe diagnostic", e)
			}
		})
	}
}
