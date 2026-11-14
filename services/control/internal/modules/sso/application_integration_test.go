//go:build sso_provider_integration

package sso_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM11ActualGiteaProductHTTPBindingAndOrdinaryLoginSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	fixturePath, repo := os.Getenv("PROJECT_ONE_GITEA_FIXTURE_MANIFEST"), os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	if !filepath.IsAbs(fixturePath) || !filepath.IsAbs(repo) {
		t.Fatal("explicit actual fixture and repository required")
	}
	raw, e := os.ReadFile(fixturePath)
	if e != nil {
		t.Fatal("private actual provider receipt required")
	}
	var provider struct {
		Status, Origin  string
		CAFile          string `json:"ca_file"`
		CredentialsFile string `json:"credentials_file"`
	}
	if json.Unmarshal(raw, &provider) != nil || provider.Status != "running_actual_isolated_provider_users_verified" || !strings.HasPrefix(provider.Origin, "https://127.0.0.1:") {
		t.Fatal("running isolated provider required")
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	directory := t.TempDir()
	if e = os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	keyring := filepath.Join(directory, "keyring.json")
	if e = sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	ready := make(chan struct{})
	var handler http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-ready
		if r.URL.Path == "/done" {
			w.Header().Set("Cache-Control", "no-store")
			w.Write([]byte("Private product SSO callback verified"))
			return
		}
		if r.URL.Path == "/login/sso-result" || r.URL.Path == "/account/sso-result" {
			if r.URL.Query().Get("error") != "" {
				http.Error(w, "Product SSO callback failed", 400)
				return
			}
			http.Redirect(w, r, "/done", 303)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	accounts.Config.Server.AdminOrigin = server.URL
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: provider.Origin, Issuer: provider.Origin, DialAddresses: []string{"127.0.0.1"}, CAFile: provider.CAFile}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	accounts.Sources = service
	handler = identityhttp.New(accounts)
	close(ready)
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	call := func(method, path string, input any, key string) map[string]any {
		t.Helper()
		var raw []byte
		if input != nil {
			raw, _ = json.Marshal(input)
		}
		r, e := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(raw))
		if e != nil {
			t.Fatal("private platform request invalid")
		}
		r.Header.Set("Origin", server.URL)
		if input != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if method != "GET" {
			csrfRequest, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/auth/csrf", nil)
			response, e := client.Do(csrfRequest)
			if e != nil {
				t.Fatal("actual platform CSRF failed")
			}
			var envelope struct {
				Data struct {
					Token string `json:"token"`
				} `json:"data"`
			}
			e = json.NewDecoder(response.Body).Decode(&envelope)
			response.Body.Close()
			if e != nil || response.StatusCode != 200 || envelope.Data.Token == "" {
				t.Fatal("actual platform CSRF invalid")
			}
			r.Header.Set("X-CSRF-Token", envelope.Data.Token)
		}
		response, e := client.Do(r)
		if e != nil {
			t.Fatal("actual platform API failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 201 {
			t.Fatalf("actual platform API status %d at fixed path %s", response.StatusCode, path)
		}
		body, e := io.ReadAll(io.LimitReader(response.Body, 65537))
		if e != nil || len(body) > 65536 {
			t.Fatal("actual platform response invalid")
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil {
			t.Fatal("actual platform JSON invalid")
		}
		return envelope.Data
	}
	call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, "")
	me := call("GET", "/api/v1/me", nil, "")
	userID := me["user"].(map[string]any)["id"].(string)
	connector := uuid.NewString()
	callback := server.URL + "/api/v1/auth/sso/" + connector + "/callback"
	register := exec.CommandContext(ctx, "python3", "-B", filepath.Join(repo, "tools/dev/sso/provider.py"), "register-client", "--manifest", fixturePath, "--redirect-uri", callback)
	output, e := register.CombinedOutput()
	if e != nil {
		t.Fatal("actual private confidential application registration failed")
	}
	var registration struct {
		File string `json:"client_credentials_file"`
	}
	for _, line := range strings.Split(string(output), "\n") {
		var value struct {
			File string `json:"client_credentials_file"`
		}
		if json.Unmarshal([]byte(line), &value) == nil && value.File != "" {
			registration = value
		}
	}
	credentials, e := os.ReadFile(registration.File)
	if e != nil {
		t.Fatal("private actual application credential absent")
	}
	var app struct {
		ID     string `json:"client_id"`
		Secret string `json:"client_secret"`
	}
	if json.Unmarshal(credentials, &app) != nil || app.ID == "" || app.Secret == "" {
		t.Fatal("private actual application credential invalid")
	}
	call("POST", "/api/v1/admin/sso/connectors", sso.CreateConnector{ID: connector, Name: "Actual private Gitea", Provider: "gitea", Origin: provider.Origin, ClientID: app.ID, ClientSecret: app.Secret, Enabled: true, Reason: "Create real confidential application connector"}, uuid.NewString())
	call("PATCH", "/api/v1/admin/sso/connectors/"+connector, sso.UpdateConnector{Revision: 1, Name: "Actual private Gitea", Enabled: true, ClientID: &app.ID, ClientSecret: &app.Secret, Reason: "Install actual synthetic confidential application"}, uuid.NewString())
	certificate, e := os.ReadFile(filepath.Join(filepath.Dir(provider.CAFile), "server.pem"))
	if e != nil {
		t.Fatal("actual provider certificate unavailable")
	}
	block, _ := pem.Decode(certificate)
	if block == nil {
		t.Fatal("actual provider PEM invalid")
	}
	parsed, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal("actual provider certificate invalid")
	}
	first, second := sha256.Sum256(parsed.RawSubjectPublicKeyInfo), sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	spki := base64.StdEncoding.EncodeToString(first[:]) + "," + base64.StdEncoding.EncodeToString(second[:])
	for index := 0; index < 2; index++ {
		mode := "link"
		var input any = sso.StartInput{Mode: mode, Password: &password, Reason: stringPointer("Explicit actual provider account link")}
		if index == 1 {
			mode = "login"
			jar, _ = cookiejar.New(nil)
			client.Jar = jar
			input = sso.StartInput{Mode: mode}
		}
		start := call("POST", "/api/v1/auth/sso/"+connector+"/starts", input, "")
		parsedURL, _ := http.NewRequest("GET", server.URL+"/", nil)
		cookies := []map[string]any{}
		var previous string
		for _, cookie := range jar.Cookies(parsedURL.URL) {
			if cookie.Name == "__Host-admin_session" {
				previous = cookie.Value
			}
			if cookie.Name == "__Host-admin_session" || cookie.Name == "__Host-admin_sso_flow" {
				cookies = append(cookies, map[string]any{"name": cookie.Name, "value": cookie.Value, "url": server.URL + "/", "secure": true, "httpOnly": true, "sameSite": "Lax"})
			}
		}
		browserInput := map[string]any{"authorization_url": start["authorization_url"], "callback": callback, "credentials_file": provider.CredentialsFile, "role": "member", "spki": spki, "platform_cookies": cookies, "expected_user_id": userID, "audience": "admin"}
		encoded, _ := json.Marshal(browserInput)
		path := filepath.Join(directory, "browser-"+mode+".json")
		if e = os.WriteFile(path, encoded, 0600); e != nil {
			t.Fatal(e)
		}
		browser := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/sso/provider-flow.mjs"), path)
		output, e := browser.CombinedOutput()
		if e != nil || !strings.Contains(string(output), `"status":"passed_actual_provider_login_consent_callback_only"`) || !strings.Contains(string(output), `"step":"ordinary_platform_session"`) {
			t.Fatal("actual product callback/browser failed; sanitized output: " + string(output))
		}
		if mode == "link" {
			if _, e = accounts.Authenticate(ctx, previous, "admin"); e == nil {
				t.Fatal("actual link did not rotate prior browser session")
			}
		}
	}
	var sessions, bindings, flows int
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE sso_connector_id=$1 AND revoked_at IS NULL`, connector).Scan(&sessions); e != nil || sessions != 2 {
		t.Fatal("actual sourced sessions not persisted", e)
	}
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sso_bindings WHERE connector_id=$1 AND user_id=$2 AND subject='2' AND active`, connector, userID).Scan(&bindings); e != nil || bindings != 1 {
		t.Fatal("actual stable provider subject not linked", e)
	}
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE connector_id=$1 AND state='succeeded' AND verifier_key_id IS NULL AND verifier_ciphertext IS NULL`, connector).Scan(&flows); e != nil || flows != 2 {
		t.Fatal("actual flows not singly consumed", e)
	}
	t.Log("Real Gitea28.1.0 browser login/consent, product Admin TLS/CSRF connector create/update/start/callback cookies, actual code+PKCE/introspection/user, existing-account binding and ordinary persisted login sessions pass. Result-page wrapper is a test receiver; React SSO journeys remain a separate acceptance gate.")
}

func stringPointer(value string) *string { return &value }
