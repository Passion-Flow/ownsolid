package browser_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
	"time"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20SSOBindingRecoveryProductionBrowser(t *testing.T) {
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
	output := filepath.Join(evidence, "sso-binding-recovery-browser")
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
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, subject := range []string{"9007199254740993", "9007199254740995", "9007199254740997"} {
		if _, e := accounts.DB.Exec(ctx, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,$4,$5)`, ids[i], tenant, connector.ID, subject, actor.Me.User.ID); e != nil {
			t.Fatal("synthetic owned binding prerequisite", e)
		}
	}
	start, e := service.Start(ctx, connector.ID, "portal", "", nil, sso.StartInput{Mode: sso.ModeLogin}, uuid.NewString())
	if e != nil {
		t.Fatal("actual encrypted flow prerequisite", e)
	}
	authorization, e := url.Parse(start.AuthorizationURL)
	if e != nil {
		t.Fatal(e)
	}
	var flow string
	if e := accounts.DB.QueryRow(ctx, `SELECT id::text FROM sso_flows WHERE connector_id=$1 AND state_hash=$2`, connector.ID, security.Digest(authorization.Query().Get("state"))).Scan(&flow); e != nil {
		t.Fatal(e)
	}
	// Privileged synthetic claim states qualify source revocation only. No
	// provider response,token exchange or external network is simulated as success.
	if _, e := accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2`, uuid.NewString(), flow); e != nil {
		t.Fatal(e)
	}
	if _, e := accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE id=$2`, actor.Me.User.ID, flow); e != nil {
		t.Fatal(e)
	}
	source := identity.SessionSource{TenantID: tenant, ConnectorID: connector.ID, BindingID: ids[2], FlowID: flow, AuthVersion: 1}
	store, e := accounts.Repository()
	if e != nil {
		t.Fatal(e)
	}
	var sourced identity.IssuedSession
	if e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		sourced, e = accounts.IssueSessionWithin(ctx, q, actor.Me.User.ID, actor.Epoch, "portal", "", uuid.NewString(), &source)
		return e
	}); e != nil {
		t.Fatal("actual sourced portal issuance", e)
	}
	value := map[string]any{"portal": accounts.Config.Server.PortalOrigin, "admin": accounts.Config.Server.AdminOrigin, "email": "admin@example.invalid", "password": password, "source_token": sourced.Token, "binding_ids": ids, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output}
	raw, _ := json.Marshal(value)
	path := filepath.Join(private, "browser.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal("private browser input", e)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "tests/acceptance/sso/binding-recovery-flow.mjs"), path)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if e := command.Run(); e != nil {
		t.Fatal("SSO_BINDING_RECOVERY_BROWSER_FAILED")
	}
	var result struct {
		Status string   `json:"status"`
		Steps  []string `json:"steps"`
	}
	raw, e = os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || len(result.Steps) != 3 {
		t.Fatal("complete binding browser result required")
	}
	var bindings, intents, audits int
	if e := accounts.DB.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=$1 AND user_id=$2 AND NOT active AND revision=2),(SELECT COUNT(*) FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND operation='unlink_binding'),(SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND actor_user_id=$2 AND action='sso.binding_unlinked' AND outcome='success')`, tenant, actor.Me.User.ID).Scan(&bindings, &intents, &audits); e != nil || bindings != 3 || intents != 3 || audits != 3 {
		t.Fatal("independent actual binding/intent/audit exactly-once readback", e)
	}
	if _, e := accounts.Authenticate(ctx, sourced.Token, "portal"); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("actual sourced browser session must remain revoked", e)
	}
	if _, e := accounts.Authenticate(ctx, token, "admin"); e != nil {
		t.Fatal("ordinary local Admin must survive", e)
	}
}
