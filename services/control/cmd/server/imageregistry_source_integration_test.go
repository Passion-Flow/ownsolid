package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	deploymenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments/http"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	licensehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	workspacehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This exercises the real startup constructor and PostgreSQL installation fence.
// Source imports and cross-workspace pin reads belong to the Registry fixture.
func TestRegistrySiteSourceStartupAdmission(t *testing.T) {
	owner := fixture.New(t)
	store, err := owner.Repository()
	if err != nil {
		t.Fatal(err)
	}
	w := &workspaces.Service{DB: owner.DB, Identity: owner}
	n := &nodes.Service{DB: owner.DB, Identity: owner, Workspaces: w}
	d := &deployments.Service{Nodes: n, Models: &models.Service{DB: owner.DB, Identity: owner, Workspaces: w}}
	license, err := licensing.FromConfig(owner)
	if err != nil {
		t.Fatal(err)
	}
	h := &identityhttp.Handler{
		Service: owner, Workspaces: &workspacehttp.Handler{Service: w},
		Nodes: &nodehttp.Handler{Service: n}, Deployments: &deploymenthttp.Handler{Service: d},
		Licensing: &licensehttp.Handler{Service: license},
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var requests atomic.Int32
	endpoint := func(name string) (*httptest.Server, string, string) {
		t.Helper()
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
		t.Cleanup(server.Close)
		ca := write(name+"-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
		raw, err := json.Marshal(map[string]string{"username": name, "password": security.Token()})
		if err != nil {
			t.Fatal(err)
		}
		return server, ca, write(name+"-credential.json", raw)
	}
	destination, ca, credential := endpoint("destination")
	source, sourceCA, sourceCredential := endpoint("source")
	registry := registryconfig.Defaults()
	registry.Endpoint, registry.AllowedCIDRs = destination.URL, []string{"127.0.0.0/8"}
	registry.CAFile, registry.CredentialFile = ca, credential
	registry.StoreRoot, registry.PreparedReleaseRoot = filepath.Join(root, "cas"), filepath.Join(root, "prepared")
	registry.Verification.MountRoot, registry.Verification.TrustFile = root, filepath.Join(root, "trust.json")
	registry.ReleaseArchiveSHA256, registry.ReleaseManifestSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := os.Mkdir(registry.StoreRoot, 0700); err != nil {
		t.Fatal(err)
	}
	sourceSettings := func() map[string]any {
		return map[string]any{"endpoint": source.URL, "allowed_cidrs": []string{"127.0.0.0/8"}, "ca_file": sourceCA, "credential_file": sourceCredential}
	}
	selectSource := func(name string, settings map[string]any) config.Config {
		t.Helper()
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		return config.Config{ImageRegistry: &registry, ImageRegistrySiteSourceFile: write(name+".json", raw)}
	}
	c := selectSource("site", sourceSettings())
	startup, cancelStartup := context.WithTimeout(t.Context(), time.Duration(c.Policy().HTTP.StartupSeconds)*time.Second)
	defer cancelStartup()
	if s, err := openImageRegistry(startup, c, store, h); s != nil || err == nil || err.Error() != "INSTALLATION_REQUIRED" {
		t.Fatal("site constructor bypassed installation admission", err)
	}
	fixture.Bootstrap(t, owner)
	type refusal struct {
		name, file, code string
	}
	public := selectSource("public", sourceSettings()).ImageRegistrySiteSourceFile
	if err := os.Chmod(public, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-source.json")
	if err := os.Symlink(c.ImageRegistrySiteSourceFile, link); err != nil {
		t.Fatal(err)
	}
	cases := []refusal{
		{"missing_source", filepath.Join(root, "absent-source.json"), "REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID"},
		{"public_source", public, "REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID"},
		{"linked_source", link, "REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID"},
		{"malformed_source", write("malformed-source.json", []byte("{")), "REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID"},
	}
	for _, policy := range []struct {
		name   string
		change func(map[string]any)
		code   string
	}{
		{"missing_ca", func(s map[string]any) { s["ca_file"] = filepath.Join(root, "missing-ca") }, "REGISTRY_TLS_INVALID"},
		{"invalid_ca", func(s map[string]any) { s["ca_file"] = write("invalid-ca.pem", []byte("invalid certificate")) }, "REGISTRY_TLS_INVALID"},
		{"missing_credential", func(s map[string]any) { s["credential_file"] = filepath.Join(root, "missing-credential") }, "REGISTRY_CREDENTIAL_INVALID"},
		{"invalid_credential", func(s map[string]any) { s["credential_file"] = write("invalid-credential.json", []byte("{}")) }, "REGISTRY_CREDENTIAL_INVALID"},
		{"public_cidr", func(s map[string]any) { s["allowed_cidrs"] = []string{"8.8.8.0/24"} }, "REGISTRY_CONFIGURATION_INVALID"},
		{"foreign_scope_field", func(s map[string]any) { s["tenant_id"] = "foreign" }, "REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID"},
		{"same_origin", func(s map[string]any) { s["endpoint"] = destination.URL }, "REGISTRY_SITE_SOURCE_NOT_DISTINCT"},
		{"same_credentials", func(s map[string]any) { s["credential_file"] = credential }, "REGISTRY_SITE_SOURCE_NOT_DISTINCT"},
	} {
		settings := sourceSettings()
		policy.change(settings)
		selected := selectSource(policy.name, settings)
		cases = append(cases, refusal{policy.name, selected.ImageRegistrySiteSourceFile, policy.code})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := c
			selected.ImageRegistrySiteSourceFile = tc.file
			s, err := openImageRegistry(startup, selected, store, h)
			if s != nil || err == nil || err.Error() != tc.code || d.RegistryActions != nil {
				t.Fatalf("source refusal bound an owner or selected fallback: %v", err)
			}
		})
	}
	for _, sourceFile := range []string{"", c.ImageRegistrySiteSourceFile} {
		t.Run("construct_"+filepath.Base(sourceFile), func(t *testing.T) {
			selected := c
			selected.ImageRegistrySiteSourceFile = sourceFile
			s, err := openImageRegistry(startup, selected, store, h)
			if err != nil || s == nil || d.RegistryActions != s {
				t.Fatal("registry startup did not bind the selected owner", err)
			}
			s.Close()
			s.Close()
			d.RegistryActions = nil
		})
	}
	if requests.Load() != 0 {
		t.Fatal("startup performed a registry request")
	}
	disabled := config.Config{}
	if s, err := openImageRegistry(startup, disabled, store, h); s != nil || err != nil {
		t.Fatal("omitted registry did not retain empty-state admission", err)
	}
	disabled.ImageRegistrySiteSourceFile = c.ImageRegistrySiteSourceFile
	if s, err := openImageRegistry(startup, disabled, store, h); s != nil || err == nil || err.Error() != "CONFIG_IMAGE_REGISTRY_INVALID" {
		t.Fatal("source enabled while registry was disabled", err)
	}
}
