// Package agentidentity issues and reads the client certificates that identify agents
// (ARC-103, ADR 0050).
package agentidentity

import (
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// backdate lets a certificate work on a node whose clock is somewhat behind control's.
const backdate = time.Hour

// serialBits is the entropy of a certificate's serial number.
const serialBits = 128

// Authority signs node certificates with the agent CA.
type Authority struct {
	certificate *x509.Certificate
	key         crypto.Signer
	lifetime    time.Duration
}

// NewAuthority builds an authority from a CA bundle and the private key of its first
// certificate, which is the issuer; the others are trusted only while a CA is replaced.
func NewAuthority(bundlePEM, keyPEM []byte, lifetime time.Duration) (*Authority, error) {
	bad := errors.New("AGENT_IDENTITY_MATERIAL_INVALID")
	pair, e := tls.X509KeyPair(bundlePEM, keyPEM)
	if e != nil || lifetime <= 0 {
		return nil, bad
	}
	issuer, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil || !issuer.IsCA || issuer.KeyUsage&x509.KeyUsageCertSign == 0 || !issuer.BasicConstraintsValid {
		return nil, bad
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, bad
	}
	return &Authority{certificate: issuer, key: key, lifetime: lifetime}, nil
}

// Issue signs a certificate naming node for the key of a checked certificate request. Only
// the request's public key is used. The certificate can only authenticate a client and
// never outlives its issuer.
func (a *Authority) Issue(request *x509.CertificateRequest, scope wire.Scope, node string) (string, time.Time, error) {
	bad := errors.New("AGENT_IDENTITY_UNAVAILABLE")
	if a == nil || request == nil {
		return "", time.Time{}, bad
	}
	identity, e := wire.NodeURI(scope, node)
	if e != nil {
		return "", time.Time{}, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if e != nil {
		return "", time.Time{}, bad
	}
	now := time.Now()
	notAfter := now.Add(a.lifetime)
	if notAfter.After(a.certificate.NotAfter) {
		notAfter = a.certificate.NotAfter
	}
	if !now.Before(notAfter) {
		return "", time.Time{}, bad
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: node},
		NotBefore:    now.Add(-backdate),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{identity},
	}
	der, e := x509.CreateCertificate(rand.Reader, template, a.certificate, request.PublicKey, a.key)
	if e != nil {
		return "", time.Time{}, bad
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: wire.CertificatePEM, Bytes: der})), notAfter, nil
}

// Peer is what a connection's verified client certificate says.
type Peer struct {
	// Presented reports a certificate; the other fields mean nothing without one.
	Presented bool
	Scope     wire.Scope
	Node      string
	Key       []byte
	NotAfter  time.Time
	// Malformed reports a certificate of the agent CA that names no node.
	Malformed bool
}

// PeerOf reads the verified client certificate of a connection. A nil state, as on a
// listener without TLS, presents none.
func PeerOf(state *tls.ConnectionState) Peer {
	if state == nil || len(state.PeerCertificates) == 0 {
		return Peer{}
	}
	leaf := state.PeerCertificates[0]
	scope, node, e := wire.CertificateNode(leaf)
	if e != nil {
		return Peer{Presented: true, Malformed: true}
	}
	key, e := wire.KeyDigest(leaf.PublicKey)
	if e != nil {
		return Peer{Presented: true, Malformed: true}
	}
	return Peer{Presented: true, Scope: scope, Node: node, Key: key, NotAfter: leaf.NotAfter}
}
