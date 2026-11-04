// Package e2e drives the real installer and control plane. run-single-node.sh runs it
// inside a container with no network route, so everything it proves works offline.
package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

const (
	portalOrigin = "https://portal.ownsolid.test:8443"
	adminOrigin  = "https://admin.ownsolid.test:8443"
	adminEmail   = "admin@ownsolid.test"
)

type environment struct {
	installer, composeFile, controlImage, postgresImage, archive, root string
}

func loadEnvironment(t *testing.T) environment {
	t.Helper()
	e := environment{
		installer: os.Getenv("OWNSOLID_E2E_INSTALLER"), composeFile: os.Getenv("OWNSOLID_E2E_COMPOSE_FILE"),
		controlImage: os.Getenv("OWNSOLID_E2E_CONTROL_IMAGE"), postgresImage: os.Getenv("OWNSOLID_E2E_POSTGRES_IMAGE"),
		archive: os.Getenv("OWNSOLID_E2E_IMAGE_ARCHIVE"), root: os.Getenv("OWNSOLID_E2E_ROOT"),
	}
	if e.installer == "" {
		t.Skip("needs tests/e2e/run-single-node.sh, which sets OWNSOLID_E2E_INSTALLER")
	}
	return e
}

func (e environment) configuration(t *testing.T, directory, size, controlImage string) string {
	t.Helper()
	_, trust, err := license.GenerateKey("e2e-issuer")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	trustFile := filepath.Join(directory, "license-trust.json")
	if err = os.WriteFile(trustFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "install-"+strings.NewReplacer(":", "-", "/", "-").Replace(size+controlImage)+".yaml")
	content := fmt.Sprintf(`format_version: 1
topology: {size: %s}
install_root: %s
compose_file: %s
images:
  archive_file: %s
  control: %s
  postgres: %s
origins: {portal: %s, admin: %s}
license: {trust_file: %s}
smtp: {host: smtp.ownsolid.test, from_address: platform@ownsolid.test}
administrator:
  email: %s
  display_name: Installation Administrator
  installation_name: End To End
  workspace_name: Default Workspace
`, size, e.root, e.composeFile, e.archive, controlImage, e.postgresImage, portalOrigin, adminOrigin, trustFile, adminEmail)
	if err = os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

func (e environment) compose(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return run(t, "docker", append([]string{"compose", "--env-file", filepath.Join(e.root, "compose.env"), "-f", filepath.Join(e.root, "compose.yaml")}, args...)...)
}

func (e environment) apply(t *testing.T, path string) {
	t.Helper()
	output, err := run(t, e.installer, "apply", "--config", path)
	if err != nil || !strings.Contains(output, "INSTALL_COMPLETE") {
		t.Fatalf("apply: %v\n%s", err, output)
	}
}

// browser talks to the published port the way a browser does: origin host name, verified
// certificate, cookies and the CSRF token.
type browser struct {
	client *http.Client
	origin string
}

func (e environment) newBrowser(t *testing.T, origin string) *browser {
	t.Helper()
	authority, err := os.ReadFile(filepath.Join(e.root, "config", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority) {
		t.Fatal("generated CA does not parse")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &browser{origin: origin, client: &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:8443")
		},
	}}}
}

func (b *browser) request(t *testing.T, method, path string, body any, csrf string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, b.origin+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", b.origin)
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := b.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}

func (b *browser) login(t *testing.T, password string) int {
	t.Helper()
	status, raw := b.request(t, http.MethodGet, "/api/v1/auth/csrf", nil, "")
	if status != http.StatusOK {
		t.Fatalf("csrf: %d %s", status, raw)
	}
	var csrf struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &csrf); err != nil || csrf.Data.Token == "" {
		t.Fatalf("csrf body: %v", err)
	}
	status, _ = b.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": adminEmail, "password": password}, csrf.Data.Token)
	return status
}

func (e environment) password(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.root, "operator", "admin-credential"))
	if err != nil {
		t.Fatal(err)
	}
	var credential struct {
		Password string `json:"password"`
	}
	if err = json.Unmarshal(raw, &credential); err != nil || credential.Password == "" {
		t.Fatal("administrator credential file does not hold a password")
	}
	return credential.Password
}

func (e environment) assertLogin(t *testing.T, password string) {
	t.Helper()
	b := e.newBrowser(t, adminOrigin)
	if status := b.login(t, password); status != http.StatusOK {
		t.Fatalf("administrator login: status %d", status)
	}
	if status, raw := b.request(t, http.MethodGet, "/api/v1/me", nil, ""); status != http.StatusOK || !strings.Contains(string(raw), adminEmail) {
		t.Fatalf("session read: status %d", status)
	}
	if status, _ := b.request(t, http.MethodGet, "/api/v1/sites/default/cells/default/admin/nodes", nil, ""); status != http.StatusOK {
		t.Fatalf("scoped node list: status %d", status)
	}
	for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/sites/default/cells/other/admin/nodes"} {
		if status, _ := b.request(t, http.MethodGet, path, nil, ""); status != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, status)
		}
	}
}

