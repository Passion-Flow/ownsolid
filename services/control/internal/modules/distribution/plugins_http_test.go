//go:build plugin_database_integration

package distribution_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

// This real TLS/session/CSRF/generated-schema profile verifies actual signed
// installation and grants only. It does not fabricate tool workflow execution.
func TestM14ActualPluginHumanTLSContractsCSRFStrictInputAndLifecycle(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	c := setup(t)
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	c.s.Identity.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	h := identityhttp.New(c.s.Identity)
	if e = h.Plugins.Service.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := h.Plugins.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	}()
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/plugins/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	base := "/api/v1/admin/plugins"
	workspace := c.p.Me.Workspace.ID
	grants := base + "/workspaces/" + workspace + "/grants"
	available := "/api/v1/workspaces/" + workspace + "/plugins"
	admin.Call("GET", base+"/configuration", nil, 200, "PluginConfigurationResponse")
	portal.Call("GET", base, nil, 404, "Problem")
	portal.Call("GET", available, nil, 200, "PluginGrantPageResponse")
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, c.s.Identity.Config.Policy().Plugins.PackageLimits())
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]any{"capsule": base64.StdEncoding.EncodeToString(raw), "sha256": wire.Digest(raw), "reason": "Actual HTTP signed capsule"}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	csrf := admin.Call("GET", "/api/v1/auth/csrf", nil, 200, "")["token"].(string)
	admin.CSRF = ""
	admin.Call("POST", base+"/imports", input, 403, "Problem")
	admin.CSRF = csrf
	admin.Headers["Origin"] = "https://foreign.example.invalid"
	admin.Call("POST", base+"/imports", input, 403, "Problem")
	delete(admin.Headers, "Origin")
	admin.Headers["Content-Type"] = "text/plain"
	admin.Call("POST", base+"/imports", input, 415, "Problem")
	delete(admin.Headers, "Content-Type")
	for _, body := range []string{`{"capsule":"x","sha256":"x","reason":"test","executor":"shell"}`, `{"capsule":"x","capsule":"y","sha256":"x","reason":"test"}`, `{"capsule":"x","sha256":"x","reason":null}`, `{"Capsule":"x","sha256":"x","reason":"test"}`} {
		admin.Write("POST", base+"/imports", []byte(body), 400, "Problem")
	}
	version := admin.Write("POST", base+"/imports", input, 201, "PluginVersionResponse")
	versionID := version["id"].(string)
	path := base + "/versions/" + versionID
	replay := admin.Write("POST", base+"/imports", input, 201, "PluginVersionResponse")
	if replay["id"] != versionID {
		t.Fatal("HTTP import replay changed identity")
	}
	admin.Call("GET", path, nil, 200, "PluginVersionResponse")
	admin.Call("GET", base+"?limit=1", nil, 200, "PluginVersionPageResponse")
	admin.Call("GET", base+"?unknown=ignored", nil, 400, "Problem")
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	version = admin.Write("POST", path+"/installations", map[string]any{"expected_revision": version["revision"], "reason": "Actual HTTP install"}, 200, "PluginVersionResponse")
	if version["available"] != true || version["state"] != "installed" {
		t.Fatal("installed HTTP declaration unavailable")
	}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	grant := admin.Write("POST", grants, map[string]any{"plugin_version_id": versionID, "reason": "Actual HTTP exact grant"}, 201, "PluginGrantResponse")
	selected := portal.Call("GET", available, nil, 200, "PluginGrantPageResponse")
	items := selected["items"].([]any)
	if len(items) != 1 {
		t.Fatal("actual Portal selector omitted grant")
	}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	grant = admin.Write("PATCH", grants+"/"+grant["id"].(string), map[string]any{"enabled": false, "expected_revision": grant["revision"], "reason": "Actual HTTP revocation"}, 200, "PluginGrantResponse")
	if grant["enabled"] != false {
		t.Fatal("false revocation value lost")
	}
	selected = portal.Call("GET", available, nil, 200, "PluginGrantPageResponse")
	if len(selected["items"].([]any)) != 0 {
		t.Fatal("revoked Portal selector leaked grant")
	}
	refs := admin.Call("GET", path+"/references", nil, 200, "PluginReferencesResponse")
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	version = admin.Write("POST", path+"/uninstallations", map[string]any{"expected_revision": version["revision"], "references_sha256": refs["sha256"], "confirm_referenced_uninstall": false, "reason": "Actual HTTP reference-aware uninstall"}, 200, "PluginVersionResponse")
	if version["state"] != "uninstalled" {
		t.Fatal("HTTP uninstall not durable")
	}
	var versionsCount, grantsCount, runsCount int
	if e = c.s.DB.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM plugin_versions),(SELECT count(*) FROM plugin_grants),(SELECT count(*) FROM workflow_runs)").Scan(&versionsCount, &grantsCount, &runsCount); e != nil || versionsCount != 1 || grantsCount != 1 || runsCount != 0 {
		t.Fatal("HTTP rejects or metadata lifecycle fabricated execution", e)
	}
}
