//go:build sso_provider_integration

package sso

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestM11ActualGiteaHTTPSPKCECodeIntrospectionAndStableUser(t *testing.T) {
	fixturePath := os.Getenv("PROJECT_ONE_GITEA_FIXTURE_MANIFEST")
	raw, e := readPrivateFile(fixturePath, maximumProviderCertificateBytes)
	if e != nil {
		t.Fatal("actual private fixture manifest required", e)
	}
	var fixture struct {
		Status              string `json:"status"`
		Origin              string `json:"origin"`
		CAFile              string `json:"ca_file"`
		CredentialsFile     string `json:"credentials_file"`
		SyntheticIdentities map[string]struct {
			ID int64 `json:"id"`
		} `json:"synthetic_identities"`
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.Status != "running_actual_isolated_provider_users_verified" || !strings.HasPrefix(fixture.Origin, "https://127.0.0.1:") {
		t.Fatal("actual running isolated fixture required")
	}
	repo := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	if !filepath.IsAbs(repo) {
		t.Fatal("explicit repository root required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	var states sync.Map
	connector := uuid.NewString()
	callbackPath := "/api/v1/auth/sso/" + connector + "/callback"
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/done" {
			w.Write([]byte("Provider callback received"))
			return
		}
		query := r.URL.Query()
		target, ok := states.Load(query.Get("state"))
		if r.Method != "GET" || r.URL.Path != callbackPath || len(query) != 2 || len(query["state"]) != 1 || len(query["code"]) != 1 || !ok || !safeOpaque(query.Get("code"), maximumAuthorizationCodeBytes) {
			http.Error(w, "Invalid callback", 400)
			return
		}
		select {
		case target.(chan string) <- query.Get("code"):
		default:
			http.Error(w, "Callback already consumed", 409)
			return
		}
		http.Redirect(w, r, "/done", 303)
	}))
	defer callback.Close()
	callbackURI := callback.URL + callbackPath
	command := exec.CommandContext(ctx, "python3", "-B", filepath.Join(repo, "tools/dev/sso/provider.py"), "register-client", "--manifest", fixturePath, "--redirect-uri", callbackURI)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatal("actual confidential application registration failed")
	}
	var registration struct {
		Status string `json:"status"`
		File   string `json:"client_credentials_file"`
	}
	for _, line := range strings.Split(string(output), "\n") {
		var value map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &value) == nil && value["client_credentials_file"] != nil {
			json.Unmarshal([]byte(line), &registration)
		}
	}
	if registration.Status != "created_actual_private_confidential_fixture_client" {
		t.Fatal("actual confidential application receipt absent")
	}
	private, e := readPrivateFile(registration.File, 65536)
	if e != nil {
		t.Fatal(e)
	}
	var client struct {
		ID     string `json:"client_id"`
		Secret string `json:"client_secret"`
	}
	if json.Unmarshal(private, &client) != nil || !canonicalID(client.ID) || client.Secret == "" {
		t.Fatal("private actual client credential invalid")
	}
	parsed, e := newGiteaProvider(config.SSOProvider{Origin: fixture.Origin, Issuer: fixture.Origin, DialAddresses: []string{"127.0.0.1"}, CAFile: fixture.CAFile}, config.DefaultSSOPolicy(), []string{callbackURI}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer parsed.close()
	public, e := os.ReadFile(filepath.Join(filepath.Dir(fixture.CAFile), "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(public)
	if block == nil {
		t.Fatal("provider public certificate absent")
	}
	providerCertificate, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	first := sha256.Sum256(providerCertificate.RawSubjectPublicKeyInfo)
	second := sha256.Sum256(callback.Certificate().RawSubjectPublicKeyInfo)
	spki := base64.StdEncoding.EncodeToString(first[:]) + "," + base64.StdEncoding.EncodeToString(second[:])
	for index := 0; index < 2; index++ {
		state, verifier := security.Token(), security.Token()
		received := make(chan string, 1)
		states.Store(state, received)
		authorization, e := parsed.authorizationURL(client.ID, callbackURI, state, verifier)
		if e != nil {
			t.Fatal(e)
		}
		input := map[string]any{"authorization_url": authorization, "callback": callbackURI, "credentials_file": fixture.CredentialsFile, "role": "member", "spki": spki}
		encoded, _ := json.Marshal(input)
		directory := t.TempDir()
		os.Chmod(directory, 0700)
		inputPath := filepath.Join(directory, "private-browser-input.json")
		if e = os.WriteFile(inputPath, encoded, 0600); e != nil {
			t.Fatal(e)
		}
		run := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/sso/provider-flow.mjs"), inputPath)
		browserOutput, e := run.CombinedOutput()
		if e != nil || !strings.Contains(string(browserOutput), `"status":"passed_actual_provider_login_consent_callback_only"`) {
			t.Fatal("actual provider browser failed; sanitized result: " + string(browserOutput))
		}
		var code string
		select {
		case code = <-received:
		case <-ctx.Done():
			t.Fatal("actual single-use callback not received")
		}
		states.Delete(state)
		if index == 0 {
			if _, e = parsed.exchange(ctx, client.ID, client.Secret, callbackURI, code, security.Token()); e == nil {
				t.Fatal("real provider accepted wrong PKCE verifier")
			}
		} else {
			if _, e = parsed.exchange(ctx, client.ID, "synthetic-wrong-client-secret", callbackURI, code, verifier); e == nil {
				t.Fatal("real provider accepted wrong confidential secret")
			}
		}
		identity, e := parsed.exchange(ctx, client.ID, client.Secret, callbackURI, code, verifier)
		if e != nil || identity.subject != "2" || fixture.SyntheticIdentities["member"].ID != 2 {
			t.Fatal("actual narrow introspection/user identity exchange failed", e)
		}
		if _, e = parsed.exchange(ctx, client.ID, client.Secret, callbackURI, code, verifier); e == nil {
			t.Fatal("real provider accepted reused authorization code")
		}
	}
	t.Log("Actual pinned Gitea HTTPS/SQLite/synthetic member login+consent, confidential application, PKCE rejection, narrow authenticated introspection, exact stable user2, wrong-secret rejection and code replay refusal. Callback is an isolated test receiver, not product SSO/session/binding acceptance.")
}
