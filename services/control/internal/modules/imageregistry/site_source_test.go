package imageregistry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func siteSourceFixtureFile(t *testing.T, source siteSourceConfig) string {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "site.json")
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestSiteReleaseSourceRefusesUntrustedOriginsAndCredentials(t *testing.T) {
	var outside atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		outside.Add(1)
	}))
	defer destination.Close()
	c := transportConfig(t, destination)
	remote, err := newDistribution(c)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, _, ok := r.BasicAuth()
		if !ok || username != "site-fixture" || r.Method != http.MethodGet ||
			r.URL.Path != "/v2/ownsolid/release/control-image/manifests/"+digestBytes([]byte("fixture")) {
			t.Error("site source did not use the scoped read-only request")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	s := transportConfig(t, redirect)
	if err := os.WriteFile(s.CredentialFile, []byte(`{"username":"site-fixture","password":"synthetic-fixture-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	valid := siteSourceConfig{Endpoint: s.Endpoint, AllowedCIDRs: s.AllowedCIDRs, CAFile: s.CAFile, CredentialFile: s.CredentialFile}
	for _, scenario := range []struct {
		name   string
		change func(*siteSourceConfig)
		code   string
	}{
		{"same_origin", func(site *siteSourceConfig) { site.Endpoint = c.Endpoint }, "REGISTRY_SITE_SOURCE_NOT_DISTINCT"},
		{"same_credential", func(site *siteSourceConfig) { site.CredentialFile = c.CredentialFile }, "REGISTRY_SITE_SOURCE_NOT_DISTINCT"},
		{"missing_ca", func(site *siteSourceConfig) { site.CAFile = filepath.Join(t.TempDir(), "missing-ca.pem") }, "REGISTRY_TLS_INVALID"},
		{"missing_credential", func(site *siteSourceConfig) { site.CredentialFile = filepath.Join(t.TempDir(), "missing.json") }, "REGISTRY_CREDENTIAL_INVALID"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := valid
			scenario.change(&config)
			if source, err := openSiteReleaseSource(siteSourceFixtureFile(t, config), c, remote); source != nil || err == nil || err.Error() != scenario.code {
				t.Fatalf("unexpected source admission: %v", err)
			}
		})
	}
	source, err := openSiteReleaseSource(siteSourceFixtureFile(t, valid), c, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer source.client.Close()
	blob := bundle.RuntimeBlob{Digest: digestBytes([]byte("fixture")), Size: 7, MediaType: ociManifest, Manifest: true}
	for _, asset := range []string{"../another-image", "control-image@sha256:wrong", "control_image", "registry-image"} {
		if err := source.get(t.Context(), asset, blob, io.Discard); err == nil || err.Error() != "REGISTRY_PATH_REJECTED" {
			t.Fatalf("unexpected asset admission: %q %v", asset, err)
		}
	}
	if err := source.get(t.Context(), "control-image", blob, io.Discard); err == nil || err.Error() != "REGISTRY_REDIRECT_REJECTED" {
		t.Fatalf("source redirect outcome: %v", err)
	}
	if outside.Load() != 0 {
		t.Fatal("source attempted a request on the writable origin")
	}
}
