package v1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Agent certificates (ARC-103, ADR 0050). An agent keeps a private key on the node and proves
// it on every connection; control signs certificates for keys it has pinned to the node.

const (
	// MaxCertificateRequestBytes bounds a PEM certificate request in an agent message.
	MaxCertificateRequestBytes = 2048
	// MaxCertificateBytes bounds a PEM certificate chain in a reply.
	MaxCertificateBytes = 8192
	// CertificatesPath ends the path that requests a certificate.
	CertificatesPath = "certificates"
	// CertificateConfirmationsPath follows CertificatesPath to end a rotation.
	CertificateConfirmationsPath = "confirmations"
	// CertificateRequestPEM and CertificatePEM are the PEM block types the protocol carries.
	CertificateRequestPEM = "CERTIFICATE REQUEST"
	CertificatePEM        = "CERTIFICATE"
	// PrivateKeyPEM is the PEM block type of a node's private key, which stays on the node.
	PrivateKeyPEM = "PRIVATE KEY"

	nodeURIPrefix = "urn:ownsolid:node:"
	// RenewEarliestPercent and RenewLatestPercent bound when in a certificate's lifetime an
	// agent renews it. The moment is random inside them, so a fleet enrolled together does
	// not renew together.
	RenewEarliestPercent = 70
	RenewLatestPercent   = 90
	// ExpirySafety is how long before its end an agent stops presenting a certificate, so
	// a clock slightly behind control's does not fail the handshake.
	ExpirySafety = 5 * time.Minute
)

// CertificateRenewal asks for a certificate for the key that signed the request.
type CertificateRenewal struct {
	CertificateRequest string `json:"certificate_request"`
}

// IssuedCertificate is a signed certificate, leaf first, in PEM.
type IssuedCertificate struct {
	Certificate string `json:"certificate"`
}

// CertificateConfirmed reports that the old key of a rotation no longer works.
type CertificateConfirmed struct {
	Confirmed bool `json:"confirmed"`
}

// NodeURI is the identity a node's certificate names, in its one URI SAN.
func NodeURI(scope Scope, node string) (*url.URL, error) {
	if !scope.Valid() || !validNode(node) {
		return nil, errors.New("AGENT_CERTIFICATE_REQUEST_INVALID")
	}
	return url.Parse(nodeURIPrefix + scope.SiteID + ":" + scope.CellID + ":" + node)
}

var nodePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validNode(node string) bool { return nodePattern.MatchString(node) }

// CertificateNode reads the node a verified client certificate names. A certificate that does
// not have the exact shape control issues names no node.
func CertificateNode(c *x509.Certificate) (Scope, string, error) {
	bad := errors.New("AGENT_UNAUTHORIZED")
	if c == nil || c.IsCA || len(c.URIs) != 1 || len(c.DNSNames) != 0 || len(c.IPAddresses) != 0 || len(c.EmailAddresses) != 0 {
		return Scope{}, "", bad
	}
	rest, found := strings.CutPrefix(c.URIs[0].String(), nodeURIPrefix)
	if !found {
		return Scope{}, "", bad
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		return Scope{}, "", bad
	}
	scope := Scope{SiteID: parts[0], CellID: parts[1]}
	if !scope.Valid() || !validNode(parts[2]) {
		return Scope{}, "", bad
	}
	return scope, parts[2], nil
}

// ParseCertificateRequest checks a PEM certificate request: one block, a good signature, which
// proves the sender holds the key, an ECDSA P-256 key and no names of its own. Control decides
// every name in a certificate.
func ParseCertificateRequest(text string) (*x509.CertificateRequest, error) {
	bad := errors.New("AGENT_CERTIFICATE_REQUEST_INVALID")
	if text == "" || len(text) > MaxCertificateRequestBytes {
		return nil, bad
	}
	block, rest := pem.Decode([]byte(text))
	if block == nil || block.Type != CertificateRequestPEM || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, bad
	}
	request, e := x509.ParseCertificateRequest(block.Bytes)
	if e != nil || request.CheckSignature() != nil {
		return nil, bad
	}
	key, ok := request.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, bad
	}
	if len(request.DNSNames) != 0 || len(request.IPAddresses) != 0 || len(request.EmailAddresses) != 0 || len(request.URIs) != 0 {
		return nil, bad
	}
	return request, nil
}

// NewCertificateKey makes a node's private key, in PEM.
func NewCertificateKey() (string, error) {
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return "", errors.New("CERTIFICATE_KEY_FAILED")
	}
	der, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		return "", errors.New("CERTIFICATE_KEY_FAILED")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: PrivateKeyPEM, Bytes: der})), nil
}

// parseCertificateKey reads a node's private key.
func parseCertificateKey(key string) (*ecdsa.PrivateKey, error) {
	bad := errors.New("CERTIFICATE_STATE_INVALID")
	block, _ := pem.Decode([]byte(key))
	if block == nil || block.Type != PrivateKeyPEM {
		return nil, bad
	}
	parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return nil, bad
	}
	private, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, bad
	}
	return private, nil
}

// NewCertificateRequest signs a request with key. It names nothing: control decides every
// name in a certificate.
func NewCertificateRequest(key string) (string, error) {
	private, e := parseCertificateKey(key)
	if e != nil {
		return "", e
	}
	der, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, private)
	if e != nil {
		return "", errors.New("CERTIFICATE_KEY_FAILED")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: CertificateRequestPEM, Bytes: der})), nil
}

// CheckIssuedCertificate accepts a certificate only for the key that asked and for this node.
func CheckIssuedCertificate(certificate, key, node string) (*x509.Certificate, error) {
	bad := errors.New("CERTIFICATE_RESPONSE_INVALID")
	if certificate == "" || len(certificate) > MaxCertificateBytes {
		return nil, bad
	}
	block, _ := pem.Decode([]byte(certificate))
	if block == nil || block.Type != CertificatePEM {
		return nil, bad
	}
	leaf, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return nil, bad
	}
	private, e := parseCertificateKey(key)
	if e != nil {
		return nil, bad
	}
	public, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !public.Equal(&private.PublicKey) {
		return nil, bad
	}
	if _, named, e := CertificateNode(leaf); e != nil || named != node {
		return nil, bad
	}
	return leaf, nil
}

// KeyDigest identifies a public key: the SHA-256 of its DER SubjectPublicKeyInfo.
func KeyDigest(publicKey any) ([]byte, error) {
	der, e := x509.MarshalPKIXPublicKey(publicKey)
	if e != nil {
		return nil, errors.New("AGENT_CERTIFICATE_REQUEST_INVALID")
	}
	digest := sha256.Sum256(der)
	return digest[:], nil
}

// RenewAt picks when an agent renews a certificate valid from notBefore to notAfter.
func RenewAt(random Random, notBefore, notAfter time.Time) time.Time {
	lifetime := notAfter.Sub(notBefore)
	earliest := lifetime / 100 * RenewEarliestPercent
	window := lifetime / 100 * (RenewLatestPercent - RenewEarliestPercent)
	return notBefore.Add(earliest + time.Duration(random(int64(window)+1)))
}
