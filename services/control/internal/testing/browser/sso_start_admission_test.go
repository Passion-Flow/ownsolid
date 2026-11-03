package browser_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"testing"
	"time"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20SSOStartAdmissionProductionBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("explicit bounded production browser gate required")
	}
	root, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence {
		t.Fatal("explicit canonical external evidence required")
	}
	output := filepath.Join(evidence, "sso-start-admission-browser")
	if e := os.Mkdir(output, 0700); e != nil {
		t.Fatal("exclusive browser evidence directory", e)
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private synthetic browser fixture")
	}
	keyring := filepath.Join(private, "sso-keyring.json")
	if e := sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	cert, caFile, _, _ := smtpsink.Certificate(t, private)
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	accounts.Config.Server.PortalOrigin = "https://localhost:" + port
	accounts.Config.Server.AdminOrigin = "https://127.0.0.1:" + port
	accounts.Config.Server.PortalAssets = os.Getenv("PROJECT_ONE_BROWSER_PORTAL_ASSETS")
	accounts.Config.Server.AdminAssets = os.Getenv("PROJECT_ONE_BROWSER_ADMIN_ASSETS")
	for _, assets := range []string{accounts.Config.Server.PortalAssets, accounts.Config.Server.AdminAssets} {
		if !filepath.IsAbs(assets) || filepath.Clean(assets) != assets {
			t.Fatal("explicit immutable production asset reference required")
		}
		if _, e := os.Stat(filepath.Join(assets, "index.html")); e != nil {
			t.Fatal("actual production bundle required")
		}
	}
	provider := "https://provider.example.invalid"
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: provider, Issuer: provider, DialAddresses: []string{"127.0.0.1"}}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	accounts.Sources = service
	server.Config.Handler = transport.New(accounts)
	server.StartTLS()
	defer server.Close()
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	_, token, _, e := accounts.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal("actual fixture local Admin authentication", e)
	}
	actor, e := accounts.Authenticate(ctx, token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	connector, e := service.CreateConnector(ctx, actor, sso.CreateConnector{ID: uuid.NewString(), Name: "Synthetic retained account provider \u4e16\u754c", Provider: sso.ProviderGitea, Origin: provider, ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Synthetic browser prerequisite"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual connector prerequisite", e)
	}
	var tenant string
	if e := accounts.DB.QueryRow(ctx, `SELECT tenant_id::text FROM installation_state WHERE id=1`).Scan(&tenant); e != nil {
		t.Fatal(e)
	}
	value := map[string]any{"portal": accounts.Config.Server.PortalOrigin, "admin": accounts.Config.Server.AdminOrigin, "email": "admin@example.invalid", "password": password, "provider": provider, "connector_id": connector.ID, "connector_name": connector.Name, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output}
	raw, _ := json.Marshal(value)
	path := filepath.Join(private, "browser.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal("private browser input", e)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "tests/acceptance/sso/start-admission-flow.mjs"), path)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if e := command.Run(); e != nil {
		t.Fatal("SSO_START_ADMISSION_BROWSER_FAILED")
	}
	var result struct {
		Status       string   `json:"status"`
		Steps        []string `json:"steps"`
		Observations []struct {
			StateHash string `json:"state_sha256"`
			Status    int    `json:"server_status"`
		} `json:"observations"`
	}
	raw, e = os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || len(result.Steps) != 4 || len(result.Observations) != 6 {
		t.Fatal("complete first-party start browser result required")
	}
	expected := map[string]bool{}
	for _, observation := range result.Observations {
		if observation.Status == 200 {
			expected[observation.StateHash] = true
		}
	}
	if len(expected) != 5 {
		t.Fatal("five independently bound successful starts required")
	}
	rows, e := accounts.DB.Query(ctx, `SELECT state_hash,mode,previous_session_id::text,initiating_user_id::text,credential_version FROM sso_flows WHERE tenant_id=$1 AND connector_id=$2 AND state='pending' AND verifier_ciphertext IS NOT NULL AND expires_at>clock_timestamp()`, tenant, connector.ID)
	if e != nil {
		t.Fatal("independent pending-flow browser readback", e)
	}
	login, link := 0, 0
	for rows.Next() {
		var state []byte
		var mode string
		var session, user *string
		var credential *int64
		if e := rows.Scan(&state, &mode, &session, &user, &credential); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		key := hex.EncodeToString(state)
		if !expected[key] {
			rows.Close()
			t.Fatal("browser state binding absent from independent SQL")
		}
		delete(expected, key)
		if mode == sso.ModeLogin {
			login++
			if session != nil || user != nil || credential != nil {
				rows.Close()
				t.Fatal("anonymous browser login invented identity")
			}
		} else if mode == sso.ModeLink {
			link++
			if session == nil || user == nil || *user != actor.Me.User.ID || credential == nil || *credential != 1 {
				rows.Close()
				t.Fatal("browser link lost actual local confirmation")
			}
		} else {
			rows.Close()
			t.Fatal("unexpected browser flow mode")
		}
	}
	if e := rows.Err(); e != nil {
		rows.Close()
		t.Fatal(e)
	}
	rows.Close()
	var audits int
	if e := accounts.DB.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action='sso.flow_started' AND outcome='success'`, tenant).Scan(&audits); e != nil || audits != 5 || login != 3 || link != 2 || len(expected) != 0 {
		t.Fatal("independent start flow/audit acceptance counts", e)
	}
}
