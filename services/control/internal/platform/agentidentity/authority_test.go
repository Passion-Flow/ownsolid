package agentidentity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const testNode = "11111111-2222-4333-8444-555555555555"

func testCA(t *testing.T, notAfter time.Time, ca bool) ([]byte, []byte) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	usage := x509.KeyUsageCertSign
	if !ca {
		usage = x509.KeyUsageDigitalSignature
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, IsCA: ca, BasicConstraintsValid: true, KeyUsage: usage}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func request(t *testing.T) *x509.CertificateRequest {
	t.Helper()
	key, e := wire.NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	text, e := wire.NewCertificateRequest(key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := wire.ParseCertificateRequest(text)
	if e != nil {
		t.Fatal(e)
	}
	return parsed
}

func parse(t *testing.T, text string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		t.Fatal("no certificate")
	}
	leaf, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	return leaf
}

func TestIssueMakesAClientOnlyCertificateNamingTheNode(t *testing.T) {
	bundle, key := testCA(t, time.Now().Add(24*365*time.Hour), true)
	authority, e := NewAuthority(bundle, key, 720*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	text, notAfter, e := authority.Issue(request(t), wire.LocalScope(), testNode)
	if e != nil {
		t.Fatal(e)
	}
	leaf := parse(t, text)
	if _, node, e := wire.CertificateNode(leaf); e != nil || node != testNode {
		t.Fatal(node, e)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || leaf.IsCA || leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Fatalf("usage %v %v %v", leaf.ExtKeyUsage, leaf.IsCA, leaf.KeyUsage)
	}
	if !leaf.NotAfter.Equal(notAfter.Truncate(time.Second)) || leaf.NotBefore.After(time.Now()) {
		t.Fatal(leaf.NotBefore, leaf.NotAfter, notAfter)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(bundle)
	if _, e := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		t.Fatal("the agent CA must verify its certificates", e)
	}
	if _, e := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e == nil {
		t.Fatal("a node certificate must not be able to serve")
	}
}

func TestCertificateNeverOutlivesTheIssuer(t *testing.T) {
	end := time.Now().Add(48 * time.Hour)
	bundle, key := testCA(t, end, true)
	authority, e := NewAuthority(bundle, key, 720*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	_, notAfter, e := authority.Issue(request(t), wire.LocalScope(), testNode)
	if e != nil || notAfter.After(end) {
		t.Fatal(notAfter, e)
	}
}

func TestAnExpiredIssuerIssuesNothing(t *testing.T) {
	bundle, key := testCA(t, time.Now().Add(-time.Minute), true)
	authority, e := NewAuthority(bundle, key, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := authority.Issue(request(t), wire.LocalScope(), testNode); e == nil || e.Error() != "AGENT_IDENTITY_UNAVAILABLE" {
		t.Fatal(e)
	}
}

func TestAuthorityRefusesMaterialThatCannotSign(t *testing.T) {
	bundle, key := testCA(t, time.Now().Add(time.Hour), false)
	if _, e := NewAuthority(bundle, key, time.Hour); e == nil {
		t.Fatal("a leaf was accepted as a CA")
	}
	bundle, key = testCA(t, time.Now().Add(time.Hour), true)
	otherBundle, _ := testCA(t, time.Now().Add(time.Hour), true)
	if _, e := NewAuthority(otherBundle, key, time.Hour); e == nil {
		t.Fatal("a key of another CA was accepted")
	}
	if _, e := NewAuthority(bundle, key, 0); e == nil {
		t.Fatal("a zero lifetime was accepted")
	}
	retiring, _ := testCA(t, time.Now().Add(time.Hour), true)
	if _, e := NewAuthority(append(append([]byte{}, bundle...), retiring...), key, time.Hour); e != nil {
		t.Fatal("a CA being retired may follow the issuer", e)
	}
}

func TestPeerReadsTheVerifiedCertificate(t *testing.T) {
	if p := PeerOf(nil); p.Presented {
		t.Fatal("no TLS presents nothing")
	}
	if p := PeerOf(&tls.ConnectionState{}); p.Presented {
		t.Fatal("no certificate presents nothing")
	}
	bundle, key := testCA(t, time.Now().Add(24*time.Hour), true)
	authority, e := NewAuthority(bundle, key, 10*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	csr := request(t)
	text, _, e := authority.Issue(csr, wire.LocalScope(), testNode)
	if e != nil {
		t.Fatal(e)
	}
	leaf := parse(t, text)
	peer := PeerOf(&tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}})
	want, _ := wire.KeyDigest(csr.PublicKey)
	if !peer.Presented || peer.Malformed || peer.Node != testNode || peer.Scope != wire.LocalScope() || string(peer.Key) != string(want) || !peer.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("%+v", peer)
	}
	stranger := parse(t, string(bundle))
	if p := PeerOf(&tls.ConnectionState{PeerCertificates: []*x509.Certificate{stranger}}); !p.Presented || !p.Malformed {
		t.Fatalf("%+v", p)
	}
}
