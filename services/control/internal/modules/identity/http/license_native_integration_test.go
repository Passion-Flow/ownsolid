//go:build license_native_integration

package http_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

const nativeLicenseMaximumOutput = 16 << 10

type nativeLicenseOutput struct{ bytes.Buffer }

func (b *nativeLicenseOutput) Write(raw []byte) (int, error) {
	if len(raw) > nativeLicenseMaximumOutput-b.Len() {
		return 0, errors.New("NATIVE_LICENSE_OUTPUT_LIMIT")
	}
	return b.Buffer.Write(raw)
}

type nativeLicenseProcess struct {
	t                                 *testing.T
	ctx                               context.Context
	binary, path, root, admin, portal string
	configuration                     config.Config
	client                            *http.Client
	command                           *exec.Cmd
	exited                            chan error
	log                               *os.File
	pids                              []int
}

func nativeLicense(t *testing.T, ctx context.Context, owner *identity.Service, pool *pgxpool.Pool, label string) *nativeLicenseProcess {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if binary == "" {
		t.Fatal("explicit current native control required")
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private native root")
	}
	write := func(name string, value []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if e := os.WriteFile(path, value, 0600); e != nil {
			t.Fatal("private native input")
		}
		return path
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(address)
	_, caFile, certificate, key := smtpsink.Certificate(t, root)
	ca, e := os.ReadFile(caFile)
	roots := x509.NewCertPool()
	if e != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("private native CA")
	}
	selected := fixture.OwnedPostgres(t, pool)
	dsn, e := url.Parse(os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil {
		t.Fatal("owned DSN")
	}
	dsn.Path = "/" + pool.Config().ConnConfig.Database
	query := dsn.Query()
	query.Set("search_path", selected.Schema)
	dsn.RawQuery = query.Encode()
	c := owner.Config
	c.Server.ListenAddress = address
	c.Server.PortalOrigin = "https://127.0.0.1:" + port
	c.Server.AdminOrigin = "https://localhost:" + port
	c.Server.TLSCertificateFile = certificate
	c.Server.TLSPrivateKeyFile = key
	c.Server.PortalAssets = os.Getenv("PROJECT_ONE_LICENSE_PORTAL_ASSETS")
	c.Server.AdminAssets = os.Getenv("PROJECT_ONE_LICENSE_ADMIN_ASSETS")
	c.Database.DSNFile = write("operator-dsn", []byte(dsn.String()))
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.Security.AuthHMACKeyFile = write("auth-hmac", owner.HMACKey)
	keys, e := json.Marshal(owner.Mail.Keys)
	if e != nil {
		t.Fatal(e)
	}
	c.Security.MailKeyringFile = write("mail-keyring.json", keys)
	c.Security.PasswordBlocklistFile = write("blocklist", []byte("passwordpassword\n"))
	trust, e := os.ReadFile(owner.Config.Licensing.TrustFile)
	if e != nil {
		t.Fatal("protected public trust")
	}
	c.Licensing = &config.LicensingConfiguration{TrustFile: write("license-trust.json", trust)}
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.Port = 1
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 1
	c.SMTP.OperationTimeoutSeconds = 2
	policy := c.Policy()
	policy.HTTP.RequestSeconds = 47
	c.Runtime = &policy
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("explicit evidence root")
	}
	log, e := os.OpenFile(filepath.Join(output, label+"-control.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	jar, _ := cookiejar.New(nil)
	p := &nativeLicenseProcess{t: t, ctx: ctx, binary: binary, root: root, path: filepath.Join(root, "control.yaml"), admin: c.Server.AdminOrigin, portal: c.Server.PortalOrigin, configuration: c, client: &http.Client{Transport: transport, Jar: jar, Timeout: config.Seconds(policy.HTTP.RequestSeconds)}, log: log}
	p.save()
	t.Cleanup(func() { p.stop(); p.client.CloseIdleConnections(); p.log.Close() })
	return p
}
func (p *nativeLicenseProcess) save() {
	p.t.Helper()
	raw, e := yaml.Marshal(p.configuration)
	if e != nil || os.WriteFile(p.path, raw, 0600) != nil {
		p.t.Fatal("native configuration serialization")
	}
	if _, e = config.Load(p.path); e != nil {
		p.t.Fatal("native configuration rejected", e)
	}
}
func (p *nativeLicenseProcess) start() {
	p.t.Helper()
	if p.command != nil {
		p.t.Fatal("native already running")
	}
	command := exec.Command(p.binary, "serve", "--config", p.path)
	command.Stdout = p.log
	command.Stderr = p.log
	if e := command.Start(); e != nil {
		p.t.Fatal("native start", e)
	}
	p.command = command
	p.pids = append(p.pids, command.Process.Pid)
	p.exited = make(chan error, 1)
	go func() { p.exited <- command.Wait() }()
	deadline := time.Now().Add(config.Seconds(p.configuration.Policy().HTTP.StartupSeconds))
	for time.Now().Before(deadline) {
		response, e := p.client.Get(p.portal + "/health/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return
			}
		}
		select {
		case <-p.exited:
			p.command = nil
			p.t.Fatal("native exited before readiness; inspect protected public log")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatal("native readiness deadline")
}
func (p *nativeLicenseProcess) stop() {
	p.t.Helper()
	if p.command == nil {
		return
	}
	_ = p.command.Process.Signal(syscall.SIGTERM)
	select {
	case e := <-p.exited:
		if e != nil {
			p.t.Error("native graceful exit failed", e)
		}
	case <-time.After(config.Seconds(p.configuration.Policy().HTTP.ShutdownSeconds) + 5*time.Second):
		_ = p.command.Process.Kill()
		<-p.exited
		p.t.Error("native shutdown timeout")
	}
	p.command = nil
}
func (p *nativeLicenseProcess) operator(want string, args ...string) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, p.binary, append(args, "--config", p.path)...)
	command.WaitDelay = 5 * time.Second
	var output nativeLicenseOutput
	command.Stdout = &output
	command.Stderr = &output
	e := command.Run()
	if want == "" {
		if e != nil {
			p.t.Fatal("native recovery command failed; raw diagnostic withheld")
		}
	} else if e == nil || strings.TrimSpace(output.String()) != want {
		p.t.Fatal("native exact refusal missing", want)
	}
}
func (p *nativeLicenseProcess) call(origin, method, path string, input any, status int) []byte {
	p.t.Helper()
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	headers := http.Header{}
	if method == "POST" && path == "/api/v1/admin/invitations" {
		headers.Set("Idempotency-Key", uuid.NewString())
	}
	if method != "GET" {
		var csrf struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(p.call(origin, "GET", "/api/v1/auth/csrf", nil, 200), &csrf) != nil || csrf.Token == "" {
			p.t.Fatal("native CSRF")
		}
		headers.Set("X-CSRF-Token", csrf.Token)
		headers.Set("Origin", origin)
	}
	if input != nil {
		headers.Set("Content-Type", "application/json")
	}
	request, e := http.NewRequestWithContext(p.ctx, method, origin+path, bytes.NewReader(body))
	if e != nil {
		p.t.Fatal("native request construction")
	}
	request.Header = headers
	response, e := p.client.Do(request)
	if e != nil {
		p.t.Fatal("native request failed", method, path)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil || response.StatusCode != status {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &problem)
		p.t.Fatal("native status mismatch", method, path, status, response.StatusCode, problem.Code)
	}
	if !strings.HasPrefix(path, "/api/") && path != "/runtime-config.json" {
		return raw
	}
	if status >= 400 || status == 204 {
		return raw
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		p.t.Fatal("native response envelope")
	}
	return envelope.Data
}
func (p *nativeLicenseProcess) login(origin, password string) {
	p.t.Helper()
	p.call(origin, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200)
}
func (p *nativeLicenseProcess) state() licensing.State {
	p.t.Helper()
	var out licensing.State
	if json.Unmarshal(p.call(p.admin, "GET", "/api/v1/admin/license", nil, 200), &out) != nil {
		p.t.Fatal("native state")
	}
	return out
}

func TestM19ActualNativeLicenseStartupRestartAndIsolatedLogicalRestore(t *testing.T) {
	owner := fixture.NewUnlicensed(t)
	password := fixture.Bootstrap(t, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	selected := fixture.OwnedPostgres(t, owner.DB)
	source := nativeLicense(t, ctx, owner, owner.DB, "license-source")
	source.configuration.Licensing = &config.LicensingConfiguration{}
	source.save()
	source.operator("CONFIG_LICENSING_TRUST_REQUIRED", "serve")
	source.configuration.Licensing = &config.LicensingConfiguration{TrustFile: filepath.Join(source.root, "license-trust.json")}
	source.save()
	source.start()
	source.login(source.admin, password)
	source.login(source.portal, password)
	missing := source.state()
	if missing.State != licensing.Missing || missing.AcceptedLicenses != 0 {
		t.Fatal("native missing license recovery")
	}
	for _, origin := range []string{source.admin, source.portal} {
		if !bytes.Contains(source.call(origin, "GET", "/", nil, 200), []byte("<html")) {
			t.Fatal("native entrance asset")
		}
		var public map[string]any
		raw := source.call(origin, "GET", "/runtime-config.json", nil, 200)
		if json.Unmarshal(raw, &public) != nil || public["request_timeout_milliseconds"] != float64(47000) {
			t.Fatal("native public runtime configuration")
		}
	}
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	claims := licenseClaims(t, owner, base)
	claims.ExpiresAt = base.Add(time.Hour).Format(time.RFC3339)
	claims.GraceUntil = base.Add(2 * time.Hour).Format(time.RFC3339)
	input := licenseInput(t, owner, claims, "")
	source.call(source.admin, "POST", "/api/v1/admin/license", input, 200)
	active := source.state()
	if active.State != licensing.Active || active.AcceptedLicenses != 1 || active.HistoryHeadLicenseID != claims.LicenseID || active.DeploymentID != missing.DeploymentID {
		t.Fatal("native real signed import")
	}
	// Exercise the real audited raw download, not the API envelope decoder.
	diagnostic, e := source.client.Get(source.admin + "/api/v1/admin/license/diagnostics")
	if e != nil {
		t.Fatal("native diagnostics")
	}
	diagnosticBytes, e := io.ReadAll(io.LimitReader(diagnostic.Body, 16385))
	diagnostic.Body.Close()
	diagnosticHash := sha256.Sum256(diagnosticBytes)
	if e != nil || diagnostic.StatusCode != 200 || len(diagnosticBytes) > 16384 || diagnostic.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(diagnosticHash[:]) {
		t.Fatal("native diagnostic checksum/bounds")
	}
	var profile identity.Me
	if json.Unmarshal(source.call(source.portal, "GET", "/api/v1/me", nil, 200), &profile) != nil {
		t.Fatal("native profile version")
	}
	source.call(source.portal, "PATCH", "/api/v1/me", map[string]any{"display_name": "Native license recovery profile", "locale": "en", "version": profile.User.Version}, 200)
	// This real human new-business mutation must pass after importing the license.
	source.call(source.admin, "POST", "/api/v1/admin/invitations", map[string]any{"email": "native-license@example.invalid", "locale": "en"}, 201)
	source.stop()
	source.start()
	source.login(source.admin, password)
	restarted := source.state()
	if restarted.DeploymentID != active.DeploymentID || restarted.HistoryHeadLicenseID != active.HistoryHeadLicenseID || restarted.ObservedAt.Before(active.ObservedAt) {
		t.Fatal("native restart lost license identity/history/time")
	}
	source.stop()
	archive := selected.DumpSchema(t, ctx)
	archiveDigest := sha256.Sum256(archive)
	restored := selected.RestoreFresh(t, ctx, owner.DB, archive)
	t.Logf("license_native_owned_restore_database=%s", restored.Config().ConnConfig.Database)
	target := nativeLicense(t, ctx, owner, restored, "license-restored")
	target.operator("", "invalidate-restored-auth", "--reason", "Synthetic isolated license restoration")
	// Reuse only an old cookie jar as an adversarial restore input. A fresh login
	// must replace it after the real server-side restored authority invalidation.
	target.client.Jar = source.client.Jar
	oldOrigin, _ := url.Parse(target.admin)
	hasSession := false
	for _, cookie := range target.client.Jar.Cookies(oldOrigin) {
		if cookie.Name == "__Host-admin_session" && cookie.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		t.Fatal("old restore session fixture missing")
	}

	target.start()
	target.call(target.admin, "GET", "/api/v1/me", nil, 401)
	target.login(target.admin, password)
	target.login(target.portal, password)
	after := target.state()
	if after.DeploymentID != restarted.DeploymentID || after.HistoryHeadLicenseID != restarted.HistoryHeadLicenseID || after.AcceptedLicenses != 1 || after.ObservedAt.Before(restarted.ObservedAt) {
		t.Fatal("logical restore lost license history/highwater")
	}
	var me struct {
		User struct {
			DisplayName string `json:"display_name"`
		} `json:"user"`
	}
	if json.Unmarshal(target.call(target.portal, "GET", "/api/v1/me", nil, 200), &me) != nil || me.User.DisplayName != "Native license recovery profile" {
		t.Fatal("logical restore lost profile persistence")
	}
	target.stop()
	var digest string
	if e := restored.QueryRow(ctx, "SELECT artifact_sha256 FROM platform_license_artifacts WHERE license_id=$1", claims.LicenseID).Scan(&digest); e != nil {
		t.Fatal(e)
	}
	if _, e := restored.Exec(ctx, "UPDATE platform_license_artifacts SET artifact_sha256=repeat('0',64)"); e != nil {
		t.Fatal(e)
	}
	target.operator("LICENSE_STORED_STATE_INVALID", "serve")
	if _, e := restored.Exec(ctx, "UPDATE platform_license_artifacts SET artifact_sha256=$1 WHERE license_id=$2", digest, claims.LicenseID); e != nil {
		t.Fatal(e)
	}
	if e := database.Check(ctx, restored); e != nil {
		t.Fatal("native read-only restored schema check", e)
	}
	target.start()
	target.login(target.admin, password)
	final := target.state()
	if final.State != licensing.Active || final.HistoryHeadLicenseID != claims.LicenseID {
		t.Fatal("native explicit synthetic corruption recovery")
	}
	target.stop()
	proof := map[string]any{"status": "passed_actual_native_license_startup_restart_and_fresh_logical_restore", "actual_host_clock": true, "native_main_process": true, "missing_trust_refused": true, "valid_trust_missing_artifact_served": true, "actual_HTTP_signed_import": true, "source_restart": true, "logical_archive_sha256": hex.EncodeToString(archiveDigest[:]), "logical_archive_bytes": len(archive), "fresh_generated_database": true, "actual_restore_auth_invalidation": true, "license_history_and_highwater_preserved": true, "profile_preserved": true, "corrupt_history_refused_before_serving": true, "schema_readonly_verified": true, "source_processes": source.pids, "restored_processes": target.pids, "all_joined": true, "profile": "empty-model local TLS current native control with retained production frontend assets; no restored Chrome/nonempty-file/image/offline/target qualification"}
	raw, _ := json.MarshalIndent(proof, "", "  ")
	if e := os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "native-license-recovery.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
