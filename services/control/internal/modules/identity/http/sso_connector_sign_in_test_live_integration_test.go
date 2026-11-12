//go:build sso_provider_integration

package http_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestConnectorSignInTestActualGiteaHTTPS(t *testing.T) {
	p := actualRecoveryGitea(t)
	s, _, _, template, password := setup(t)
	ddl, err := os.ReadFile("../../sso/testdata/connector_sign_in_tests.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	keyring, _, _, _, _ := ssoRecoveryKeyrings(t)
	s.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: p.Origin, Issuer: p.Origin, DialAddresses: []string{"127.0.0.1"}, CAFile: p.CAFile}}}
	source, err := sso.New(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	s.Sources = source
	diagnostic, err := sso.NewConnectorSignInTests(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	h := transport.New(s)
	h.SSO.ConnectorTests = diagnostic
	contracts := maps.Clone(template.schemas)
	for name, schema := range httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json") {
		contracts["sso:"+name] = schema
	}
	admin := newClient(t, accountTLSServer(t, h), "admin", contracts)
	admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	id := uuid.NewString()
	base := "/api/v1/admin/sso/connectors/" + id + "/sign-in-tests"
	clientID, secret := p.application(t, s.Config.Server.AdminOrigin+base+"/callback")
	admin.mutate(http.MethodPost, "/api/v1/admin/sso/connectors", sso.CreateConnector{ID: id, Name: "Actual diagnostic provider", Provider: sso.ProviderGitea, Origin: p.Origin, ClientID: clientID, ClientSecret: secret, Enabled: false, Reason: "Verify identity without a local binding"}, 201, "sso:SSOConnectorResponse")
	authority := (&connectorSignInFixture{accounts: s}).authority(t)
	session := admin.cookies["__Host-admin_session"].Value
	start := admin.mutate(http.MethodPost, base, sso.ConnectorSignInTestInput{Revision: 1, Reason: "Inspect actual provider identity without signing in"}, 200, "")
	recoveryCallback(t, admin, p.authorize(t, start["authorization_url"].(string)), "/sso?test_id="+start["id"].(string), false)
	result := admin.request(http.MethodGet, base+"/"+start["id"].(string), nil, 200, "", nil)
	response, raw := p.request(t, p.browser(), http.MethodGet, p.Origin+sso.GiteaUserPath, nil, true)
	var user struct {
		ID    int64
		Login string
	}
	if response.StatusCode != 200 || json.Unmarshal(raw, &user) != nil || user.ID < 1 || user.Login != p.Username {
		t.Fatal("actual provider identity readback failed")
	}
	if result["state"] != "succeeded" || result["subject"] != strconv.FormatInt(user.ID, 10) || result["login"] != user.Login {
		t.Fatal("diagnostic result differs from actual provider identity")
	}
	if admin.cookies["__Host-admin_session"].Value != session || authority != (&connectorSignInFixture{accounts: s}).authority(t) {
		t.Fatal("actual provider test changed ownsolid login or bindings")
	}
	t.Log("Actual pinned Gitea TLS/password/consent/S256 exchange returned the provider's stable subject and login through a disabled connector, with no local binding, session creation or session-cookie rotation. Explicit candidate composition; installed UI and formal migration admission remain pending.")
}
