package v1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

const identityTestNode = "11111111-2222-4333-8444-555555555555"

func requestPEM(t *testing.T, key any, template *x509.CertificateRequest) string {
	t.Helper()
	der, e := x509.CreateCertificateRequest(rand.Reader, template, key)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: CertificateRequestPEM, Bytes: der}))
}

func signedLeaf(t *testing.T, key *ecdsa.PrivateKey, uris []*url.URL, ca bool) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{SerialNumber: x509SerialOne(), Subject: pkix.Name{CommonName: "x"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), URIs: uris, IsCA: ca, BasicConstraintsValid: ca}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	return leaf
}

func TestCertificateRequestsAreCheckedForShapeAndKey(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, e := ParseCertificateRequest(requestPEM(t, key, &x509.CertificateRequest{})); e != nil {
		t.Fatal("a plain request", e)
	}
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	weak, _ := rsa.GenerateKey(rand.Reader, 2048)
	named, _ := url.Parse("urn:ownsolid:node:default:default:" + identityTestNode)
	valid := requestPEM(t, key, &x509.CertificateRequest{})
	cases := map[string]string{
		"empty":           "",
		"not pem":         "garbage",
		"too long":        strings.Repeat("a", MaxCertificateRequestBytes+1),
		"wrong curve":     requestPEM(t, p384, &x509.CertificateRequest{}),
		"wrong algorithm": requestPEM(t, weak, &x509.CertificateRequest{}),
		"asks for a name": requestPEM(t, key, &x509.CertificateRequest{URIs: []*url.URL{named}}),
		"asks for dns":    requestPEM(t, key, &x509.CertificateRequest{DNSNames: []string{"control.internal"}}),
		"two blocks":      valid + valid,
		"wrong type":      strings.ReplaceAll(valid, CertificateRequestPEM, "CERTIFICATE"),
	}
	for name, text := range cases {
		if _, e := ParseCertificateRequest(text); e == nil || e.Error() != "AGENT_CERTIFICATE_REQUEST_INVALID" {
			t.Errorf("%s: %v", name, e)
		}
	}
	block, _ := pem.Decode([]byte(valid))
	block.Bytes[len(block.Bytes)-1] ^= 1
	if _, e := ParseCertificateRequest(string(pem.EncodeToMemory(block))); e == nil {
		t.Error("a bad signature was accepted")
	}
}

func TestCertificateNamesExactlyOneNode(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	good, e := NodeURI(LocalScope(), identityTestNode)
	if e != nil {
		t.Fatal(e)
	}
	scope, node, e := CertificateNode(signedLeaf(t, key, []*url.URL{good}, false))
	if e != nil || scope != LocalScope() || node != identityTestNode {
		t.Fatal(scope, node, e)
	}
	other, _ := url.Parse("urn:ownsolid:node:default:default:not-a-uuid")
	foreign, _ := url.Parse("spiffe://example.org/node/" + identityTestNode)
	for name, uris := range map[string][]*url.URL{"none": nil, "two": {good, good}, "bad node": {other}, "other scheme": {foreign}} {
		if _, _, e := CertificateNode(signedLeaf(t, key, uris, false)); e == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, _, e := CertificateNode(signedLeaf(t, key, []*url.URL{good}, true)); e == nil {
		t.Error("a CA was accepted")
	}
	if _, e := NodeURI(LocalScope(), "not-a-uuid"); e == nil {
		t.Error("a malformed node got a name")
	}
}

func TestRenewalFallsInsideTheWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(1000 * time.Hour)
	if at := RenewAt(func(int64) int64 { return 0 }, start, end); !at.Equal(start.Add(700 * time.Hour)) {
		t.Fatal(at)
	}
	if at := RenewAt(func(n int64) int64 { return n - 1 }, start, end); !at.Equal(start.Add(900 * time.Hour)) {
		t.Fatal(at)
	}
}

func TestIssuedCertificateMustFitTheKeyAndNode(t *testing.T) {
	key, e := NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	other, _ := NewCertificateKey()
	request, e := NewCertificateRequest(key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseCertificateRequest(request)
	if e != nil {
		t.Fatal(e)
	}
	ca, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	authority := signedLeaf(t, ca, nil, true)
	identity, _ := NodeURI(LocalScope(), identityTestNode)
	der, e := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: x509SerialOne(), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), URIs: []*url.URL{identity}}, authority, parsed.PublicKey, ca)
	if e != nil {
		t.Fatal(e)
	}
	issued := string(pem.EncodeToMemory(&pem.Block{Type: CertificatePEM, Bytes: der}))
	if _, e := CheckIssuedCertificate(issued, key, identityTestNode); e != nil {
		t.Fatal(e)
	}
	if _, e := CheckIssuedCertificate(issued, other, identityTestNode); e == nil {
		t.Error("a certificate for another key was accepted")
	}
	if _, e := CheckIssuedCertificate(issued, key, "99999999-2222-4333-8444-555555555555"); e == nil {
		t.Error("a certificate for another node was accepted")
	}
	if _, e := CheckIssuedCertificate("nonsense", key, identityTestNode); e == nil {
		t.Error("nonsense was accepted")
	}
}

func x509SerialOne() *big.Int { return big.NewInt(1) }
