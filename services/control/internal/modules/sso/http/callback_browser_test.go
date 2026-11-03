//go:build repository_integration

package http_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20SSOCallbackUnknownProductionBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("explicit bounded browser gate required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	repo, e := filepath.Abs("../../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence {
		t.Fatal("canonical owned browser evidence required")
	}
	out := filepath.Join(evidence, "sso-callback-browser")
	if e := os.Mkdir(out, 0700); e != nil {
		t.Fatal(e)
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private browser fixture")
	}
	cert, ca, _, _ := smtpsink.Certificate(t, private)
	keyring := filepath.Join(private, "sso-keyring.json")
	if e := sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	protocol := &callbackProtocolDouble{client: uuid.NewString(), secret: security.Token(), code: security.Token()}
	connector := uuid.NewString()
	var authorizations int
	var controlMu sync.Mutex
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	accounts.Config.Server.PortalOrigin = "https://localhost:" + port
	accounts.Config.Server.AdminOrigin = "https://127.0.0.1:" + port
	accounts.Config.Server.PortalAssets = os.Getenv("PROJECT_ONE_BROWSER_PORTAL_ASSETS")
	accounts.Config.Server.AdminAssets = os.Getenv("PROJECT_ONE_BROWSER_ADMIN_ASSETS")
	for _, path := range []string{accounts.Config.Server.PortalAssets, accounts.Config.Server.AdminAssets} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("explicit immutable production bundle required")
		}
		if _, e := os.Stat(filepath.Join(path, "index.html")); e != nil {
			t.Fatal(e)
		}
	}
	callbackPath := "/api/v1/auth/sso/" + connector + "/callback"
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != sso.GiteaAuthorizationPath {
			protocol.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		callback := q.Get("redirect_uri")
		if r.Method != "GET" || q.Get("client_id") != protocol.client || q.Get("scope") != sso.GiteaScope || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !security.ValidToken(q.Get("state")) || q.Get("code_challenge") == "" || (callback != accounts.Config.Server.PortalOrigin+callbackPath && callback != accounts.Config.Server.AdminOrigin+callbackPath) {
			http.Error(w, "Invalid synthetic authorization", 400)
			return
		}
		// The navigation/identity provider is an explicit test double. This
		// issues no real external identity and makes no Gitea qualification claim.
		protocol.callback, protocol.challenge = callback, q.Get("code_challenge")
		authorizations++
		http.Redirect(w, r, callback+"?code="+protocol.code+"&state="+q.Get("state"), 303)
	}))
	provider.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	provider.StartTLS()
	defer provider.Close()
	protocol.origin = provider.URL
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: provider.URL, Issuer: provider.URL, DialAddresses: []string{"127.0.0.1"}, CAFile: ca}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	accounts.Sources = service
	actor := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	const providerName = "Synthetic callback recovery provider"
	if _, e := service.CreateConnector(ctx, actor, sso.CreateConnector{ID: connector, Name: providerName, Provider: sso.ProviderGitea, Origin: provider.URL, ClientID: protocol.client, ClientSecret: protocol.secret, Enabled: true, Reason: "Synthetic browser recovery prerequisite"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = identityhttp.New(accounts)
	server.StartTLS()
	defer server.Close()
	proxy := callbackWire(t, accounts)
	admission := accounts.BusinessAdmission
	defer func() { accounts.BusinessAdmission = admission }()
	selected := os.Getenv("PROJECT_ONE_CALLBACK_BROWSER_CASE")
	allowed := map[string]bool{"portal_claim": true, "admin_claim": true, "portal_completion": true, "admin_completion": true}
	if !allowed[selected] {
		t.Fatal("explicit owned single browser journey required")
	}
	order := []string{selected}
	completions := 0
	if strings.HasSuffix(selected, "_completion") {
		completions = 1
	}
	next := 0
	armed := false
	controlToken := security.Token()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controlMu.Lock()
		defer controlMu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/" || r.Header.Get("X-Control-Token") != controlToken {
			http.Error(w, "Private test control refused", 403)
			return
		}
		var input struct {
			Action string `json:"action"`
			Case   string `json:"case"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || next >= len(order) || input.Case != order[next] {
			http.Error(w, "Private test order invalid", 400)
			return
		}
		if input.Action == "arm" && !armed {
			protocol.beforeUser = nil
			accounts.BusinessAdmission = admission
			if strings.HasSuffix(input.Case, "_claim") {
				accounts.BusinessAdmission = &callbackClaimFault{BusinessAdmission: admission, proxy: proxy, mode: commitfault.DropCommitAcknowledgement}
			} else {
				protocol.beforeUser = func() {
					if e := proxy.Arm(commitfault.DropCommitAcknowledgement); e != nil {
						t.Error(e)
					}
				}
			}
			armed = true
		} else if input.Action == "check" && armed {
			select {
			case observed := <-proxy.Outcomes():
				if observed.Mode != commitfault.DropCommitAcknowledgement || !observed.Forwarded || !observed.ServerOK || !observed.Suppressed {
					http.Error(w, "Actual wire outcome mismatch", 500)
					return
				}
			case <-time.After(3 * time.Second):
				http.Error(w, "Actual wire outcome missing", 500)
				return
			}
			accounts.BusinessAdmission = admission
			protocol.beforeUser = nil
			next++
			armed = false
		} else {
			http.Error(w, "Private test state invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer control.Close()
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	input := map[string]any{"portal": accounts.Config.Server.PortalOrigin, "admin": accounts.Config.Server.AdminOrigin, "provider": provider.URL, "provider_name": providerName, "connector_id": connector, "email": "admin@example.invalid", "password": password, "spki": base64.StdEncoding.EncodeToString(pin[:]), "control": control.URL, "control_token": controlToken, "output": out, "cases": order}
	raw, _ := json.Marshal(input)
	path := filepath.Join(private, "browser-input.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private browser input")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/sso/callback-recovery-flow.mjs"), path)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	if e := command.Run(); e != nil {
		t.Fatal("SSO_CALLBACK_RECOVERY_BROWSER_FAILED")
	}
	var result struct {
		Status       string   `json:"status"`
		Steps        []string `json:"steps"`
		Observations []struct {
			Case  string `json:"case"`
			State string `json:"state_sha256"`
		} `json:"observations"`
	}
	raw, e = os.ReadFile(filepath.Join(out, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || len(result.Steps) != len(order) || len(result.Observations) != len(order) || next != len(order) || authorizations != len(order) || protocol.requests.Load() != int32(3*completions) {
		t.Fatal("complete actual callback browser/wire/provider-double result required")
	}
	seen := map[string]bool{}
	for index, item := range result.Observations {
		digest, e := hex.DecodeString(item.State)
		if e != nil || len(digest) != sha256.Size || seen[item.State] || item.Case != order[index] {
			t.Fatal("browser callback state binding invalid")
		}
		seen[item.State] = true
		var state, mode, audience string
		var erased, issued bool
		if e := accounts.DB.QueryRow(ctx, `SELECT state,mode,audience,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL),issued_session_id IS NOT NULL FROM sso_flows WHERE state_hash=$1`, digest).Scan(&state, &mode, &audience, &erased, &issued); e != nil {
			t.Fatal(e)
		}
		wantState, wantMode, wantIssued := "claimed", "login", false
		if strings.HasSuffix(item.Case, "_completion") {
			wantState, wantMode, wantIssued = "succeeded", "link", true
		}
		wantAudience := "portal"
		if strings.HasPrefix(item.Case, "admin_") {
			wantAudience = "admin"
		}
		if state != wantState || mode != wantMode || audience != wantAudience || !erased || issued != wantIssued {
			t.Fatal("independent browser flow/verifier/issuance mismatch")
		}
	}
	for query, want := range map[string]int{`SELECT COUNT(*) FROM sso_flows`: len(order), `SELECT COUNT(*) FROM sessions WHERE sso_flow_id IS NOT NULL`: completions, `SELECT COUNT(*) FROM sso_bindings WHERE active`: completions, `SELECT COUNT(*) FROM audit_events WHERE action='auth.sso_login_succeeded'`: completions, `SELECT COUNT(*) FROM audit_events WHERE action='sso.binding_linked'`: completions} {
		var actual int
		if e := accounts.DB.QueryRow(ctx, query).Scan(&actual); e != nil || actual != want {
			t.Fatal("independent callback browser atomic inventory", e)
		}
	}
	t.Log("One actual production React/TLS/PG callback uncertainty and local recovery journey;one actual suppressed server COMMIT ACK;explicit provider double;independent flow/session/binding/audit readback.")
}
