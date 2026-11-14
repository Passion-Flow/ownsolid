package main

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// The real CLI dispatcher uses an isolated loopback synthetic schema; it never serves HTTP.
func TestActualSchemaCheckCLIWithoutAuthenticationAssetsAndNoMutation(t *testing.T) {
	s := identityfixture.New(t)
	ctx := context.Background()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	dsn := filepath.Join(root, "operator-dsn")
	// ConnString retains the original fixture DSN, before its random schema was
	// injected. Serialize the effective isolated connection explicitly.
	cc := s.DB.Config().ConnConfig
	u := url.URL{Scheme: "postgres", User: url.UserPassword(cc.User, cc.Password), Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database}
	q := url.Values{"sslmode": {"disable"}}
	for k, v := range cc.RuntimeParams {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	if e := os.WriteFile(dsn, []byte(u.String()), 0600); e != nil {
		t.Fatal(e)
	}
	c := s.Config
	c.Server.ListenAddress = "127.0.0.1:18443"
	c.Database.DSNFile = dsn
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	// These deliberately missing assets must not be read by schema verification.
	c.Security.AuthHMACKeyFile = filepath.Join(root, "missing-hmac")
	c.Security.MailKeyringFile = filepath.Join(root, "missing-mail")
	c.Security.PasswordBlocklistFile = filepath.Join(root, "missing-blocklist")
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(root, "operator.yaml")
	if e = os.WriteFile(config, raw, 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(dsn)
	args := os.Args
	defer func() { os.Args = args }()
	os.Args = []string{"control", "check-schema", "--config", config}
	var ledgerBefore, usersBefore, auditBefore int
	if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM schema_migrations),(SELECT count(*) FROM users),(SELECT count(*) FROM audit_events)").Scan(&ledgerBefore, &usersBefore, &auditBefore); e != nil {
		t.Fatal(e)
	}
	if e = run(); e != nil {
		t.Fatal("schema command", e)
	}
	var ledgerAfter, usersAfter, auditAfter int
	if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM schema_migrations),(SELECT count(*) FROM users),(SELECT count(*) FROM audit_events)").Scan(&ledgerAfter, &usersAfter, &auditAfter); e != nil || ledgerAfter != ledgerBefore || usersAfter != usersBefore || auditAfter != auditBefore {
		t.Fatal("schema check mutated database", e)
	}
	after, _ := os.ReadFile(dsn)
	if string(before) != string(after) {
		t.Fatal("credential file changed")
	}
	lease, e := s.DB.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("test installation lock", e)
	}
	if e = run(); e == nil || e.Error() != "INSTALLATION_PROCESS_ALREADY_RUNNING" {
		t.Fatal("concurrent operator accepted", e)
	}
	if _, e = lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=1"); e != nil {
		t.Fatal(e)
	}
	if e = run(); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("changed ledger accepted/repaired", e)
	}
	var checksum string
	if e = s.DB.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); e != nil || checksum != "0000000000000000000000000000000000000000000000000000000000000000" {
		t.Fatal("schema verifier repaired ledger", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TABLE schema_migrations"); e != nil {
		t.Fatal(e)
	}
	if e = run(); e == nil || e.Error() != "SCHEMA_UNAVAILABLE" {
		t.Fatal("absent ledger accepted/repaired", e)
	}
	var absent bool
	if e = s.DB.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NULL").Scan(&absent); e != nil || !absent {
		t.Fatal("absent ledger was created", e)
	}
}

func TestActualRuntimeAccessCLIUsesProtectedConfigurationWithoutAuthAssets(t *testing.T) {
	s := identityfixture.New(t)
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	cc := s.DB.Config().ConnConfig
	q := url.Values{"sslmode": {"disable"}}
	for k, v := range cc.RuntimeParams {
		q.Set(k, v)
	}
	uri := url.URL{Scheme: "postgres", User: url.UserPassword(cc.User, cc.Password), Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database, RawQuery: q.Encode()}
	operatorDSN := filepath.Join(root, "operator-dsn")
	if e := os.WriteFile(operatorDSN, []byte(uri.String()), 0600); e != nil {
		t.Fatal(e)
	}
	c := s.Config
	c.Server.ListenAddress = "127.0.0.1:18443"
	c.Database.DSNFile = operatorDSN
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	write := func(name string, value any) string {
		t.Helper()
		b, e := yaml.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		p := filepath.Join(root, name)
		if e = os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	op := write("operator.yaml", c)
	role := "m17_cli_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	secret, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		s.DB.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
		s.DB.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	}()
	uri.User = url.UserPassword(role, secret)
	runtimeDSN := filepath.Join(root, "runtime-dsn")
	if e = os.WriteFile(runtimeDSN, []byte(uri.String()), 0600); e != nil {
		t.Fatal(e)
	}
	c.Database.DSNFile = runtimeDSN
	runtimeConfig := write("runtime.yaml", c)
	args := os.Args
	defer func() { os.Args = args }()
	call := func(command string) error {
		os.Args = []string{"control", command, "--config", op, "--runtime-config", runtimeConfig}
		return run()
	}
	if e = call("check-runtime-access"); e == nil || e.Error() != databaseaccess.NotCommitted {
		t.Fatal("absent role CLI reported success", e)
	}
	if e = call("prepare-runtime-access"); e != nil {
		t.Fatal("actual private CLI preparation", e)
	}
	var before uint32
	s.DB.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&before)
	if e = call("check-runtime-access"); e != nil {
		t.Fatal(e)
	}
	if e = call("prepare-runtime-access"); e != nil {
		t.Fatal(e)
	}
	var after uint32
	s.DB.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&after)
	if before == 0 || before != after {
		t.Fatal("CLI recreated role")
	}
	actual, e := os.ReadFile(runtimeDSN)
	if e != nil || string(actual) != uri.String() {
		t.Fatal("CLI changed protected credential", e)
	}
}
