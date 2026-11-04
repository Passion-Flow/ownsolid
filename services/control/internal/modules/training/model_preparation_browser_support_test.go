//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

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

	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

// A synthetic-scope test harness only. It starts a bounded loopback server and browser,
// retains private fixture credentials only in its temporary0600 file, and joins both.
func runPreparationBrowser(t *testing.T, g preparationScenario, script, evidence string, extra map[string]any, afterConfiguration ...func() func()) json.RawMessage {
	t.Helper()
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" || script != "preparation-flow.mjs" && script != "approval-flow.mjs" {
		t.Fatal("explicit bounded preparation browser profile required")
	}
	identity := g.f.s.Identity
	repo, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	for _, app := range []string{"console", "portal"} {
		if _, e = os.Stat(filepath.Join(repo, "web/apps", app, "dist/index.html")); e != nil {
			t.Fatal("production UI build required")
		}
	}
	dir := t.TempDir()
	certificate, ca, _, _ := smtpsink.Certificate(t, dir)
	server := httptest.NewUnstartedServer(preparationHTTPHandler(g))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	original := identity.Config.Server
	defer func() { identity.Config.Server = original }()
	identity.Config.Server.AdminOrigin = "https://127.0.0.1:" + port
	identity.Config.Server.PortalOrigin = "https://localhost:" + port
	identity.Config.Server.AdminAssets = filepath.Join(repo, "web/apps/console/dist")
	identity.Config.Server.PortalAssets = filepath.Join(repo, "web/apps/portal/dist")
	server.StartTLS()
	defer server.Close()
	for _, start := range afterConfiguration {
		stop := start()
		defer stop()
	}
	leaf, e := x509.ParseCertificate(certificate.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	output := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), evidence+"-"+os.Getenv("PROJECT_ONE_BROWSER_LOCALE"))
	if e = os.MkdirAll(output, 0700); e != nil {
		t.Fatal(e)
	}
	manifest := map[string]any{"browser_locale": os.Getenv("PROJECT_ONE_BROWSER_LOCALE"), "admin": identity.Config.Server.AdminOrigin, "portal": identity.Config.Server.PortalOrigin, "admin_email": "admin@example.invalid", "admin_password": g.f.password, "workspace_id": g.input.WorkspaceID, "training_job_id": g.input.TrainingJobID, "heldout_id": g.input.HeldoutID, "node_id": g.node.NodeID, "gpu_uuid": g.input.GPUUUID, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output}
	for key, value := range extra {
		if _, exists := manifest[key]; exists {
			t.Fatal("browser fixture field collision")
		}
		manifest[key] = value
	}
	raw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "browser-fixture.json")
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/model-lifecycle", script), file)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if e = command.Run(); e != nil {
		t.Fatal("MODEL_PREPARATION_BROWSER_FAILED")
	}
	result, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	return result
}
