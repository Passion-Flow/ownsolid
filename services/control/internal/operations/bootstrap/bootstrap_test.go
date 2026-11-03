package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func request() Request {
	return Request{1, "admin@example.invalid", "Test Admin", "Test installation", "Default"}
}
func paths(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	input, output := filepath.Join(dir, "request.json"), filepath.Join(dir, "credential.json")
	b, _ := json.Marshal(request())
	if e := os.WriteFile(input, b, 0600); e != nil {
		t.Fatal(e)
	}
	return input, output
}
func lease(t *testing.T, s *identity.Service) *pgxpool.Conn {
	t.Helper()
	c, e := s.DB.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var acquired bool
	if e = c.QueryRow(context.Background(), "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&acquired); e != nil || !acquired {
		c.Release()
		t.Fatal("installation lock unavailable", e)
	}
	t.Cleanup(func() {
		c.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
		c.Release()
	})
	return c
}
func retained(t *testing.T, path string) Intent {
	t.Helper()
	var i Intent
	if _, e := readPrivate(path, &i); e != nil {
		t.Fatal(e)
	}
	return i
}
func count(t *testing.T, s *identity.Service, query string) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow(context.Background(), query).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestBootstrapProtectedDocuments(t *testing.T) {
	input, output := paths(t)
	raw, _ := os.ReadFile(input)
	for name, value := range map[string][]byte{
		"unknown":   bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":1,"other":true`), 1),
		"duplicate": bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1),
		"null":      bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":null`), 1),
		"missing":   []byte(`{"format_version":1}`), "trailing": append(append([]byte{}, raw...), []byte(`{}`)...), "oversized": bytes.Repeat([]byte(" "), MaximumDocumentBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			os.WriteFile(input, value, 0600)
			var r Request
			if _, e := readPrivate(input, &r); e == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
	os.WriteFile(input, raw, 0600)
	for _, mode := range []os.FileMode{0400, 0640, 0644} {
		os.Chmod(input, mode)
		var r Request
		if _, e := readPrivate(input, &r); e == nil {
			t.Fatal("unsafe mode accepted", mode)
		}
	}
	os.Chmod(input, 0600)
	link := filepath.Join(filepath.Dir(input), "link.json")
	if e := os.Symlink(input, link); e != nil {
		t.Fatal(e)
	}
	var r Request
	if _, e := readPrivate(link, &r); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Remove(link)
	if e := os.Link(input, link); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(input, &r); e == nil {
		t.Fatal("hardlink accepted")
	}
	os.Remove(link)
	if e := syscall.Mkfifo(link, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(link, &r); e == nil {
		t.Fatal("FIFO accepted")
	}
	os.Remove(link)
	os.Chmod(filepath.Dir(input), 0755)
	if _, e := readPrivate(input, &r); e == nil {
		t.Fatal("public parent accepted")
	}
	os.Chmod(filepath.Dir(input), 0700)
	if _, e := readPrivate("relative.json", &r); e == nil {
		t.Fatal("relative path accepted")
	}
	i := Intent{1, request(), strings.Repeat("a", 64), strings.Repeat("b", 64), base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, passwordBytes))}
	b, _ := json.Marshal(i)
	if e := writeIntent(output, b); e != nil {
		t.Fatal(e)
	}
	if e := writeIntent(output, []byte("replacement")); e == nil {
		t.Fatal("overwrote intent")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(after, b) {
		t.Fatal("intent changed")
	}
	wrong := &fakeInfo{uid: uint32(os.Geteuid() + 1), links: 1}
	if owned(wrong, true) {
		t.Fatal("foreign owner accepted")
	}
}

// fakeInfo is a metadata test double only; real filesystem cases above use os files.
type fakeInfo struct {
	os.FileInfo
	uid   uint32
	links uint16
}

func (i *fakeInfo) Sys() any { return &syscall.Stat_t{Uid: i.uid, Nlink: 1} }

func TestBootstrapRequestNormalization(t *testing.T) {
	r := request()
	r.AdministratorEmail = " ADMIN@example.invalid "
	r.DisplayName = " e\u0301 "
	n, e := normalize(r)
	if e != nil || n.AdministratorEmail != "admin@example.invalid" || n.DisplayName != "\u00e9" {
		t.Fatal("normalization", e)
	}
	for _, modify := range []func(*Request){func(r *Request) { r.FormatVersion = 2 }, func(r *Request) { r.AdministratorEmail = "bad" }, func(r *Request) { r.DisplayName = "" }, func(r *Request) { r.InstallationName = "bad\nname" }, func(r *Request) { r.WorkspaceName = strings.Repeat("x", 101) }} {
		r := request()
		modify(&r)
		if _, e := normalize(r); e == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestActualBootstrapFilePersistenceReconciliationAndConflicts(t *testing.T) {
	s := identityfixture.New(t)
	c := lease(t, s)
	ctx := context.Background()
	input, output := paths(t)
	status, e := Run(ctx, s, c, "bootstrap-admin-file", input, output)
	if e != nil || status != Verified {
		t.Fatal(status, e)
	}
	i := retained(t, output)
	original, _ := os.ReadFile(output)
	for _, table := range []string{"users", "tenants", "workspaces", "workspace_memberships", "password_credentials"} {
		if count(t, s, "SELECT count(*) FROM "+table) != 1 {
			t.Fatal("duplicate or missing", table)
		}
	}
	if count(t, s, "SELECT count(*) FROM sessions") != 0 || count(t, s, "SELECT count(*) FROM mail_deliveries") != 0 || count(t, s, "SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'") != 1 {
		t.Fatal("unexpected bootstrap effects")
	}
	var phc string
	if e = s.DB.QueryRow(ctx, "SELECT password_phc FROM password_credentials").Scan(&phc); e != nil || strings.Contains(phc, i.Password) {
		t.Fatal("password storage invalid")
	}
	for _, command := range []string{"reconcile-bootstrap-file", "resume-bootstrap-file"} {
		status, e = Run(ctx, s, c, command, input, output)
		if e != nil || status != Verified {
			t.Fatal(command, status, e)
		}
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(original, after) || count(t, s, "SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'") != 1 {
		t.Fatal("reconcile mutated state")
	}
	if _, e = Run(ctx, s, c, "bootstrap-admin-file", input, output); e == nil {
		t.Fatal("repeated bootstrap accepted")
	}
	// Actual generated credential authenticates in both entrances through existing service behavior.
	for _, audience := range []string{"portal", "admin"} {
		identityfixture.Login(t, s, i.Request.AdministratorEmail, i.Password, audience)
	}
	s.Config.Server.AdminOrigin = "https://changed.example.invalid"
	if _, e = Run(ctx, s, c, "reconcile-bootstrap-file", input, output); e == nil {
		t.Fatal("changed configuration accepted")
	}
	s.Config.Server.AdminOrigin = "https://admin.example.invalid"
	os.WriteFile(input, append(mustRead(t, input), '\n'), 0600)
	if _, e = Run(ctx, s, c, "reconcile-bootstrap-file", input, output); e == nil {
		t.Fatal("changed request bytes accepted")
	}
	raw, _ := json.Marshal(request())
	os.WriteFile(input, raw, 0600)
	for _, sql := range []string{"UPDATE users SET status='disabled'", "UPDATE users SET status='active',display_name='Another admin'", "UPDATE users SET display_name='Test Admin'; UPDATE workspace_memberships SET role='member'"} {
		if _, e = s.DB.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
		if _, e = Run(ctx, s, c, "reconcile-bootstrap-file", input, output); e == nil {
			t.Fatal("conflicting result accepted")
		}
	}
	s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='owner'")
	other, _ := s.Passwords.Hash(ctx, identityfixture.Password())
	s.DB.Exec(ctx, "UPDATE password_credentials SET password_phc=$1", other)
	if _, e = Run(ctx, s, c, "reconcile-bootstrap-file", input, output); e == nil {
		t.Fatal("changed password accepted")
	}
}
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestActualBootstrapIntentBeforeMutationResumeAndBinding(t *testing.T) {
	s := identityfixture.New(t)
	c := lease(t, s)
	ctx := context.Background()
	input, output := paths(t)
	binding, e := lockedTarget(ctx, s, c)
	if e != nil {
		t.Fatal(e)
	}
	raw := mustRead(t, input)
	password := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, passwordBytes))
	i := Intent{1, request(), digest(raw), binding, password}
	b, _ := json.Marshal(i)
	if e = writeIntent(output, b); e != nil {
		t.Fatal(e)
	}
	status, e := Run(ctx, s, c, "reconcile-bootstrap-file", input, output)
	if e != nil || status != NotCommitted || count(t, s, "SELECT count(*) FROM users") != 0 {
		t.Fatal(status, e)
	}
	if _, e = Run(ctx, s, c, "bootstrap-admin-file", input, output); e == nil {
		t.Fatal("existing intent accepted")
	}
	if count(t, s, "SELECT count(*) FROM users") != 0 || !bytes.Equal(b, mustRead(t, output)) {
		t.Fatal("existing intent changed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = Run(canceled, s, c, "resume-bootstrap-file", input, output); e == nil {
		t.Fatal("canceled call accepted")
	}
	status, e = Run(ctx, s, c, "resume-bootstrap-file", input, output)
	if e != nil || status != Verified {
		t.Fatal(status, e)
	}
	if retained(t, output).Password != password {
		t.Fatal("resume regenerated password")
	}
	// A distinct actual schema cannot consume the retained credential even with identical request/config.
	other := identityfixture.New(t)
	if _, e = Run(ctx, other, c, "resume-bootstrap-file", input, output); e == nil {
		t.Fatal("foreign pool accepted")
	}
	if count(t, other, "SELECT count(*) FROM users") != 0 {
		t.Fatal("foreign schema mutated")
	}
}

func TestActualBootstrapRequiresInstallationLock(t *testing.T) {
	s := identityfixture.New(t)
	ctx := context.Background()
	input, output := paths(t)
	c, e := s.DB.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Release()
	if _, e = Run(ctx, s, c, "bootstrap-admin-file", input, output); e == nil {
		t.Fatal("missing lock accepted")
	}
	if _, e = os.Stat(output); !os.IsNotExist(e) {
		t.Fatal("unlocked operation created intent")
	}
	owner := lease(t, s)
	if _, e = Run(ctx, s, c, "bootstrap-admin-file", input, output); e == nil {
		t.Fatal("foreign connection lock accepted")
	}
	var acquired bool
	if e = c.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&acquired); e != nil || acquired {
		t.Fatal("concurrent operator lock", e)
	}
	if _, e = Run(ctx, s, owner, "invalid", input, output); e == nil {
		t.Fatal("invalid command accepted")
	}
	if count(t, s, "SELECT count(*) FROM users") != 0 {
		t.Fatal("lock failures mutated installation")
	}
}
