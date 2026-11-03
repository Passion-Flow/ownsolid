package browser_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20SSOConnectorRecoveryProductionBrowser(t *testing.T) {
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
	output := filepath.Join(evidence, "sso-connector-recovery-browser")
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
	value := map[string]any{"portal": accounts.Config.Server.PortalOrigin, "admin": accounts.Config.Server.AdminOrigin, "email": "admin@example.invalid", "password": password, "provider": provider, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output}
	raw, _ := json.Marshal(value)
	path := filepath.Join(private, "browser.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal("private browser input", e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "tests/acceptance/sso/connector-recovery-flow.mjs"), path)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if e := command.Run(); e != nil {
		t.Fatal("SSO_CONNECTOR_RECOVERY_BROWSER_FAILED")
	}
	var result struct {
		Status string            `json:"status"`
		IDs    map[string]string `json:"connector_ids"`
	}
	raw, e = os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || len(result.IDs) != 3 {
		t.Fatal("complete browser result required")
	}
	var tenant string
	if e := accounts.DB.QueryRow(ctx, `SELECT tenant_id::text FROM installation_state WHERE id=1`).Scan(&tenant); e != nil {
		t.Fatal("independent actual browser tenant")
	}
	var connectors, intents, audits int
	if e := accounts.DB.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM sso_connectors WHERE tenant_id=$1),(SELECT COUNT(*) FROM sso_mutation_intents WHERE tenant_id=$1),(SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action IN('sso.connector_created','sso.connector_updated') AND outcome='success')`, tenant).Scan(&connectors, &intents, &audits); e != nil || connectors != 3 || intents != 5 || audits != 5 {
		t.Fatal("independent real SQL create/update/intent/audit exactly-once inventory", e)
	}
	for name, id := range result.IDs {
		var revision, authority int64
		var enabled bool
		if e := accounts.DB.QueryRow(ctx, `SELECT revision,auth_version,enabled FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&revision, &authority, &enabled); e != nil {
			t.Fatal("independent actual connector readback", e)
		}
		want := int64(1)
		if name == "recovered" {
			want = 3
		}
		if revision != want || authority != want || enabled != (name != "recovered") {
			t.Fatal("browser recovery duplicated an accepted connector revision")
		}
	}
}
