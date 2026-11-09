package metering

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
)

const meteringInsertColumns = "producer,event_id,tenant_id,workspace_id,occurred_at,gpu_microseconds,tokens,storage_byte_seconds,requests,unmeasured_token_requests"

func removeOwnedMeteringRole(t *testing.T, operator *pgxpool.Pool, role string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var exists bool
		if err := operator.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
			t.Errorf("inspect owned fixture role: %v", err)
			return
		}
		if !exists {
			return
		}
		roleID := pgx.Identifier{role}.Sanitize()
		if _, err := operator.Exec(ctx, "DROP OWNED BY "+roleID); err != nil {
			t.Errorf("revoke owned fixture role grants: %v", err)
		}
		if _, err := operator.Exec(ctx, "DROP ROLE IF EXISTS "+roleID); err != nil {
			t.Errorf("drop owned fixture role: %v", err)
		}
	})
}

func prepareMeteringRuntime(t *testing.T, accounts *identity.Service) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	operator := accounts.DB
	role := "metering_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	secret, err := databaseaccess.NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	removeOwnedMeteringRole(t, operator, role)
	cfg := operator.Config().Copy()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, secret
	cfg.MaxConns, cfg.MinConns = 4, 1
	query := url.Values{"sslmode": {"disable"}}
	for key, value := range cfg.ConnConfig.RuntimeParams {
		query.Set(key, value)
	}
	dsn := url.URL{Scheme: "postgres", User: url.UserPassword(role, secret),
		Host: net.JoinHostPort(cfg.ConnConfig.Host, strconv.Itoa(int(cfg.ConnConfig.Port))),
		Path: "/" + cfg.ConnConfig.Database, RawQuery: query.Encode()}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	policy := accounts.Config
	policy.Server.ListenAddress = "127.0.0.1:18443"
	policy.Database.DSNFile = filepath.Join(dir, "runtime-dsn")
	policy.Database.MaxOpenConnections, policy.Database.MaxIdleConnections = cfg.MaxConns, cfg.MinConns
	policy.Database.StatementTimeoutSeconds = 5
	policy.SMTP.Host, policy.SMTP.Port, policy.SMTP.TLSMode = "smtp.example.invalid", 587, "starttls_required"
	policy.SMTP.ConnectTimeoutSeconds, policy.SMTP.OperationTimeoutSeconds = 5, 20
	if err := os.WriteFile(policy.Database.DSNFile, []byte(dsn.String()), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := yaml.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "runtime.yaml")
	if err := os.WriteFile(configPath, settings, 0600); err != nil {
		t.Fatal(err)
	}
	input, err := databaseaccess.LoadRuntime(configPath)
	if err != nil {
		t.Fatal("load protected serving policy:", err)
	}

	lease, err := operator.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	var locked bool
	if err := lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); err != nil || !locked {
		t.Fatal("acquire fixture installation lock", err)
	}
	defer lease.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
	if status, err := databaseaccess.Prepare(ctx, operator, lease, input); err != nil || status != databaseaccess.Verified {
		t.Fatalf("prepare current serving role: %s, %v", status, err)
	}

	wrong := cfg.Copy()
	wrong.ConnConfig.Password, err = databaseaccess.NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated, err := pgxpool.NewWithConfig(ctx, wrong)
	if err != nil {
		t.Fatal(err)
	}
	err = unauthenticated.Ping(ctx)
	unauthenticated.Close()
	var authenticationFailure *pgconn.PgError
	if !errors.As(err, &authenticationFailure) || authenticationFailure.Code != "28P01" {
		t.Fatal("a wrong serving credential was not refused")
	}

	runtime, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	store, err := transaction.FromPostgreSQL(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CheckRuntimeReady(ctx, store); err != nil {
		t.Fatal("predecessor serving readiness:", err)
	}
	var current, session string
	if err := runtime.QueryRow(ctx, "SELECT current_user, session_user").Scan(&current, &session); err != nil ||
		current != role || session != role {
		t.Fatal("expected a direct login by the installed serving role", err)
	}
	accounts.Config = policy
	return runtime
}

