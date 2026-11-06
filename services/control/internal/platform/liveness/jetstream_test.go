package liveness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

func pemBlock(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}

// serverCertificates makes a CA and one certificate that every server of the cluster uses.
func serverCertificates(t *testing.T, directory string, names ...string) []byte {
	t.Helper()
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "liveness test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "liveness.internal"}, DNSNames: append([]string{"liveness.internal"}, names...), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, caTemplate, &key.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	ca := pemBlock("CERTIFICATE", caDER)
	for name, content := range map[string][]byte{"ca.pem": ca, "server.pem": pemBlock("CERTIFICATE", der), "server.key": pemBlock("EC PRIVATE KEY", keyDER)} {
		if e = os.WriteFile(filepath.Join(directory, name), content, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	return ca
}

func TestNewJetStreamRefusesBadSettings(t *testing.T) {
	user, e := nkeys.CreateUser()
	if e != nil {
		t.Fatal(e)
	}
	seed, e := user.Seed()
	if e != nil {
		t.Fatal(e)
	}
	// The address is unused: construction checks settings and connects in the background.
	valid := func() JetStreamSettings {
		return JetStreamSettings{Servers: []string{"tls://127.0.0.1:1"}, ServerName: "x", CA: serverCertificates(t, t.TempDir()), Seed: seed, Bucket: "b", Replicas: 1, TTL: time.Minute}
	}
	replica, e := NewJetStream(valid())
	if e != nil {
		t.Fatal("a cluster that is down must not stop the gateway:", e)
	}
	replica.Close()
	for want, change := range map[string]func(*JetStreamSettings){
		"LIVENESS_REPLICA_CONFIG_INVALID": func(s *JetStreamSettings) { s.Servers = nil },
		"LIVENESS_REPLICA_CA_INVALID":     func(s *JetStreamSettings) { s.CA = []byte("not pem") },
		"LIVENESS_REPLICA_NKEY_INVALID":   func(s *JetStreamSettings) { s.Seed = []byte("not a seed") },
	} {
		settings := valid()
		change(&settings)
		if _, e := NewJetStream(settings); e == nil || e.Error() != want {
			t.Errorf("want %s, got %v", want, e)
		}
	}
}
