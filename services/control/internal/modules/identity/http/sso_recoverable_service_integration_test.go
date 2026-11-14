package http_test

import (
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestSSORecoverableServiceHTTPS(t *testing.T) {
	s, _, _, template, password := setup(t)
	contracts := maps.Clone(template.schemas)
	for name, schema := range httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json") {
		contracts["sso:"+name] = schema
	}
	ctx := t.Context()
	original, retained, active, _, _ := ssoRecoveryKeyrings(t)
	var providerCalls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(provider.Close)
	ca := filepath.Join(filepath.Dir(original), "provider-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0600); err != nil {
		t.Fatal("test provider CA unavailable")
	}
	fresh := func(path string) *identity.Service {
		cfg := s.Config
		cfg.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: path, Providers: []config.SSOProvider{{
			Origin: provider.URL, Issuer: provider.URL, DialAddresses: []string{"127.0.0.1"}, CAFile: ca,
		}}}
		return &identity.Service{DB: s.DB, Config: cfg, Passwords: s.Passwords, Mail: s.Mail, HMACKey: s.HMACKey}
	}
	credentials := map[string]string{"email": "admin@example.invalid", "password": password}
	serve := func(path string, recoverable bool) (*identity.Service, *sso.Service, *client) {
		t.Helper()
		accounts := fresh(path)
		build := func() (*sso.Service, error) { return sso.New(accounts) }
		if recoverable {
			build = func() (*sso.Service, error) { return sso.NewWithKeyringRecovery(ctx, accounts) }
		}
		owner, err := build()
		if err != nil {
			t.Fatal("SSO fixture initialization failed", err)
		}
		t.Cleanup(owner.Close)
		accounts.Sources = owner
		h := transport.New(accounts)
		if err := h.InitializationError(); err != nil {
			t.Fatal("identity fixture initialization failed", err)
		}
		admin := newClient(t, accountTLSServer(t, h), "admin", contracts)
		admin.mutate(http.MethodPost, "/api/v1/auth/login", credentials, 200, "LoginResponse")
		return accounts, owner, admin
	}
	const catalog = "/api/v1/admin/sso/connectors"
	const discovery = "/api/v1/auth/sso/connectors"
	startPath := func(id string) string { return "/api/v1/auth/sso/" + id + "/starts" }
	login := map[string]any{"mode": "login", "password": nil, "reason": nil}
	create := func(admin *client, name string) string {
		t.Helper()
		id := uuid.NewString()
		admin.mutate(http.MethodPost, catalog, sso.CreateConnector{ID: id, Name: name, Provider: sso.ProviderGitea,
			Origin: provider.URL, ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Synthetic recovery fixture"}, 201, "sso:SSOConnectorResponse")
		return id
	}
	start := func(admin *client, id string) (string, string) {
		t.Helper()
		browser := newClient(t, admin.s, "admin", contracts)
		result := browser.mutate(http.MethodPost, startPath(id), login, 200, "sso:SSOStartResponse")
		redirect, err := url.Parse(result["authorization_url"].(string))
		if err != nil || redirect.Query().Get("state") == "" || browser.cookies["__Host-admin_sso_flow"] == nil {
			t.Fatal("expected real browser flow proof")
		}
		return redirect.Query().Get("state"), browser.cookies["__Host-admin_sso_flow"].Value
	}
	cipher := func(id string) string {
		t.Helper()
		var encrypted string
		if err := s.DB.QueryRow(ctx, `SELECT secret_key_id||':'||encode(secret_nonce,'hex')||':'||encode(secret_ciphertext,'hex')
			FROM sso_connectors WHERE id=$1`, id).Scan(&encrypted); err != nil {
			t.Fatal("expected encrypted connector")
		}
		return encrypted
	}

	oldAccounts, _, old := serve(original, false)
	unreadable := create(old, "Old key")
	oldState, oldProof := start(old, unreadable)
	state, _ := start(old, unreadable)
	oldToken := issueSSORecoverySource(t, oldAccounts, unreadable, state)
	oldCipher := cipher(unreadable)
	rotatedAccounts, _, rotated := serve(retained, false)
	readable := create(rotated, "Active key")
	newState, newProof := start(rotated, unreadable)
	state, _ = start(rotated, readable)
	readToken := issueSSORecoverySource(t, rotatedAccounts, readable, state)
	readCipher := cipher(readable)
	foreignTenant, foreignConnector := uuid.NewString(), uuid.NewString()
	if _, err := s.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign recovery fixture','active')", foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by)
		SELECT $1,$2,'Foreign recovery fixture',provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by
		FROM sso_connectors WHERE id=$3`, foreignConnector, foreignTenant, unreadable); err != nil {
		t.Fatal(err)
	}
	foreignCipher := cipher(foreignConnector)

	if err := os.Chmod(active, 0644); err != nil {
		t.Fatal(err)
	}
	if owner, err := sso.NewWithKeyringRecovery(ctx, fresh(active)); owner != nil || err == nil || err.Error() != "SSO_KEYRING_INVALID" {
		t.Fatal("unsafe keyring entered recovery mode")
	}
	if err := os.Chmod(active, 0600); err != nil {
		t.Fatal(err)
	}
	untrusted := fresh(active)
	untrusted.Config.SSO.Providers[0].CAFile = ca + ".absent"
	if owner, err := sso.NewWithKeyringRecovery(ctx, untrusted); owner != nil || err == nil || err.Error() != "SSO_PROVIDER_CONFIGURATION_INVALID" {
		t.Fatal("invalid provider trust entered recovery mode")
	}
	accounts, recovery, admin := serve(active, true)
	requirePage := func(accounts *identity.Service, owner *sso.Service, admin *client, want map[string]bool) {
		t.Helper()
		actor, err := accounts.Authenticate(ctx, admin.cookies["__Host-admin_session"].Value, "admin")
		if err != nil {
			t.Fatal("local Admin unavailable", err)
		}
		page, err := owner.ListConnectors(ctx, actor, 10, "")
		if err != nil || len(page.Items) != 2 || !maps.Equal(page.UnreadableConnectorIDs, want) ||
			!page.Items[0].Enabled || !page.Items[1].Enabled {
			t.Fatal("credential state did not preserve connector intent", err)
		}
	}
	requirePage(accounts, recovery, admin, map[string]bool{unreadable: true})
	discovered := admin.request(http.MethodGet, discovery, nil, 200, "sso:PublicSSOConnectorListResponse", nil)["items"].([]any)
	if len(discovered) != 1 || discovered[0].(map[string]any)["id"] != readable {
		t.Fatal("public discovery included an unreadable connector")
	}
	problemCode(t, admin.request(http.MethodGet, catalog+"?tenant_id="+foreignTenant, nil, 400, "sso:Problem", nil), "VALIDATION_FAILED")
	problemCode(t, admin.mutate(http.MethodPost, startPath(foreignConnector), login, 404, "sso:Problem"), "SSO_CONNECTOR_NOT_FOUND")
	problemCode(t, admin.mutate(http.MethodPost, startPath(unreadable), login, 503, "sso:Problem"), "SSO_ENCRYPTED_STATE_INVALID")
	for _, flow := range [][2]string{{oldState, oldProof}, {newState, newProof}} {
		if _, _, err := recovery.Complete(ctx, unreadable, "admin", flow[0], flow[1], "", security.Token(), false, uuid.NewString()); err == nil || err.Error() != "SSO_FLOW_UNUSABLE" {
			t.Fatal("unreadable callback was not refused")
		}
		var terminal bool
		if err := s.DB.QueryRow(ctx, `SELECT state='failed' AND verifier_ciphertext IS NULL AND issued_session_id IS NULL
			FROM sso_flows WHERE state_hash=$1`, security.Digest(flow[0])).Scan(&terminal); err != nil || !terminal {
			t.Fatal("unreadable callback did not terminate without a session", err)
		}
	}
	browser := func(token string) *client {
		result := newClient(t, admin.s, "admin", contracts)
		result.cookies["__Host-admin_session"] = &http.Cookie{Name: "__Host-admin_session", Value: token}
		return result
	}
	problemCode(t, browser(oldToken).request(http.MethodGet, "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	readAdmin := browser(readToken)
	readAdmin.request(http.MethodGet, "/api/v1/me", nil, 200, "MeResponse", nil)
	start(admin, readable)
	if providerCalls.Load() != 0 || cipher(unreadable) != oldCipher || cipher(readable) != readCipher {
		t.Fatal("recovery probed an upstream or rewrote connector ciphertext")
	}

	// Restoring protected custody makes the original connector usable again.
	_, _, restored := serve(retained, false)
	start(restored, unreadable)
	noKeyAccounts, noKeyOwner, noKeyAdmin := serve(filepath.Join(filepath.Dir(active), "absent.json"), true)
	requirePage(noKeyAccounts, noKeyOwner, noKeyAdmin, map[string]bool{readable: true, unreadable: true})
	clientID, clientSecret := uuid.NewString(), security.Token()
	update := sso.UpdateConnector{Revision: 1, Name: "Old key", Enabled: true, ClientID: &clientID, ClientSecret: &clientSecret, Reason: "Replace unreadable fixture credential"}
	target := catalog + "/" + unreadable
	problemCode(t, admin.mutate(http.MethodPatch, catalog+"/"+foreignConnector, update, 404, "sso:Problem"), "SSO_CONNECTOR_NOT_FOUND")
	problemCode(t, noKeyAdmin.mutate(http.MethodPatch, target, update, 503, "sso:Problem"), "SSO_KEYRING_INVALID")
	problemCode(t, readAdmin.mutate(http.MethodPatch, target, update, 403, "sso:Problem"), "ACCESS_DENIED")
	admin.refresh()
	problemCode(t, admin.request(http.MethodPatch, target, update, 403, "sso:Problem", map[string]string{"X-CSRF-Token": ""}), "CSRF_INVALID")
	stale := update
	stale.Revision = 2
	problemCode(t, admin.mutate(http.MethodPatch, target, stale, 409, "sso:Problem"), "SSO_REVISION_CONFLICT")
	if cipher(unreadable) != oldCipher {
		t.Fatal("refused replacement changed the old ciphertext")
	}
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	if _, err := s.DB.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT fixture_refuses_sso_repair CHECK(action<>'sso.connector_updated') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	problemCode(t, admin.request(http.MethodPatch, target, update, 503, "sso:Problem", headers), "STORAGE_UNAVAILABLE")
	if cipher(unreadable) != oldCipher {
		t.Fatal("failed audit changed unreadable connector ciphertext")
	}
	requirePage(accounts, recovery, admin, map[string]bool{unreadable: true})
	if _, err := s.DB.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT fixture_refuses_sso_repair"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result := admin.request(http.MethodPatch, target, update, 200, "sso:SSOConnectorResponse", headers)
		if result["revision"] != float64(2) || result["auth_version"] != float64(2) || result["client_id"] != clientID {
			t.Fatal("replacement did not commit the correct revision")
		}
	}
	requirePage(accounts, recovery, admin, nil)
	start(admin, unreadable)
	var events int
	var leaked bool
	if err := s.DB.QueryRow(ctx, `SELECT count(*),coalesce(bool_or(strpos(to_jsonb(a)::text,$2)>0),false)
		FROM audit_events a WHERE target_type='sso_connector' AND target_id=$1 AND action='sso.connector_updated'`, unreadable, clientSecret).
		Scan(&events, &leaked); err != nil || events != 1 || leaked || cipher(readable) != readCipher || cipher(foreignConnector) != foreignCipher {
		t.Fatal("replacement audit or connector isolation failed", err)
	}
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1", foreignConnector).Scan(&events); err != nil || events != 0 {
		t.Fatal("refused foreign connector operation created an audit mutation", err)
	}

	disable := sso.UpdateConnector{Revision: 1, Name: "Active key", Enabled: false, Reason: "Disable without protected key material"}
	headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	for range 2 {
		result := noKeyAdmin.request(http.MethodPatch, catalog+"/"+readable, disable, 200, "sso:SSOConnectorResponse", headers)
		if result["revision"] != float64(2) || result["auth_version"] != float64(2) || result["enabled"] != false {
			t.Fatal("missing-key disable did not retain its committed result")
		}
	}
	var revoked bool
	if err := s.DB.QueryRow(ctx, "SELECT revoked_at IS NOT NULL AND revoke_reason='sso_connector_changed' FROM sessions WHERE token_hash=$1", security.Digest(readToken)).Scan(&revoked); err != nil || !revoked {
		t.Fatal("missing-key disable did not revoke the sourced session", err)
	}
	problemCode(t, readAdmin.request(http.MethodGet, "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	noKeyAdmin.request(http.MethodGet, "/api/v1/me", nil, 200, "MeResponse", nil)
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='sso.connector_updated'", readable).Scan(&events); err != nil || events != 1 || cipher(readable) != readCipher || cipher(foreignConnector) != foreignCipher {
		t.Fatal("missing-key disable changed ciphertext or duplicated its audit", err)
	}
	if providerCalls.Load() != 0 {
		t.Fatal("credential recovery contacted the synthetic provider")
	}
}

// Build the provider-claim fixture through the real PostgreSQL transitions,
// then let identity issue and validate the sourced session.
func issueSSORecoverySource(t *testing.T, accounts *identity.Service, connector, state string) string {
	t.Helper()
	var issued identity.IssuedSession
	ctx := t.Context()
	err := pgx.BeginFunc(ctx, accounts.DB, func(tx pgx.Tx) error {
		source := identity.SessionSource{ConnectorID: connector, BindingID: uuid.NewString()}
		var user string
		var epoch int64
		if err := tx.QueryRow(ctx, `SELECT i.tenant_id::text,u.id::text,u.auth_epoch FROM installation_state i
			JOIN users u ON u.id=i.primary_admin_user_id WHERE i.id=1`).Scan(&source.TenantID, &user, &epoch); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'1',$4)",
			source.BindingID, source.TenantID, connector, user); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL
			WHERE state_hash=$2 RETURNING id::text,auth_version`, uuid.NewString(), security.Digest(state)).Scan(&source.FlowID, &source.AuthVersion); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE id=$2", user, source.FlowID); err != nil {
			return err
		}
		var err error
		issued, err = accounts.IssueSession(ctx, tx, user, epoch, "admin", "", uuid.NewString(), &source)
		return err
	})
	if err != nil {
		t.Fatal("actual sourced identity fixture failed", err)
	}
	return issued.Token
}
