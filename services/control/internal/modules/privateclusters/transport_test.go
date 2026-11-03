package privateclusters

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the exact production credential/dial boundary. Only the remote peer
// and credential bytes are synthetic; this is no signed image/cluster claim.
func protectedPeer(t *testing.T, handler http.HandlerFunc) (Config, Profile, []byte, *atomic.Int32) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "M28 synthetic private CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if e != nil {
		t.Fatal(e)
	}
	requests := new(atomic.Int32)
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.TLS == nil || r.Header.Get("Authorization") != "Bearer synthetic-private-cluster-token" {
			t.Error("protected TLS/Bearer boundary missing")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	peer.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	peer.StartTLS()
	t.Cleanup(peer.Close)
	c := syntheticConfig()
	p := c.Profiles[0]
	p.ServerOrigin, p.TLSServerName = peer.URL, "127.0.0.1"
	p.CAFile, p.TokenFile = filepath.Join(t.TempDir(), "ca.pem"), filepath.Join(t.TempDir(), "token")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if e = os.WriteFile(p.CAFile, caPEM, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p.TokenFile, []byte("synthetic-private-cluster-token\n"), 0600); e != nil {
		t.Fatal(e)
	}
	return c, p, caPEM, requests
}

func TestPrivateClusterProtectedTransport(t *testing.T) {
	c, p, ca, requests := protectedPeer(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) })
	// A configured process proxy must not receive Kubernetes credentials.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	remote, e := credentialClient(t.Context(), c, p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(remote.close)
	var result map[string]bool
	if e = remote.get(t.Context(), "/proof", &result); e != nil || !result["ok"] || requests.Load() != 1 || remote.transport.Proxy != nil || len(remote.credentialSHA) != 64 {
		t.Fatal("actual protected transport failed", e)
	}
	for name, mutate := range map[string]func(*Profile){
		"private CIDR escape": func(p *Profile) { p.AllowedCIDRs = []string{"10.0.0.0/8"} },
		"hostname mismatch":   func(p *Profile) { p.TLSServerName = "wrong.example.invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			profile := p
			mutate(&profile)
			bad, e := credentialClient(t.Context(), c, profile)
			if e != nil {
				t.Fatal(e)
			}
			defer bad.close()
			before := requests.Load()
			if e = bad.get(t.Context(), "/proof", new(map[string]bool)); e == nil || requests.Load() != before {
				t.Fatal("unsafe peer reached", e)
			}
		})
	}
	for name, bytes := range map[string][]byte{
		"garbage preamble": append([]byte("not a certificate\n"), ca...),
		"garbage tail":     append(append([]byte{}, ca...), []byte("garbage")...),
		"empty":            {},
	} {
		t.Run(name, func(t *testing.T) {
			profile := p
			profile.CAFile = filepath.Join(t.TempDir(), "ca.pem")
			if e := os.WriteFile(profile.CAFile, bytes, 0600); e != nil {
				t.Fatal(e)
			}
			expected := "PRIVATE_CLUSTER_CA_INVALID"
			if name == "empty" {
				expected = "PRIVATE_CLUSTER_CA_REQUIRED" // Protected files reject empty input before PEM parsing.
			}
			if _, e := credentialClient(t.Context(), c, profile); e == nil || e.Error() != expected {
				t.Fatal("malformed CA accepted", e)
			}
		})
	}
	for _, token := range []string{"short", "synthetic-private-cluster-token\r\n", "synthetic-private-cluster-token\n\n", "synthetic-private-cluster-token injected"} {
		profile := p
		profile.TokenFile = filepath.Join(t.TempDir(), "token")
		if e := os.WriteFile(profile.TokenFile, []byte(token), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := credentialClient(t.Context(), c, profile); e == nil || e.Error() != "PRIVATE_CLUSTER_TOKEN_INVALID" {
			t.Fatal("invalid Bearer material accepted", e)
		}
	}
	profile := p
	profile.TokenFile = filepath.Join(t.TempDir(), "symlink")
	if e := os.Symlink(p.TokenFile, profile.TokenFile); e != nil {
		t.Fatal(e)
	}
	if _, e := credentialClient(t.Context(), c, profile); e == nil {
		t.Fatal("symlink credential accepted")
	}
	for _, suffix := range []string{"?", "#"} {
		profile := p
		profile.ServerOrigin += suffix
		if profile.Validate() == nil {
			t.Fatal("non-origin URL delimiter accepted")
		}
	}
}

func TestPrivateClusterAccessRequiresExplicitCurrentReview(t *testing.T) {
	for _, mode := range []string{"canonical", "missing allowed", "null allowed", "aliased allowed", "evaluation error", "excess authority"} {
		t.Run(mode, func(t *testing.T) {
			var profile Profile
			c, _ := wireClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/selfsubjectreviews") {
					json.NewEncoder(w).Encode(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview", "status": map[string]any{"userInfo": map[string]any{"username": "system:serviceaccount:" + profile.Namespace + ":" + ControlServiceAccount, "uid": profile.ControlServiceAccountUID}}})
					return
				}
				var request struct {
					Spec struct {
						ResourceAttributes map[string]string `json:"resourceAttributes"`
					} `json:"spec"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid review request")
					w.WriteHeader(400)
					return
				}
				a := request.Spec.ResourceAttributes
				deny := a["resource"] == "secrets" || a["subresource"] == "exec" || a["resource"] == "pods" && (a["verb"] == "create" || a["verb"] == "delete") || a["verb"] == "deletecollection" || a["resource"] == "nodes" && a["verb"] == "update" || a["resource"] == "namespaces" && a["verb"] == "delete" || a["resource"] == "rolebindings" || a["resource"] == "deployments" || a["namespace"] == "default"
				status := map[string]any{"allowed": !deny, "denied": deny}
				if a["resource"] == "secrets" {
					switch mode {
					case "missing allowed":
						delete(status, "allowed")
					case "null allowed":
						status["allowed"] = nil
					case "aliased allowed":
						delete(status, "allowed")
						status["Allowed"] = false
					case "evaluation error":
						status["evaluationError"] = "synthetic unresolved authorizer"
					case "excess authority":
						status["allowed"], status["denied"] = true, false
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "status": status})
			})
			profile = syntheticProfile()
			c.profile.Namespace = profile.Namespace
			c.profile.ControlServiceAccountUID = profile.ControlServiceAccountUID
			e := c.access(t.Context())
			if (e == nil) != (mode == "canonical") {
				t.Fatal("incomplete or excessive authority accepted", e)
			}
		})
	}
}
