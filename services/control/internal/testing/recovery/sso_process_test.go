//go:build sso_recovery_integration

package recovery_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

// The stored provider result is an explicit source-data fixture. This helper
// starts the actual built executable with enabled SSO and protected real assets.
func verifySSONativeProcessRestarts(t *testing.T, ctx context.Context, accounts *identity.Service, token, keyring, directory string) {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("explicit current native control binary required")
	}
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	_, ca, certificate, privateKey := smtpsink.Certificate(t, directory)
	dsn, e := url.Parse(accounts.DB.Config().ConnString())
	if e != nil {
		t.Fatal("private native SSO DSN invalid")
	}
	query := dsn.Query()
	query.Set("search_path", accounts.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	mailKeys, _ := json.Marshal(accounts.Mail.Keys)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(address)
	origin := "https://127.0.0.1:" + port
	repo := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	if !filepath.IsAbs(repo) {
		t.Fatal("explicit source root required")
	}
	configuration := map[string]any{
		"server":   map[string]any{"listen_address": address, "admin_origin": origin, "portal_origin": "https://localhost:" + port, "tls_certificate_file": certificate, "tls_private_key_file": privateKey, "portal_assets": filepath.Join(repo, "apps/web/apps/portal/dist"), "admin_assets": filepath.Join(repo, "apps/web/apps/admin/dist")},
		"database": map[string]any{"dsn_file": write("native-sso-dsn", []byte(dsn.String())), "max_open_connections": 8, "max_idle_connections": 1, "statement_timeout_seconds": 5},
		"security": map[string]any{"auth_hmac_key_file": write("native-auth-hmac", accounts.HMACKey), "mail_keyring_file": write("native-mail-ring.json", mailKeys), "password_blocklist_file": write("native-blocklist", []byte("passwordpassword\n"))},
		"smtp":     map[string]any{"host": "127.0.0.1", "port": 1, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 1, "operation_timeout_seconds": 2},
		"sso":      map[string]any{"enabled": true, "keyring_file": keyring, "providers": []map[string]any{{"origin": "https://provider.recovery.example.invalid", "issuer": "https://provider.recovery.example.invalid", "dial_addresses": []string{"127.0.0.1"}, "ca_file": ""}}},
	}
	if accounts.Config.SSOOnly() {
		// Empty fixture stores are real initialized namespaces. Accepted artifact
		// byte recovery remains the separate cumulative training/four-store gate.
		c := accounts.Config
		var tenant string
		if e := accounts.DB.QueryRow(ctx, "SELECT tenant_id::text FROM platform_installation").Scan(&tenant); e != nil {
			t.Fatal(e)
		}
		configuration["server"].(map[string]any)["application_profile"] = "sso"
		configuration["models"] = map[string]any{"artifact_root": c.Models.ArtifactRoot}
		configuration["datasets"] = map[string]any{"artifact_root": c.Datasets.ArtifactRoot, "python_executable": os.Getenv("PROJECT_ONE_DATA_PYTHON"), "processor_file": filepath.Join(repo, "runtimes/data-processing/process.py")}
		configuration["training"] = map[string]any{"artifact_root": c.Training.ArtifactRoot, "image_reference": c.Training.ImageReference, "image_id": c.Training.ImageID, "worker_source_sha256": c.Training.WorkerSourceSHA256}
		configuration["model_preparation"] = map[string]any{"artifact_root": c.ModelPreparation.ArtifactRoot, "image_reference": c.ModelPreparation.ImageReference, "image_id": c.ModelPreparation.ImageID, "worker_source_sha256": c.ModelPreparation.WorkerSourceSHA256}
		configuration["licensing"] = map[string]any{"trust_file": c.Licensing.TrustFile}
		for _, root := range []string{c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot} {
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal("owned empty cumulative store", e)
			}
		}
		if e := models.InitializeStore(c.Models.ArtifactRoot, os.Geteuid(), os.Getegid()); e != nil {
			t.Fatal(e)
		}
		for _, item := range []struct {
			path       string
			initialize func(string, string, int, int) error
		}{
			{c.Datasets.ArtifactRoot, datasets.InitializeStore}, {c.Training.ArtifactRoot, training.InitializeStore}, {c.ModelPreparation.ArtifactRoot, modelpreparations.InitializeStore},
		} {
			if e := item.initialize(item.path, tenant, os.Geteuid(), os.Getegid()); e != nil {
				t.Fatal(e)
			}
		}
	}
	raw, _ := json.Marshal(configuration)
	configPath := write("native-sso-control.json", raw)
	roots := x509.NewCertPool()
	caBytes, e := os.ReadFile(ca)
	if e != nil || !roots.AppendCertsFromPEM(caBytes) {
		t.Fatal("private native SSO CA invalid")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Proxy: nil}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	var process *exec.Cmd
	stop := func() {
		if process == nil {
			return
		}
		process.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		select {
		case e := <-done:
			if e != nil {
				t.Error("native enabled-SSO process did not stop cleanly")
			}
		case <-time.After(8 * time.Second):
			process.Process.Kill()
			<-done
			t.Error("native enabled-SSO process shutdown timed out")
		}
		process = nil
	}
	defer stop()
	for attempt := 0; attempt < 2; attempt++ {
		process = exec.CommandContext(ctx, binary, "serve", "--config", configPath)
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
		if e = process.Start(); e != nil {
			t.Fatal("native enabled-SSO process start failed")
		}
		deadline := time.Now().Add(8 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			response, e := client.Get(origin + "/health/ready")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		if !ready {
			t.Fatal("native enabled-SSO readiness failed")
		}
		request, _ := http.NewRequestWithContext(ctx, "GET", origin+"/api/v1/me", nil)
		request.AddCookie(&http.Cookie{Name: "__Host-admin_session", Value: token})
		response, e := client.Do(request)
		if e != nil {
			t.Fatal("native source session request failed")
		}
		var envelope struct {
			Data identity.Me `json:"data"`
		}
		decodeError := json.NewDecoder(response.Body).Decode(&envelope)
		response.Body.Close()
		if response.StatusCode != 200 || decodeError != nil || envelope.Data.User.ID == "" || !envelope.Data.User.IsPlatformAdmin || envelope.Data.Audience != "admin" {
			t.Fatal("native restart did not retain exact ordinary SSO authority")
		}
		stop()
	}
}
