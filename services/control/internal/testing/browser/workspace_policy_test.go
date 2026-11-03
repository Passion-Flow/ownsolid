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
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestWorkspacePolicyProductionBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("explicit bounded browser gate required")
	}
	root, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(output) {
		t.Fatal("explicit external evidence required")
	}
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private fixture")
	}
	configuration := filepath.Join(private, "control.yaml")
	raw, e := os.ReadFile(filepath.Join(root, "services/control/config/examples/control/control.example.yaml"))
	if e != nil || os.WriteFile(configuration, raw, 0600) != nil {
		t.Fatal("canonical configuration fixture")
	}
	environment := filepath.Join(private, "policy.env")
	if os.WriteFile(environment, []byte("PROJECT_ONE_WORKSPACES_MAXIMUM_COUNT=7\nPROJECT_ONE_WORKSPACES_MAXIMUM_LIVE_NOTES=13\n"), 0600) != nil {
		t.Fatal("private policy fixture")
	}
	configured, e := config.LoadWithEnvironment(configuration, environment)
	if e != nil {
		t.Fatal(e)
	}
	s := fixture.New(t)
	s.Config.Runtime = configured.Runtime
	password := fixture.Bootstrap(t, s)
	cert, caFile, _, _ := smtpsink.Certificate(t, private)
	server := httptest.NewUnstartedServer(transport.New(s))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	s.Config.Server.PortalOrigin = "https://localhost:" + port
	s.Config.Server.AdminOrigin = "https://127.0.0.1:" + port
	s.Config.Server.PortalAssets = filepath.Join(output, "portal-dist")
	s.Config.Server.AdminAssets = filepath.Join(output, "admin-dist")
	server.StartTLS()
	defer server.Close()
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	value := map[string]any{"portal": s.Config.Server.PortalOrigin, "admin": s.Config.Server.AdminOrigin, "email": "admin@example.invalid", "password": password, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output}
	raw, e = json.Marshal(value)
	fixtureFile := filepath.Join(private, "browser.json")
	if e != nil || os.WriteFile(fixtureFile, raw, 0600) != nil {
		t.Fatal("private browser fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "tests/acceptance/workspaces/policy-flow.mjs"), fixtureFile)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if command.Run() != nil {
		t.Fatal("WORKSPACE_POLICY_BROWSER_FAILED")
	}
}
