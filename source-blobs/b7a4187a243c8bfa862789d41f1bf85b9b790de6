package http_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestM11ActualTLSHumanRoutesStrictContractsCSRFAndLocalRecovery(t *testing.T) {
	admin, portal, password, accounts := configuredHTTP(t, nil)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal.Call("GET", "/api/v1/auth/sso/connectors", nil, 200, "PublicSSOConnectorListResponse")
	portal.Write("POST", "/api/v1/admin/sso/connectors", map[string]any{}, 404, "Problem")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	input := map[string]any{"id": uuid.NewString(), "name": "Synthetic provider", "provider": "gitea", "origin": "https://provider.example.invalid", "client_id": uuid.NewString(), "client_secret": security.Token(), "enabled": true, "reason": "Synthetic actual human HTTP integration"}
	created := admin.Write("POST", "/api/v1/admin/sso/connectors", input, 201, "SSOConnectorResponse")
	id := created["id"].(string)
	admin.Write("POST", "/api/v1/admin/sso/connectors", input, 201, "SSOConnectorResponse")
	admin.Call("GET", "/api/v1/admin/sso/connectors?limit=1", nil, 200, "SSOConnectorPageResponse")
	admin.Call("GET", "/api/v1/admin/sso/connectors?limit=01", nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/sso/connectors?cursor=%zz", nil, 400, "Problem")
	configuration := admin.Call("GET", "/api/v1/admin/sso/configuration", nil, 200, "SSOConfigurationResponse")
	if configuration["enabled"] != true || len(configuration["providers"].([]any)) != 1 {
		t.Fatalf("configuration %v", configuration)
	}
	admin.Call("GET", "/api/v1/admin/sso/configuration?x=1", nil, 400, "Problem")
	portal.Call("GET", "/api/v1/admin/sso/configuration", nil, 404, "Problem")
	// Nothing listens on the provider's dial address, so the check reports it, and is audited.
	check := admin.Write("POST", "/api/v1/admin/sso/connectors/"+id+"/tests", nil, 200, "SSOConnectorCheckResponse")
	if check["outcome"] != "unreachable" || check["connector_id"] != id {
		t.Fatalf("check %v", check)
	}
	admin.Write("POST", "/api/v1/admin/sso/connectors/"+id+"/tests", map[string]any{}, 400, "Problem")
	admin.Write("POST", "/api/v1/admin/sso/connectors/"+uuid.NewString()+"/tests", nil, 404, "Problem")
	portal.Write("POST", "/api/v1/admin/sso/connectors/"+id+"/tests", nil, 404, "Problem")
	var tested int
	if e := accounts.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_events WHERE action='sso.connector_tested' AND outcome='failure' AND target_id=$1 AND reason='unreachable'`, id).Scan(&tested); e != nil || tested != 1 {
		t.Fatalf("audited checks %d %v", tested, e)
	}
	for _, raw := range []string{
		`{"mode":"login","password":null}`,
		`{"mode":"login","password":null,"reason":null,"return_url":"https://outside.example.invalid"}`,
		`{"Mode":"login","password":null,"reason":null}`,
		`{"mode":"login","mode":"login","password":null,"reason":null}`,
		`{"mode":null,"password":null,"reason":null}`,
	} {
		admin.Write("POST", "/api/v1/auth/sso/"+id+"/starts", []byte(raw), 400, "Problem")
	}
	start := portal.Write("POST", "/api/v1/auth/sso/"+id+"/starts", map[string]any{"mode": "login", "password": nil, "reason": nil}, 200, "SSOStartResponse")
	if start["authorization_url"] == nil || start["browser_token"] != nil {
		t.Fatal("public flow response escaped its contract")
	}
	admin.Write("POST", "/api/v1/auth/sso/"+id+"/starts", map[string]any{"mode": "link", "password": security.Token(), "reason": "Wrong password"}, 403, "Problem")
	admin.Call("GET", "/api/v1/account/sso/bindings", nil, 200, "SSOBindingListResponse")
	for _, query := range []string{"state=x&code=y", "state=x&state=y&code=z", "state=x&code=y&error=access_denied", "state=%zz&code=y"} {
		portal.Call("GET", "/api/v1/auth/sso/"+id+"/callback?"+query, nil, 400, "Problem")
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("PATCH", "/api/v1/admin/sso/connectors/"+id, map[string]any{"revision": 1, "name": "Disabled synthetic provider", "enabled": false, "client_id": nil, "client_secret": nil, "reason": "Local recovery remains available"}, 200, "SSOConnectorResponse")
	portal.Write("POST", "/api/v1/auth/sso/"+id+"/starts", map[string]any{"mode": "login", "password": nil, "reason": nil}, 409, "Problem")
	admin.Call("GET", "/api/v1/me", nil, 200, "")
}

func configuredHTTP(t *testing.T, adjust func(*config.RuntimePolicy)) (*httpfixture.Client, *httpfixture.Client, string, *identity.Service) {
	t.Helper()
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	keyring := filepath.Join(directory, "keyring.json")
	if e := sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	if adjust != nil {
		policy := accounts.Config.Policy()
		adjust(&policy)
		if e := policy.Validate(); e != nil {
			t.Fatal(e)
		}
		accounts.Config.Runtime = &policy
	}
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: "https://provider.example.invalid", Issuer: "https://provider.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	accounts.Sources = service
	server := httpfixture.NewTLSServer(t, identityhttp.New(accounts))
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json")
	return httpfixture.New(t, server, "admin", schemas), httpfixture.New(t, server, "portal", schemas), password, accounts
}

func TestM11ActualNondefaultIndependentStartCallbackAndPasswordRateWindows(t *testing.T) {
	admin, portal, password, _ := configuredHTTP(t, func(policy *config.RuntimePolicy) {
		policy.SSO.StartIPCount = 2
		policy.SSO.CallbackIPCount = 1
		policy.SSO.PasswordIPCount = 1
	})
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	created := admin.Write("POST", "/api/v1/admin/sso/connectors", sso.CreateConnector{ID: uuid.NewString(), Name: "Rate-limited provider", Provider: sso.ProviderGitea, Origin: "https://provider.example.invalid", ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Synthetic nondefault rate policy"}, 201, "SSOConnectorResponse")
	base := "/api/v1/auth/sso/" + created["id"].(string)
	for i := 0; i < 2; i++ {
		portal.Write("POST", base+"/starts", sso.StartInput{Mode: sso.ModeLogin}, 200, "SSOStartResponse")
	}
	portal.Write("POST", base+"/starts", sso.StartInput{Mode: sso.ModeLogin}, 429, "Problem")
	reason, wrong := "Synthetic password attempt", security.Token()
	admin.Write("POST", base+"/starts", sso.StartInput{Mode: sso.ModeLink, Password: &wrong, Reason: &reason}, 403, "Problem")
	admin.Write("POST", base+"/starts", sso.StartInput{Mode: sso.ModeLink, Password: &wrong, Reason: &reason}, 429, "Problem")
	callback := base + "/callback?state=" + security.Token() + "&code=synthetic-unclaimed-code"
	portal.Call("GET", callback, nil, 400, "Problem")
	portal.Call("GET", callback, nil, 429, "Problem")
	admin.Call("GET", "/api/v1/me", nil, 200, "")
}