func TestSingleNodeInstall(t *testing.T) {
	e := loadEnvironment(t)
	directory := t.TempDir()
	config := e.configuration(t, directory, "single-node", e.controlImage)

	t.Run("host has no network route", func(t *testing.T) {
		raw, err := os.ReadFile("/proc/net/route")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if fields := strings.Fields(line); len(fields) > 1 && fields[1] == "00000000" {
				t.Fatalf("a default route exists: %s", line)
			}
		}
	})

	t.Run("configuration is validated", func(t *testing.T) {
		if output, err := run(t, e.installer, "validate", "--config", config); err != nil || !strings.Contains(output, "INSTALL_CONFIG_VALID") {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("unsupported topology changes nothing", func(t *testing.T) {
		output, err := run(t, e.installer, "apply", "--config", e.configuration(t, directory, "ha", e.controlImage))
		if err == nil || !strings.Contains(output, "CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED") {
			t.Fatalf("%v %s", err, output)
		}
		if _, err = os.Stat(e.root); !os.IsNotExist(err) {
			t.Fatal("a refused configuration touched the install root")
		}
	})

	t.Run("missing image fails clearly and can be retried", func(t *testing.T) {
		absent := "ownsolid/control:absent"
		output, err := run(t, e.installer, "apply", "--config", e.configuration(t, directory, "single-node", absent))
		if err == nil || !strings.Contains(output, "INSTALL_IMAGE_MISSING") || !strings.Contains(output, absent) || !strings.Contains(output, "run apply again") {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("apply installs from the archive", func(t *testing.T) { e.apply(t, config) })

	password := e.password(t)

	t.Run("status reports healthy", func(t *testing.T) {
		output, err := run(t, e.installer, "status", "--config", config)
		if err != nil || !strings.Contains(output, portalOrigin+": ready") || !strings.Contains(output, adminOrigin+": ready") {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("administrator signs in and authentication is enforced", func(t *testing.T) {
		e.assertLogin(t, password)
		wrong := e.newBrowser(t, adminOrigin)
		if status := wrong.login(t, password+"-wrong"); status != http.StatusUnauthorized {
			t.Fatalf("wrong password: status %d", status)
		}
		if status, _ := e.newBrowser(t, adminOrigin).request(t, http.MethodGet, "/api/v1/me", nil, ""); status != http.StatusUnauthorized {
			t.Fatalf("anonymous read: status %d", status)
		}
	})

	t.Run("serving holds no privileged database identity", func(t *testing.T) {
		output, err := e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", "import os,sys;sys.exit(os.path.exists('/run/projectone-operator'))")
		if err != nil {
			t.Fatalf("operator directory is visible to serving: %v %s", err, output)
		}
		output, err = e.compose(t, "exec", "--no-TTY", "postgres", "psql", "-X", "-tA", "-U", "ownsolid_migrator", "-d", "ownsolid", "-c", "SELECT rolsuper FROM pg_roles WHERE rolname='ownsolid_runtime'")
		if err != nil || strings.TrimSpace(output) != "f" {
			t.Fatalf("runtime role: %v %s", err, output)
		}
		output, err = e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", "import os;print(os.getuid())")
		if err != nil || strings.TrimSpace(output) == "0" {
			t.Fatalf("control runs as root: %v %s", err, output)
		}
	})

	t.Run("workloads have no route out", func(t *testing.T) {
		// Exits zero only when the connection fails.
		probe := "import socket,sys;s=socket.socket();s.settimeout(3);sys.exit(0 if s.connect_ex(('1.1.1.1',443)) else 1)"
		if output, err := e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", probe); err != nil {
			t.Fatalf("control reached the internet: %v %s", err, output)
		}
	})

	t.Run("state survives a restart", func(t *testing.T) {
		if output, err := e.compose(t, "restart", "control"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		if output, err := e.compose(t, "up", "--detach", "--wait", "--no-build", "control"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		e.assertLogin(t, password)
	})

	t.Run("stopped database is reported and repaired by apply", func(t *testing.T) {
		if output, err := e.compose(t, "stop", "postgres"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		output, err := run(t, e.installer, "status", "--config", config)
		if err == nil || !strings.Contains(output, "INSTALL_NOT_HEALTHY") {
			t.Fatalf("%v %s", err, output)
		}
		e.apply(t, config)
		e.assertLogin(t, password)
	})

	t.Run("reinstalling over existing data keeps it", func(t *testing.T) {
		if output, err := e.compose(t, "down"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		e.apply(t, config)
		if e.password(t) != password {
			t.Fatal("a second apply issued a new administrator credential")
		}
		e.assertLogin(t, password)
		output, err := run(t, e.installer, "status", "--config", config)
		if err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})
}
