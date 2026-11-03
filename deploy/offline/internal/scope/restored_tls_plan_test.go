package scope

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Synthetic CA/leaf for isolated tests only; never installs a trust anchor.
func restoredTLSCertificate(t *testing.T) ([]byte, []byte, []byte) {
	return restoredTLSCertificateAt(t, time.Now())
}
func restoredTLSCertificateAt(t *testing.T, now time.Time) ([]byte, []byte, []byte) {
	t.Helper()
	rootKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	root := &x509.Certificate{Subject: pkix.Name{CommonName: "Project-one isolated test root"}, SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(96 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	rootDER, e := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	root, e = x509.ParseCertificate(rootDER)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: "Project-one isolated serving identity"}, AuthorityKeyId: root.SubjectKeyId, SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(48 * time.Hour), DNSNames: []string{"portal.example.test", "admin.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
}
func tlsProjectionFixture() (preparedInputs, compose.Model) {
	prefix := "/run/projectone/config/private/"
	runtime := encode(map[string]any{"server": map[string]string{"portal_origin": "https://portal.example.test:18443", "admin_origin": "https://admin.example.test:18444", "tls_certificate_file": prefix + "certificate.pem", "tls_private_key_file": prefix + "private-key.pem"}})
	p := preparedInputs{files: map[string][]engine.ConfigurationFile{"control-config": {{Name: "runtime.yaml", Contents: runtime}, {Name: "runtime-dsn", Contents: []byte("SYNTHETIC_PRIVATE_DSN")}, {Name: "auth-hmac", Contents: []byte("SYNTHETIC_AUTH_KEY")}, {Name: "certificate.pem", Contents: []byte("old certificate")}, {Name: "private-key.pem", Contents: []byte("old private key")}}}}
	m := compose.Model{Services: map[string]compose.Service{"control": {Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: "/run/projectone/config", ReadOnly: true}}}}}
	return p, m
}
func TestRestoredTLSProjectionPreservesEveryOtherByteAndInputOwnership(t *testing.T) {
	p, m := tlsProjectionFixture()
	original := encode(p.files)
	c, k, ca := restoredTLSCertificate(t)
	a, identity, cn, kn, e := projectRestoredTLS(p, m, c, k, ca, time.Now())
	if e != nil || cn != "certificate.pem" || kn != "private-key.pem" || identity.LeafSHA256 == "" {
		t.Fatal("TLS projection", e)
	}
	b, _, _, _, e := projectRestoredTLS(p, m, c, k, ca, time.Now())
	if e != nil || !reflect.DeepEqual(a, b) || !bytes.Equal(original, encode(p.files)) {
		t.Fatal("nondeterminism or input mutation", e)
	}
	for _, f := range a {
		want, _ := restoreNamedFile(p.files["control-config"], f.Name)
		if f.Name == cn {
			want = c
		}
		if f.Name == kn {
			want = k
		}
		if !bytes.Equal(want, f.Contents) {
			t.Fatal("unrelated payload changed")
		}
	}
	a[0].Contents[0] ^= 1
	if !bytes.Equal(original, encode(p.files)) {
		t.Fatal("payload aliases immutable snapshot")
	}
}
func TestRestoredTLSProjectionRefusesUnsafeOrUnchangedInputs(t *testing.T) {
	c, k, ca := restoredTLSCertificate(t)
	for _, fault := range []string{"duplicate", "missing-key", "external-reference", "traversal", "same-name", "bad-runtime", "unchanged-certificate", "wrong-key", "untrusted-CA", "no-readonly-mount", "duplicate-mount", "key-is-runtime", "secret-reference"} {
		t.Run(fault, func(t *testing.T) {
			p, m := tlsProjectionFixture()
			key := k
			trust := ca
			switch fault {
			case "duplicate":
				p.files["control-config"] = append(p.files["control-config"], p.files["control-config"][0])
			case "missing-key":
				p.files["control-config"] = p.files["control-config"][:4]
			case "external-reference":
				p.files["control-config"][0].Contents = bytes.ReplaceAll(p.files["control-config"][0].Contents, []byte("/run/projectone/config/private/private-key.pem"), []byte("/outside/private-key.pem"))
			case "traversal":
				p.files["control-config"][0].Contents = bytes.ReplaceAll(p.files["control-config"][0].Contents, []byte("/private/private-key.pem"), []byte("/private/../private-key.pem"))
			case "same-name":
				p.files["control-config"][0].Contents = bytes.ReplaceAll(p.files["control-config"][0].Contents, []byte("private-key.pem"), []byte("certificate.pem"))
			case "bad-runtime":
				p.files["control-config"][0].Contents = []byte("{}")
			case "unchanged-certificate":
				p.files["control-config"][3].Contents = c
			case "wrong-key":
				key = []byte("SYNTHETIC_SECRET_INVALID_PRIVATE_KEY")
			case "untrusted-CA":
				trust = []byte("invalid")
			case "no-readonly-mount":
				s := m.Services["control"]
				s.Mounts[0].ReadOnly = false
				m.Services["control"] = s
			case "duplicate-mount":
				s := m.Services["control"]
				s.Mounts = append(s.Mounts, s.Mounts[0])
				m.Services["control"] = s
			case "key-is-runtime":
				p.files["control-config"][0].Contents = bytes.ReplaceAll(p.files["control-config"][0].Contents, []byte("private-key.pem"), []byte("runtime.yaml"))
			case "secret-reference":
				raw := string(p.files["control-config"][0].Contents)
				raw = strings.TrimSuffix(raw, "\n")
				raw = strings.TrimSuffix(raw, "}") + `,"security":{"auth_hmac_key_file":"/run/projectone/config/private/private-key.pem"}}`
				p.files["control-config"][0].Contents = []byte(raw)
			}
			if _, _, _, _, e := projectRestoredTLS(p, m, c, key, trust, time.Now()); e == nil {
				t.Fatal("unsafe projection admitted")
			}
		})
	}
}
func TestRestoredTLSPlanRequiresCompletedStoresBeforeReadingCertificate(t *testing.T) {
	l, _ := completedRestoreSQLSnapshot(t)
	defer l.close()
	request := configuration.TLSGenerationRequest{FormatVersion: 1, CertificateFile: "/external/new/cert.pem", PrivateKeyFile: "/external/new/key.pem", TrustBundleFile: "/external/new/ca.pem", Reason: "Isolated replacement"}
	_, _, e := l.makeRestoredTLSPlan(context.Background(), request, strings.Repeat("e", 32), verifiedRuntimeContext{}, time.Now())
	if e == nil || e.Error() != "OFFLINE_SCOPE_RESTORED_TLS_COMPLETED_RESTORE_STAGES_REQUIRED" {
		t.Fatal("incomplete stages admitted", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e = l.makeRestoredTLSPlan(ctx, request, strings.Repeat("e", 32), verifiedRuntimeContext{}, time.Now()); e == nil || e.Error() != "OFFLINE_SCOPE_OPERATION_INTERRUPTED" {
		t.Fatal("canceled plan admitted", e)
	}
}
