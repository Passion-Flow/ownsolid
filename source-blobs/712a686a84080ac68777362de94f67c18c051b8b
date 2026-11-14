package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Exercise the production dispatcher with real isolated PostgreSQL and private
// files. The identity fixture owns its random schema and cleanup.
func TestActualProtectedBootstrapCLIWithoutUnrelatedAuthenticationAssets(t *testing.T) {
	s := identityfixture.New(t)
	ctx := context.Background()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	cc := s.DB.Config().ConnConfig
	q := url.Values{"sslmode": {"disable"}}
	for k, v := range cc.RuntimeParams {
		q.Set(k, v)
	}
	uri := url.URL{Scheme: "postgres", User: url.UserPassword(cc.User, cc.Password), Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database, RawQuery: q.Encode()}
	c := s.Config
	c.Server.ListenAddress = "127.0.0.1:18443"
	c.Database.DSNFile = write("operator-dsn", []byte(uri.String()))
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	c.Security.PasswordBlocklistFile = write("blocklist", []byte("passwordpassword\n"))
	c.Security.AuthHMACKeyFile = filepath.Join(root, "missing-hmac")
	c.Security.MailKeyringFile = filepath.Join(root, "missing-mail")
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.StartupSeconds = 1
	c.Runtime = &policy
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	configuration := write("operator.yaml", raw)
	request := write("bootstrap-request.json", []byte(`{"format_version":1,"administrator_email":"owner@example.invalid","display_name":"Owner","installation_name":"Installation","workspace_name":"Workspace"}`))
	output := filepath.Join(root, "credential.json")
	args := os.Args
	defer func() { os.Args = args }()
	call := func(command string) error {
		os.Args = []string{"control", command, "--config", configuration, "--bootstrap-input", request, "--credential-output", output}
		return run()
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "SELECT id FROM installation_state WHERE id=1 FOR UPDATE"); e != nil {
		t.Fatal(e)
	}
	if e = call("bootstrap-admin-file"); e == nil {
		t.Fatal("blocked bootstrap incorrectly succeeded")
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = call("reconcile-bootstrap-file"); e == nil || e.Error() != bootstrap.NotCommitted {
		t.Fatal("uncommitted CLI success", e)
	}
	beforeResume, e := os.ReadFile(output)
	if e != nil {
		t.Fatal("intent not durable", e)
	}
	if e = call("resume-bootstrap-file"); e != nil {
		t.Fatal("production bootstrap resume", e)
	}
	afterResume, _ := os.ReadFile(output)
	if !bytes.Equal(beforeResume, afterResume) {
		t.Fatal("resume replaced credential")
	}

	intent, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var decoded bootstrap.Intent
	if json.Unmarshal(intent, &decoded) != nil || decoded.Password == "" {
		t.Fatal("protected intent missing")
	}
	if e = call("reconcile-bootstrap-file"); e != nil {
		t.Fatal(e)
	}
	if e = call("resume-bootstrap-file"); e != nil {
		t.Fatal(e)
	}
	if e = call("bootstrap-admin-file"); e == nil {
		t.Fatal("bootstrap replay accepted")
	}
	var users, tenants, workspaces, audits, sessions, outbox int
	if e = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),(SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'),(SELECT count(*) FROM sessions),(SELECT count(*) FROM mail_deliveries)`).Scan(&users, &tenants, &workspaces, &audits, &sessions, &outbox); e != nil || users != 1 || tenants != 1 || workspaces != 1 || audits != 1 || sessions != 0 || outbox != 0 {
		t.Fatal("bootstrap persistence", users, tenants, workspaces, audits, sessions, outbox, e)
	}
	after, e := os.ReadFile(output)
	if e != nil || !bytes.Equal(intent, after) {
		t.Fatal("credential changed", e)
	}
	var phc string
	if e = s.DB.QueryRow(ctx, "SELECT password_phc FROM password_credentials").Scan(&phc); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.Passwords.Verify(ctx, phc, decoded.Password); e != nil || !ok {
		t.Fatal("real credential does not authenticate", e)
	}
}
