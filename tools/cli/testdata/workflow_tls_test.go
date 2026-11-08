package http_test

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type workflowCLI struct {
	t        *testing.T
	service  *identity.Service
	handler  *transport.Handler
	auth     identity.Auth
	password string
	config   string
}

func newWorkflowCLI(t *testing.T, configure func(*identity.Service), wrap func(http.Handler) http.Handler) *workflowCLI {
	t.Helper()
	service := fixture.New(t)
	var endpoint http.Handler
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { endpoint.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	service.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
	if configure != nil {
		configure(service)
	}
	password := fixture.Bootstrap(t, service)
	auth := fixture.Login(t, service, "admin@example.invalid", password, "admin")
	handler := transport.New(service)
	if err := handler.LicenseInitializationError(); err != nil {
		t.Fatal(err)
	}
	if err := handler.Licensing.Service.VerifyStored(t.Context()); err != nil {
		t.Fatal(err)
	}
	endpoint = handler
	if wrap != nil {
		endpoint = wrap(endpoint)
	}
	server.StartTLS()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ca, secret := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "password")
	config, err := json.Marshal(map[string]string{"ca_file": ca})
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string][]byte{
		ca:                                pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
		secret:                            []byte(password),
		filepath.Join(dir, "config.json"): config,
	} {
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", server.URL)
	cli := &workflowCLI{t: t, service: service, handler: handler, auth: auth, password: password, config: dir}
	cli.ok("signed in", "login", "--email", "admin@example.invalid", "--password-file", secret)
	return cli
}

func (c *workflowCLI) run(args ...string) (string, string, error) {
	c.t.Helper()
	binary := os.Getenv("LANE_D_CLI_BINARY")
	if binary == "" {
		c.t.Fatal("set LANE_D_CLI_BINARY to the compiled CLI")
	}
	cmd := exec.CommandContext(c.t.Context(), binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if strings.Contains(stdout.String()+stderr.String(), c.password) {
		c.t.Fatal("CLI printed its password")
	}
	return stdout.String(), stderr.String(), err
}

func (c *workflowCLI) ok(want string, args ...string) string {
	c.t.Helper()
	stdout, stderr, err := c.run(args...)
	if err != nil || !strings.Contains(stdout, want) {
		c.t.Fatalf("CLI %v, want %q: %v %s %s", args, want, err, stdout, stderr)
	}
	return stdout
}

func (c *workflowCLI) refused(want string, args ...string) {
	c.t.Helper()
	_, stderr, err := c.run(args...)
	if err == nil || !strings.Contains(stderr, want) {
		c.t.Fatalf("CLI %v, want refusal %q: %v %s", args, want, err, stderr)
	}
}

func (c *workflowCLI) count(want int, query string, args ...any) {
	c.t.Helper()
	var got int
	if err := c.service.DB.QueryRow(c.t.Context(), query, args...).Scan(&got); err != nil || got != want {
		c.t.Fatalf("fixture count %d, want %d: %v", got, want, err)
	}
}