func TestPostgresLedgerCandidateServingRole(t *testing.T) {
	accounts := identityfixture.New(t)
	operator := accounts.DB
	runtime := prepareMeteringRuntime(t, accounts)
	accounts.DB, accounts.Mail.DB = runtime, runtime
	password := identityfixture.Bootstrap(t, accounts)
	actor := identityfixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	authority := &workspaces.Service{DB: runtime, Identity: accounts}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := operator.Exec(ctx, testSchemaSQL); err != nil {
		t.Fatal("apply the unnumbered candidate:", err)
	}
	cfg := runtime.Config().ConnConfig
	schema := cfg.RuntimeParams["search_path"]
	table := pgx.Identifier{schema, "usage_ledger"}.Sanitize()
	role := pgx.Identifier{cfg.User}.Sanitize()
	if _, err := operator.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+table+" TO "+role); err != nil {
		t.Fatal("extend the fixture with the current business table grant:", err)
	}
	store, err := transaction.FromPostgreSQL(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CheckRuntimeReady(ctx, store); err == nil || err.Error() != "SCHEMA_CATALOG_INCOMPATIBLE" {
		t.Fatal("the unnumbered catalog unexpectedly admitted serving", err)
	}
	scope := Scope{TenantID: uuid.MustParse(actor.Me.Workspace.TenantID), WorkspaceID: uuid.MustParse(actor.Me.Workspace.ID)}
	at := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	charge := Event{Producer: "fixture/serving-role", ID: "first", OccurredAt: at, Usage: Usage{Requests: 3}}
	card := testCard(t, testRateCard)
	checkWriteAndReport := func(event Event, wantAmount string) {
		t.Helper()
		if inserted, err := writeEvent(ctx, runtime, scope, event); err != nil || !inserted {
			t.Fatalf("serving append: %v, %v", inserted, err)
		}
		if inserted, err := writeEvent(ctx, runtime, scope, event); err != nil || inserted {
			t.Fatalf("serving replay: %v, %v", inserted, err)
		}
		report, err := ReadWorkspaceReport(ctx, authority, actor, scope.WorkspaceID.String(), at, at.Add(time.Hour), card, nil)
		if err != nil || !report.Showback.FullyRated || report.Showback.KnownAmountMicros != wantAmount {
			t.Fatalf("serving report: %+v, %v", report, err)
		}
	}
	checkWriteAndReport(charge, "2")
	checkRefusal := func(err error, code, message string) {
		t.Helper()
		var problem *pgconn.PgError
		if !errors.As(err, &problem) || problem.Code != code || (message != "" && problem.Message != message) {
			t.Fatalf("expected PostgreSQL refusal %s %s, got %v", code, message, err)
		}
	}
	for _, command := range []string{
		"UPDATE usage_ledger SET requests=11",
		"DELETE FROM usage_ledger",
	} {
		_, err := runtime.Exec(ctx, command)
		checkRefusal(err, "55000", "METERING_APPEND_ONLY")
	}
	for _, command := range []string{
		"TRUNCATE usage_ledger",
		"ALTER TABLE usage_ledger DISABLE TRIGGER usage_ledger_append_only",
		"SET session_replication_role = replica",
	} {
		_, err := runtime.Exec(ctx, command)
		checkRefusal(err, "42501", "")
	}
	_, err = runtime.Exec(ctx, `INSERT INTO usage_ledger
 (producer,event_id,tenant_id,workspace_id,occurred_at,gpu_microseconds,tokens,storage_byte_seconds,requests)
 VALUES($1,$2,$3,$4,$5,0,0,0,4) ON CONFLICT (producer,event_id) DO UPDATE SET requests=excluded.requests`,
		charge.Producer, charge.ID, scope.TenantID, scope.WorkspaceID, at)
	checkRefusal(err, "55000", "METERING_APPEND_ONLY")

	const insertOutside = `INSERT INTO usage_ledger
 (producer,event_id,tenant_id,workspace_id,site_id,cell_id,occurred_at,gpu_microseconds,tokens,storage_byte_seconds,requests)
 VALUES('fixture/untrusted','attempt',$1,$2,$3,$4,$5,0,0,0,1)`
	for _, coordinates := range [][2]string{{"default", "foreign-cell"}, {"foreign-site", "default"}} {
		_, err := runtime.Exec(ctx, insertOutside, scope.TenantID, scope.WorkspaceID, coordinates[0], coordinates[1], at)
		checkRefusal(err, "23514", "METERING_FOREIGN_CELL")
	}

	var temporaryAllowed bool
	if err := runtime.QueryRow(ctx, "SELECT has_database_privilege(current_database(),'TEMP')").Scan(&temporaryAllowed); err != nil {
		t.Fatal(err)
	}
	if temporaryAllowed {
		err := pgx.BeginFunc(ctx, runtime, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `CREATE TEMP TABLE cells(site_id varchar,id varchar,is_local boolean) ON COMMIT DROP;
 INSERT INTO cells VALUES('foreign-site','foreign-cell',true)`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "SET LOCAL search_path=pg_temp,"+pgx.Identifier{schema}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			var site, cell string
			if err := tx.QueryRow(ctx, "SELECT local_site_id(),local_cell_id()").Scan(&site, &cell); err != nil ||
				site != "foreign-site" || cell != "foreign-cell" {
				t.Fatal("fixture did not shadow the caller's lookup", err)
			}
			_, err := tx.Exec(ctx, insertOutside, scope.TenantID, scope.WorkspaceID, site, cell, at)
			return err
		})
		checkRefusal(err, "23514", "METERING_FOREIGN_CELL")
	}

	outsider := "metering_outsider_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	outsiderID := pgx.Identifier{outsider}.Sanitize()
	if _, err := operator.Exec(ctx, "CREATE ROLE "+outsiderID+" NOLOGIN NOINHERIT"); err != nil {
		t.Fatal(err)
	}
	removeOwnedMeteringRole(t, operator, outsider)
	if _, err := operator.Exec(ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO "+outsiderID); err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct {
		sql  string
		args []any
	}{
		{sql: "SELECT count(*) FROM usage_ledger"},
		{sql: insertOutside, args: []any{scope.TenantID, scope.WorkspaceID, "default", "default", at}},
	} {
		err := pgx.BeginFunc(ctx, operator, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+outsiderID); err != nil {
				t.Fatal(err)
			}
			_, err := tx.Exec(ctx, command.sql, command.args...)
			return err
		})
		checkRefusal(err, "42501", "")
	}

	if _, err := operator.Exec(ctx, "REVOKE ALL ON TABLE "+table+" FROM "+role+
		"; GRANT SELECT ON TABLE "+table+" TO "+role+
		"; GRANT INSERT ("+meteringInsertColumns+") ON TABLE "+table+" TO "+role); err != nil {
		t.Fatal("apply the ledger-only minimum grants:", err)
	}
	charge.ID = "minimum-grants"
	checkWriteAndReport(charge, "4")
	_, err = runtime.Exec(ctx, insertOutside, scope.TenantID, scope.WorkspaceID, "default", "default", at)
	checkRefusal(err, "42501", "")
	var rows, requests int
	if err := runtime.QueryRow(ctx, "SELECT count(*),sum(requests)::int FROM usage_ledger").Scan(&rows, &requests); err != nil ||
		rows != 2 || requests != 6 {
		t.Fatalf("attempted bypass changed the ledger: %d rows, %d requests, %v", rows, requests, err)
	}
}
