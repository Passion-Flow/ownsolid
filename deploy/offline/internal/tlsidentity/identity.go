// Package tlsidentity validates an explicit serving identity without ambient trust or network access.
package tlsidentity

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/url"
	"slices"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Identity struct {
	LeafSHA256        string   `json:"leaf_sha256"`
	CertificateSHA256 string   `json:"certificate_sha256"`
	PrivateKeySHA256  string   `json:"private_key_sha256"`
	TrustBundleSHA256 string   `json:"trust_bundle_sha256"`
	Hosts             []string `json:"hosts"`
	NotBefore         string   `json:"not_before"`
	NotAfter          string   `json:"not_after"`
}

func certificates(raw []byte) ([]*x509.Certificate, error) {
	result := []*x509.Certificate{}
	for len(bytes.TrimSpace(raw)) > 0 {
		raw = bytes.TrimSpace(raw)
		// pem.Decode skips leading junk; forbid that behavior at this boundary.
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("OFFLINE_TLS_CERTIFICATE_PEM_INVALID")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("OFFLINE_TLS_CERTIFICATE_PEM_INVALID")
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return nil, errors.New("OFFLINE_TLS_CERTIFICATE_PEM_INVALID")
		}
		result = append(result, cert)
		raw = rest
	}
	if len(result) == 0 {
		return nil, errors.New("OFFLINE_TLS_CERTIFICATE_PEM_INVALID")
	}
	return result, nil
}

// Validate checks pair, explicit chain, time and every advertised origin. It does not install trust.
func Validate(certificate, key, trust []byte, origins []string, now time.Time) (Identity, error) {
	hosts, e := OriginHosts(origins)
	if e != nil {
		return Identity{}, errors.New("OFFLINE_TLS_SERVING_IDENTITY_INVALID")
	}
	return validateHosts(certificate, key, trust, hosts, now)
}

// ValidateMachine checks one explicitly selected HTTPS identity through the
// same private-key, chain, explicit trust, SAN, server-auth and time boundary.
func ValidateMachine(certificate, key, trust []byte, origin string, now time.Time) (Identity, error) {
	hosts, e := MachineOriginHosts(origin)
	if e != nil {
		return Identity{}, errors.New("OFFLINE_TLS_SERVING_IDENTITY_INVALID")
	}
	return validateHosts(certificate, key, trust, hosts, now)
}

func validateHosts(certificate, key, trust []byte, hosts []string, now time.Time) (Identity, error) {
	var out Identity
	bad := func() (Identity, error) { return Identity{}, errors.New("OFFLINE_TLS_SERVING_IDENTITY_INVALID") }
	if now.IsZero() || len(hosts) == 0 {
		return bad()
	}
	keyInput := key
	key = bytes.TrimSpace(key)
	if !bytes.HasPrefix(key, []byte("-----BEGIN ")) {
		return bad()
	}
	keyBlock, keyRest := pem.Decode(key)
	if keyBlock == nil || len(keyBlock.Headers) != 0 || len(bytes.TrimSpace(keyRest)) != 0 || keyBlock.Type != "PRIVATE KEY" && keyBlock.Type != "EC PRIVATE KEY" && keyBlock.Type != "RSA PRIVATE KEY" {
		return bad()
	}
	pair, e := tls.X509KeyPair(certificate, key)
	if e != nil || len(pair.Certificate) == 0 {
		return bad()
	}
	chain, e := certificates(certificate)
	if e != nil {
		return bad()
	}
	roots, e := certificates(trust)
	if e != nil {
		return bad()
	}
	if chain[0].IsCA || len(chain[0].Issuer.Names) == 0 {
		return bad()
	}
	pool := x509.NewCertPool()
	for _, c := range roots {
		if !c.IsCA || !c.BasicConstraintsValid || len(c.Subject.Names) == 0 || len(c.Issuer.Names) == 0 {
			return bad()
		}
		pool.AddCert(c)
	}
	intermediate := x509.NewCertPool()
	for _, c := range chain[1:] {
		if len(c.Subject.Names) == 0 || len(c.Issuer.Names) == 0 {
			return bad()
		}
		intermediate.AddCert(c)
	}
	for _, host := range hosts {
		if _, e := chain[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: intermediate, CurrentTime: now, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
			return bad()
		}
	}
	out = Identity{LeafSHA256: wire.Digest(chain[0].Raw), CertificateSHA256: wire.Digest(certificate), PrivateKeySHA256: wire.Digest(keyInput), TrustBundleSHA256: wire.Digest(trust), Hosts: hosts, NotBefore: chain[0].NotBefore.UTC().Format(time.RFC3339), NotAfter: chain[0].NotAfter.UTC().Format(time.RFC3339)}
	return out, nil
}

// OriginHosts retains the exact-two browser address contract, including shared
// host deduplication. A single machine identity must use its separate entrance.
func OriginHosts(origins []string) ([]string, error) {
	if len(origins) != 2 {
		return nil, errors.New("OFFLINE_TLS_SERVING_IDENTITY_INVALID")
	}
	return originHosts(origins)
}

// MachineOriginHosts validates one explicit HTTPS origin without current PKI,
// host file reads or implicit browser fallback during metadata observation.
func MachineOriginHosts(origin string) ([]string, error) {
	return originHosts([]string{origin})
}

func originHosts(origins []string) ([]string, error) {
	bad := func() ([]string, error) { return nil, errors.New("OFFLINE_TLS_SERVING_IDENTITY_INVALID") }
	hosts := []string{}
	for _, origin := range origins {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || origin != "https://"+u.Host {
			return bad()
		}
		if !slices.Contains(hosts, u.Hostname()) {
			hosts = append(hosts, u.Hostname())
		}
	}
	slices.Sort(hosts)
	return hosts, nil
}
