package http_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestSSOKeyringRecoveryBoundaryHTTPS(t *testing.T) {
	s, _, _, template, password := setup(t)
	contracts := maps.Clone(template.schemas)
	for name, schema := range httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json") {
		contracts["sso:"+name] = schema
	}
	original, retained, activeOnly, mismatched, keyMaterial := ssoRecoveryKeyrings(t)
	missing := filepath.Join(filepath.Dir(original), "missing.json")
	clientSecret, replacement := security.Token(), security.Token()
	private := append(keyMaterial, original, retained, activeOnly, mismatched, missing, clientSecret, replacement)
	redacted := func(raw string) {
		t.Helper()
		for _, value := range private {
			if strings.Contains(raw, value) {
				t.Fatal("SSO diagnostic or persisted audit exposed private material")
			}
		}
	}
	redactedResponse := func(response map[string]any) {
		t.Helper()
		raw, err := json.Marshal(response)
		if err != nil {
			t.Fatal("invalid SSO fixture response")
		}
		redacted(string(raw))
	}
	const origin = "https://provider.example.invalid"
	fresh := func(path string, enabled bool) *identity.Service {
		cfg := s.Config
		cfg.SSO = config.SSOConfiguration{Enabled: enabled, KeyringFile: path, Providers: []config.SSOProvider{{
			Origin: origin, Issuer: origin, DialAddresses: []string{"127.0.0.1"},
		}}}
		return &identity.Service{DB: s.DB, Config: cfg, Passwords: s.Passwords, Mail: s.Mail, HMACKey: s.HMACKey}
	}
	localLogin := map[string]string{"email": "admin@example.invalid", "password": password}
	serve := func(path string, enabled bool) *client {
		t.Helper()
		accounts := fresh(path, enabled)
		source, err := sso.New(accounts)
		if err != nil {
			t.Fatal("SSO fixture startup refused", err)
		}
		t.Cleanup(source.Close)
		accounts.Sources = source
		handler := transport.New(accounts)
		if err := handler.InitializationError(); err != nil {
			t.Fatal("identity fixture composition refused", err)
		}
		admin := newClient(t, accountTLSServer(t, handler), "admin", contracts)
		admin.mutate(http.MethodPost, "/api/v1/auth/login", localLogin, 200, "LoginResponse")
		return admin
	}
	const catalog = "/api/v1/admin/sso/connectors"
	const public = "/api/v1/auth/sso/connectors"
	id := uuid.NewString()
	startPath := "/api/v1/auth/sso/" + id + "/starts"
	start := map[string]any{"mode": "login", "password": nil, "reason": nil}
	admin := serve(original, true)
	redactedResponse(admin.mutate(http.MethodPost, catalog, sso.CreateConnector{
		ID: id, Name: "Synthetic SSO recovery", Provider: sso.ProviderGitea, Origin: origin,
		ClientID: uuid.NewString(), ClientSecret: clientSecret, Enabled: true, Reason: "Synthetic keyring recovery fixture",
	}, 201, "sso:SSOConnectorResponse"))
	redactedResponse(admin.mutate(http.MethodPost, startPath, start, 200, "sso:SSOStartResponse"))

	for _, tc := range []struct{ path, code string }{
		{missing, "SSO_KEYRING_INVALID"},
		{mismatched, "SSO_ENCRYPTED_STATE_INVALID"},
		{activeOnly, "SSO_ENCRYPTED_STATE_INVALID"},
	} {
		broken := transport.New(fresh(tc.path, true))
		err := broken.InitializationError()
		if err == nil || err.Error() != tc.code || broken.SSO.Service != nil {
			t.Fatalf("expected %s to prevent identity startup", tc.code)
		}
		redacted(err.Error())
	}

	disabled := serve(missing, false)
	disabled.request(http.MethodGet, "/api/v1/me", nil, 200, "MeResponse", nil)
	if result := disabled.request(http.MethodGet, public, nil, 200, "sso:PublicSSOConnectorListResponse", nil); len(result["items"].([]any)) != 0 {
		t.Fatal("disabled recovery listed a public connector")
	}
	result := disabled.request(http.MethodGet, catalog, nil, 200, "sso:SSOConnectorPageResponse", nil)
	items := result["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["enabled"] != true {
		t.Fatal("disabled recovery changed the stored connector intent")
	}
	redactedResponse(result)
	denial := disabled.mutate(http.MethodPost, startPath, start, 503, "sso:Problem")
	problemCode(t, denial, "SSO_DISABLED")
	redactedResponse(denial)
	clientID := uuid.NewString()
	update := sso.UpdateConnector{Revision: 1, Name: "Synthetic SSO recovery", Enabled: true,
		ClientID: &clientID, ClientSecret: &replacement, Reason: "Replace the fixture credential"}
	denial = disabled.mutate(http.MethodPatch, catalog+"/"+id, update, 503, "sso:Problem")
	problemCode(t, denial, "SSO_DISABLED")
	redactedResponse(denial)

	restored := serve(retained, true)
	portal := newClient(t, restored.s, "portal", contracts)
	portal.mutate(http.MethodPost, "/api/v1/auth/login", localLogin, 200, "LoginResponse")
	denial = portal.mutate(http.MethodPatch, catalog+"/"+id, update, 404, "sso:Problem")
	problemCode(t, denial, "RESOURCE_NOT_FOUND")
	redactedResponse(denial)
	restored.refresh()
	denial = restored.request(http.MethodPatch, catalog+"/"+id, update, 403, "sso:Problem", map[string]string{"Origin": "https://portal.example.invalid"})
	problemCode(t, denial, "ORIGIN_REJECTED")
	redactedResponse(denial)
	foreign := struct {
		sso.UpdateConnector
		TenantID string `json:"tenant_id"`
	}{update, uuid.NewString()}
	denial = restored.mutate(http.MethodPatch, catalog+"/"+id, foreign, 400, "sso:Problem")
	problemCode(t, denial, "VALIDATION_FAILED")
	redactedResponse(denial)
	changed := restored.mutate(http.MethodPatch, catalog+"/"+id, update, 200, "sso:SSOConnectorResponse")
	if changed["id"] != id || changed["client_id"] != clientID || changed["revision"] != float64(2) || changed["auth_version"] != float64(2) {
		t.Fatal("credential replacement did not update the original connector")
	}
	redactedResponse(changed)

	restarted := serve(activeOnly, true)
	result = restarted.mutate(http.MethodPost, startPath, start, 200, "sso:SSOStartResponse")
	redirect, err := url.Parse(result["authorization_url"].(string))
	if err != nil || redirect.Query().Get("client_id") != clientID || redirect.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("replaced connector did not start a flow under the active key")
	}
	redactedResponse(result)
	var updated int
	var audit string
	if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE action='sso.connector_updated' AND audience='admin'
		AND actor_user_id=(SELECT primary_admin_user_id FROM installation_state WHERE id=1)),
		COALESCE(string_agg(to_jsonb(a)::text,''),'') FROM audit_events a WHERE target_type='sso_connector' AND target_id=$1`, id).
		Scan(&updated, &audit); err != nil || updated != 1 {
		t.Fatalf("expected one authorized connector replacement audit, got %d: %v", updated, err)
	}
	redacted(audit)
}

func ssoRecoveryKeyrings(t *testing.T) (original, retained, activeOnly, mismatched string, material []string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil || os.Chmod(dir, 0700) != nil {
		t.Fatal("private keyring fixture directory unavailable")
	}
	original, retained = filepath.Join(dir, "original.json"), filepath.Join(dir, "retained.json")
	if err := sso.InitializeKeyring(original, ""); err != nil {
		t.Fatal(err)
	}
	if err := sso.InitializeKeyring(retained, original); err != nil {
		t.Fatal(err)
	}
	type ring struct {
		Version int               `json:"schema_version"`
		Active  string            `json:"active_key_id"`
		Keys    map[string]string `json:"keys"`
	}
	read := func(path string) ring {
		t.Helper()
		raw, err := os.ReadFile(path)
		defer clear(raw)
		var parsed ring
		if err != nil || json.Unmarshal(raw, &parsed) != nil || parsed.Keys[parsed.Active] == "" {
			t.Fatal("invalid generated keyring")
		}
		return parsed
	}
	write := func(name, id, key string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		raw, err := json.Marshal(ring{Version: 1, Active: id, Keys: map[string]string{id: key}})
		defer clear(raw)
		if err != nil || os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("private keyring fixture could not be written")
		}
		return path
	}
	old, rotated := read(original), read(retained)
	newKey := rotated.Keys[rotated.Active]
	return original, retained, write("active-only.json", rotated.Active, newKey),
		write("mismatched.json", old.Active, newKey), []string{old.Keys[old.Active], newKey}
}
