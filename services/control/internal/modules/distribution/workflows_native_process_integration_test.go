//go:build knowledge_gateway_integration

package distribution_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

type nativeWorkflowProcess struct {
	testing                      *testing.T
	ctx                          context.Context
	binary, path, origin, caFile string
	client                       *http.Client
	configuration                config.Config
	command                      *exec.Cmd
	exited                       chan error
	log                          *os.File
}

func nativeWorkflow(t *testing.T, ctx context.Context, s *identity.Service, databaseName, label string) *nativeWorkflowProcess {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	root := t.TempDir()
	if binary == "" || os.Chmod(root, 0700) != nil {
		t.Fatal("explicit current native control binary/private directory required")
	}
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("private native configuration")
		}
		return path
	}
	address, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	listen := address.Addr().String()
	address.Close()
	_, port, _ := net.SplitHostPort(listen)
	_, caFile, certificate, key := smtpsink.Certificate(t, root)
	roots := x509.NewCertPool()
	ca, e := os.ReadFile(caFile)
	if e != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("private native TLS")
	}
	// Use the already parsed, attested fixture connection. No DSN is logged or
	// supplied in argv; its protected copy remains inside this exact private root.
	base := s.DB.Config().ConnConfig
	dsn := &url.URL{Scheme: "postgresql", User: url.UserPassword(base.User, base.Password), Host: net.JoinHostPort(base.Host, strconv.Itoa(int(base.Port))), Path: "/" + databaseName}
	values := url.Values{}
	if base.TLSConfig == nil {
		values.Set("sslmode", "disable")
	} else {
		values.Set("sslmode", "verify-full")
		values.Set("sslrootcert", os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_CERTIFICATE_FILE"))
	}
	// The native witness owns the real CA rather than the separate HTTP TLS proxy.
	if file := os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT"); file != "" {
		raw, e := os.ReadFile(file)
		var input struct {
			CAFile string `json:"ca_file"`
		}
		if e != nil || json.Unmarshal(raw, &input) != nil || input.CAFile == "" {
			t.Fatal("attested native CA required")
		}
		values.Set("sslrootcert", input.CAFile)
	}
	dsn.RawQuery = values.Encode()
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatal("exact isolated native database")
	}
	dsn.Path = "/" + databaseName
	query := dsn.Query()
	query.Set("search_path", s.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	c := s.Config
	c.Server.ListenAddress = listen
	c.Server.PortalOrigin = "https://127.0.0.1:" + port
	c.Server.AdminOrigin = "https://localhost:" + port
	c.Server.TLSCertificateFile = certificate
	c.Server.TLSPrivateKeyFile = key
	c.Server.PortalAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "web/apps/portal/dist")
	c.Server.AdminAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "web/apps/console/dist")
	c.Database.DSNFile = write("operator-dsn", []byte(dsn.String()))
	c.Database.MaxOpenConnections = 10
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.Security.AuthHMACKeyFile = write("auth-hmac", s.HMACKey)
	keys, _ := json.Marshal(s.Mail.Keys)
	c.Security.MailKeyringFile = write("mail-keyring.json", keys)
	c.Security.PasswordBlocklistFile = write("blocklist", []byte("passwordpassword\n"))
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.Port = 1
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 1
	c.SMTP.OperationTimeoutSeconds = 2
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	configurationFile := write("control.yaml", raw)
	if _, e = config.Load(configurationFile); e != nil {
		t.Fatal("actual native configuration", e)
	}
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	process := &nativeWorkflowProcess{testing: t, ctx: ctx, binary: binary, path: configurationFile, origin: c.Server.PortalOrigin, caFile: caFile, configuration: c, client: &http.Client{Transport: transport, Jar: jar, Timeout: config.Seconds(c.Policy().HTTP.RequestSeconds)}}
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("native recovery evidence root required")
	}
	process.log, e = os.OpenFile(filepath.Join(output, label+"-control.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { process.stop(); process.client.CloseIdleConnections(); process.log.Close() })
	return process
}
func (p *nativeWorkflowProcess) start() {
	p.testing.Helper()
	// The test cleanup owns graceful shutdown even when its assertion context has
	// already been cancelled. The outer profile owns the entire process group.
	p.command = exec.Command(p.binary, "serve", "--config", p.path)
	p.command.Stdout = p.log
	p.command.Stderr = p.log
	if e := p.command.Start(); e != nil {
		p.testing.Fatal("native control start", e)
	}
	p.exited = make(chan error, 1)
	command := p.command
	go func() { p.exited <- command.Wait() }()
	deadline := time.Now().Add(config.Seconds(p.configuration.Policy().HTTP.StartupSeconds))
	for time.Now().Before(deadline) {
		response, e := p.client.Get(p.origin + "/health/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return
			}
		}
		select {
		case <-p.exited:
			p.command = nil
			p.testing.Fatal("native control exited before readiness")
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	p.testing.Fatal("native workflow readiness deadline")
}
func (p *nativeWorkflowProcess) stop() {
	if p.command == nil {
		return
	}
	_ = p.command.Process.Signal(syscall.SIGTERM)
	select {
	case e := <-p.exited:
		if e != nil {
			p.testing.Error("native control shutdown", e)
		}
	case <-time.After(config.Seconds(p.configuration.Policy().HTTP.ShutdownSeconds + 3)):
		_ = p.command.Process.Kill()
		<-p.exited
		p.testing.Error("native workflow shutdown deadline")
	}
	p.command = nil
	p.client.CloseIdleConnections()
}

func (p *nativeWorkflowProcess) crash() {
	p.testing.Helper()
	if p.command == nil || p.command.Process.Kill() != nil {
		p.testing.Fatal("exact owned native process crash")
	}
	select {
	case e := <-p.exited:
		if e == nil {
			p.testing.Fatal("crashed native process reported clean shutdown")
		}
	case <-time.After(5 * time.Second):
		p.testing.Fatal("crashed native process did not join")
	}
	p.command = nil
	p.client.CloseIdleConnections()
}
func (p *nativeWorkflowProcess) call(method, path string, input any, key string, status int) json.RawMessage {
	p.testing.Helper()
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	headers := http.Header{}
	if method != "GET" {
		raw := p.call("GET", "/api/v1/auth/csrf", nil, "", 200)
		var csrf struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(raw, &csrf) != nil {
			p.testing.Fatal("native CSRF response")
		}
		headers.Set("X-CSRF-Token", csrf.Token)
		headers.Set("Origin", p.origin)
	}
	if input != nil {
		headers.Set("Content-Type", "application/json")
	}
	if key != "" {
		headers.Set("Idempotency-Key", key)
	}
	request, e := http.NewRequestWithContext(p.ctx, method, p.origin+path, bytes.NewReader(body))
	if e != nil {
		p.testing.Fatal(e)
	}
	request.Header = headers
	response, e := p.client.Do(request)
	if e != nil {
		p.testing.Fatal("native workflow HTTP", e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if e != nil || response.StatusCode != status {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &problem)
		p.testing.Fatalf("native %s expected %d observed %d (%s)", path, status, response.StatusCode, problem.Code)
	}
	if status >= 400 || status == 204 {
		return raw
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		p.testing.Fatal("native response envelope")
	}
	return envelope.Data
}
func (p *nativeWorkflowProcess) operator(wantSuccess bool) {
	p.testing.Helper()
	command := exec.CommandContext(p.ctx, p.binary, "invalidate-restored-auth", "--config", p.path, "--reason", "Synthetic isolated workflow restore acceptance")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	e := command.Run()
	if (e == nil) != wantSuccess {
		p.testing.Fatal("native common restore operator outcome mismatch")
	}
	if wantSuccess && !strings.Contains(output.String(), "RESTORED_AUTH_INVALIDATED") {
		p.testing.Fatal("native restore operator missing success receipt")
	}
}
