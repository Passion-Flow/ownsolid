//go:build sso_browser_integration

package sso_test

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
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM11ActualDualReactGiteaBindingLoginUnlinkAndConnectorRevocation(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) { t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale); actualDualReactSSO(t) })
	}
}
func actualDualReactSSO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	repo, fixturePath, evidence := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), os.Getenv("PROJECT_ONE_GITEA_FIXTURE_MANIFEST"), os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(repo) || !filepath.IsAbs(fixturePath) || !filepath.IsAbs(evidence) {
		t.Fatal("explicit real browser/fixture/evidence configuration required")
	}
	raw, e := os.ReadFile(fixturePath)
	if e != nil {
		t.Fatal("actual private provider receipt required")
	}
	var provider struct {
		Status, Origin  string
		CAFile          string `json:"ca_file"`
		CredentialsFile string `json:"credentials_file"`
	}
	if json.Unmarshal(raw, &provider) != nil || provider.Status != "running_actual_isolated_provider_users_verified" {
		t.Fatal("actual private provider is not running")
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	admin := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	memberPassword := fixture.Password()
	invite, e := accounts.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "admin@provider.example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = accounts.Register(ctx, fixture.InvitationToken(t, accounts, invite.ID), "SSO browser member", memberPassword, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var memberID string
	if e = accounts.DB.QueryRow(ctx, `SELECT id::text FROM users WHERE email_canonical='admin@provider.example.invalid'`).Scan(&memberID); e != nil {
		t.Fatal(e)
	}
	locale := os.Getenv("PROJECT_ONE_BROWSER_LOCALE")
	if _, e = accounts.DB.Exec(ctx, "UPDATE users SET locale=$1", locale); e != nil {
		t.Fatal(e)
	}
	private := t.TempDir()
	if e = os.Chmod(private, 0700); e != nil {
		t.Fatal(e)
	}
	keyring := filepath.Join(private, "keyring.json")
	if e = sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	ready := make(chan struct{})
	var handler http.Handler
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-ready; handler.ServeHTTP(w, r) })
	adminServer := httptest.NewTLSServer(serve)
	defer adminServer.Close()
	portalServer := httptest.NewTLSServer(serve)
	defer portalServer.Close()
	accounts.Config.Server.AdminOrigin = adminServer.URL
	accounts.Config.Server.PortalOrigin = portalServer.URL
	accounts.Config.Server.AdminAssets = os.Getenv("PROJECT_ONE_BROWSER_ADMIN_ASSETS")
	accounts.Config.Server.PortalAssets = os.Getenv("PROJECT_ONE_BROWSER_PORTAL_ASSETS")
	for _, root := range []string{accounts.Config.Server.AdminAssets, accounts.Config.Server.PortalAssets} {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			t.Fatal("explicit canonical immutable production assets required")
		}
		if _, e = os.Stat(filepath.Join(root, "index.html")); e != nil {
			t.Fatal("actual production frontend build required")
		}
	}
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: provider.Origin, Issuer: provider.Origin, DialAddresses: []string{"127.0.0.1"}, CAFile: provider.CAFile}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	accounts.Sources = service
	handler = identityhttp.New(accounts)
	close(ready)
	certificate, e := os.ReadFile(filepath.Join(filepath.Dir(provider.CAFile), "server.pem"))
	if e != nil {
		t.Fatal("actual private certificate required")
	}
	block, _ := pem.Decode(certificate)
	if block == nil {
		t.Fatal("actual private certificate invalid")
	}
	parsed, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal("actual private certificate invalid")
	}
	pins := []string{}
	for _, leaf := range []*x509.Certificate{parsed, adminServer.Certificate(), portalServer.Certificate()} {
		digest := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		pins = append(pins, base64.StdEncoding.EncodeToString(digest[:]))
	}
	output := filepath.Join(evidence, "real-dual-react-"+locale)
	if e = os.Mkdir(output, 0700); e != nil {
		t.Fatal(e)
	}
	input := map[string]any{"locale": locale, "admin": adminServer.URL, "portal": portalServer.URL, "provider": provider.Origin, "provider_manifest": fixturePath, "provider_credentials_file": provider.CredentialsFile, "repository": repo, "admin_email": "admin@example.invalid", "admin_password": password, "admin_id": admin.Me.User.ID, "member_email": "admin@provider.example.invalid", "member_password": memberPassword, "member_id": memberID, "workspace_id": admin.Me.Workspace.ID, "spki": strings.Join(pins, ","), "output": output}
	encoded, _ := json.Marshal(input)
	path := filepath.Join(private, "browser-input.json")
	if e = os.WriteFile(path, encoded, 0600); e != nil {
		t.Fatal(e)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/sso/product-flow.mjs"), path)
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	result, e := command.CombinedOutput()
	if e != nil || !strings.Contains(string(result), `"status":"passed_actual_dual_react_sso"`) {
		var outcomes []byte
		if err := accounts.DB.QueryRow(ctx, `SELECT COALESCE(json_agg(json_build_object('mode',mode,'state',state,'error_code',error_code)),'[]'::json) FROM sso_flows`).Scan(&outcomes); err == nil {
			if err = os.WriteFile(filepath.Join(output, "database-outcome.json"), outcomes, 0600); err != nil {
				t.Error("private database outcome write failed")
			}
			t.Log("Stable database flow outcomes: " + string(outcomes))
		}
		t.Fatal("actual SSO React journey failed; sanitized output: " + string(result))
	}
	var bindings, liveSources, liveLocals, disabled int
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sso_bindings WHERE subject IN ('1','2') AND active`).Scan(&bindings); e != nil || bindings != 2 {
		t.Fatal("real browser retained bindings", e)
	}
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE sso_connector_id IS NOT NULL AND revoked_at IS NULL`).Scan(&liveSources); e != nil || liveSources != 0 {
		t.Fatal("connector disable left sourced authority", e)
	}
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE sso_connector_id IS NULL AND revoked_at IS NULL`).Scan(&liveLocals); e != nil || liveLocals < 1 {
		t.Fatal("local emergency authority was revoked", e)
	}
	if e = accounts.DB.QueryRow(ctx, `SELECT count(*) FROM sso_connectors WHERE NOT enabled AND revision=3 AND auth_version=3`).Scan(&disabled); e != nil || disabled != 1 {
		t.Fatal("real connector edit was not persisted", e)
	}
	t.Log("Actual production React Admin and Portal, real local TLS/Go/PostgreSQL and pinned Gitea browser consent establish explicit role-inverted bindings, ordinary SSO sessions, Portal scope denial, password-confirmed unlink/local recovery/relink and connector-wide revocation. No provider/identity/session test double or runtime public service is used.")
}
