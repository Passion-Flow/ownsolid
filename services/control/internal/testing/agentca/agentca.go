// Package agentca makes a throwaway agent CA for tests.
package agentca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
)

// Material returns a new CA certificate and its private key, in PEM.
func Material(t *testing.T) (bundle, key []byte) {
	t.Helper()
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * 365 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// Authority returns an authority of a new CA, issuing 30-day certificates.
func Authority(t *testing.T) *agentidentity.Authority {
	t.Helper()
	bundle, key := Material(t)
	authority, e := agentidentity.NewAuthority(bundle, key, 720*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	return authority
}
