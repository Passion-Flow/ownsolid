package imageregistry

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These are explicit malformed-protocol/TLS refusal controls. A test HTTP server
// is never positive Distribution acceptance or an upload-success substitute.
func transportConfig(t *testing.T, server *httptest.Server) Config {
	t.Helper()
	dir := t.TempDir()
	c := Defaults()
	c.Endpoint = server.URL
	c.AllowedCIDRs = []string{"127.0.0.0/8"}
	c.Verification.MountRoot = dir
	c.Verification.TrustFile = filepath.Join(dir, "trust.json")
	c.StoreRoot = filepath.Join(dir, "store")
	c.PreparedReleaseRoot = filepath.Join(dir, "release")
	c.CAFile = filepath.Join(dir, "ca.pem")
	c.CredentialFile = filepath.Join(dir, "credential.json")
	c.ReleaseArchiveSHA256 = strings.Repeat("a", 64)
	c.ReleaseManifestSHA256 = strings.Repeat("b", 64)
	cert, e := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.CredentialFile, []byte(`{"username":"synthetic-negative","password":"explicit-test-only"}`), 0600); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestRegistryTransportRefusals(t *testing.T) {
	for _, name := range []string{"wrong_digest", "redirect", "cross_origin_upload", "private_prefix_escape", "fixture_bypass", "unknown_config", "credential_duplicate", "false_is_false", "wrong_san"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch name {
				case "redirect":
					w.Header().Set("Location", "https://outside.example.invalid/")
					w.WriteHeader(307)
				case "cross_origin_upload":
					if r.Method == "HEAD" {
						w.WriteHeader(404)
					} else {
						w.Header().Set("Location", "https://outside.example.invalid/v2/x/blobs/uploads/a")
						w.WriteHeader(202)
					}
				default:
					w.Header().Set("Content-Length", "3")
					w.Write([]byte("bad"))
				}
			}))
			defer server.Close()
			c := transportConfig(t, server)
			if name == "wrong_san" {
				c.Endpoint = strings.Replace(c.Endpoint, "127.0.0.1", "localhost", 1)
				c.AllowedCIDRs = append(c.AllowedCIDRs, "::1/128")
			}
			if name == "private_prefix_escape" {
				c.AllowedCIDRs = []string{"10.0.0.0/7"}
				if c.Validate() == nil {
					t.Fatal("public addresses accepted inside partly private prefix")
				}
				return
			}
			if name == "fixture_bypass" {
				c.Verification.AllowTestFixture = true
				if c.Validate() == nil {
					t.Fatal("production fixture permission accepted")
				}
				return
			}
			if name == "unknown_config" {
				p := filepath.Join(t.TempDir(), "bad.json")
				os.WriteFile(p, []byte(`{"unknown":false}`), 0600)
				if _, e := Load(p); e == nil {
					t.Fatal("unknown configuration accepted")
				}
				return
			}
			if name == "false_is_false" {
				if c.Verification.AllowTestFixture || c.Validate() != nil {
					t.Fatal("false changed meaning")
				}
				return
			}
			if name == "credential_duplicate" {
				os.WriteFile(c.CredentialFile, []byte(`{"username":"one","username":"two","password":"fixture"}`), 0600)
				if _, e := newDistribution(c); e == nil {
					t.Fatal("duplicate private credential accepted")
				}
				return
			}
			client, e := newDistribution(c)
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			repo := repository("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003")
			d := Descriptor{Digest: digestBytes([]byte("yes")), Size: 3, MediaType: "application/octet-stream", Kind: "layer"}
			if name == "cross_origin_upload" {
				e = client.PutBlob(ctx, repo, d, "unused")
			} else {
				e = client.Verify(ctx, repo, d)
			}
			if e == nil {
				t.Fatal("malformed protocol granted content")
			}
		})
	}
}

func TestRegistryConfigurationContract(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("configuration control made a Registry request") }))
	defer server.Close()
	c := transportConfig(t, server)
	c.OperationSeconds = 7200
	c.RequestSeconds = 600
	c.GrantSeconds = 1800
	c.MaximumPins = 8
	c.MaximumReferences = 16
	c.MaximumConcurrentImports = 2
	c.PageSize = 3
	file := filepath.Join(t.TempDir(), "component.json")
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := LoadEnvironment(map[string]string{"PROJECT_ONE_REGISTRY_CONFIG_FILE": file})
	if e != nil || !reflect.DeepEqual(loaded, c) {
		t.Fatal("nondefault typed configuration not retained", e)
	}
	for _, values := range []map[string]string{nil, {}, {"PROJECT_ONE_REGISTRY_CONFIG_FILE": ""}, {"PROJECT_ONE_REGISTRY_CONFIG_FILE": file, "UNKNOWN": "false"}} {
		if _, e := LoadEnvironment(values); e == nil {
			t.Fatal("ambiguous component environment accepted")
		}
	}
	for _, raw := range []string{`{"request_seconds":null}`, `{"request_seconds":1,"request_seconds":2}`, `{"allow_unverified_tls":false}`, `{"operation_seconds":0}`, `{"minimum_headroom_percent":29}`, `{"maximum_concurrent_imports":0}`} {
		if e = os.WriteFile(file, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = Load(file); e == nil {
			t.Fatal("invalid configuration accepted", raw)
		}
	}
}
