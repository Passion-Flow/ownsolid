package nodes_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type prototypePermissionTrace struct {
	t       *testing.T
	refused atomic.Int64
}

func (p *prototypePermissionTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (p *prototypePermissionTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	var failure *pgconn.PgError
	if errors.As(d.Err, &failure) {
		if failure.Code == "42501" {
			p.refused.Add(1)
		}
		p.t.Logf("prototype_sql_refusal state=%s schema=%s table=%s column=%s", failure.Code, failure.SchemaName, failure.TableName, failure.ColumnName)
	}
}
func prototypeOperatorConfig(t *testing.T) *pgxpool.Config {
	t.Helper()
	fd, err := syscall.Open(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"), syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal("protected prototype database input is unavailable")
	}
	file := os.NewFile(uintptr(fd), "prototype-database-input")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		t.Fatal("prototype input protection is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		t.Fatal("prototype database input is invalid")
	}
	config, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil || config.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("owned loopback test database is required")
	}
	config.MaxConns = 2
	return config
}
func TestRestrictedReadOnlyGatewayAuthorityPrototype(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	normal := &nodes.Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	runtimeConfig := account.DB.Config()
	opConfig := prototypeOperatorConfig(t)
	if opConfig.ConnConfig.Host != runtimeConfig.ConnConfig.Host || opConfig.ConnConfig.Port != runtimeConfig.ConnConfig.Port || opConfig.ConnConfig.Database != runtimeConfig.ConnConfig.Database {
		t.Fatal("prototype target differs from the owned fixture")
	}
	schema := runtimeConfig.ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(schema, "m01_") || strings.ContainsAny(schema, ",\" ") {
		t.Fatal("prototype schema is not the owned fixture")
	}
	opConfig.ConnConfig.RuntimeParams["search_path"] = schema
	opConfig.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	opConfig.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	operator, err := pgxpool.NewWithConfig(ctx, opConfig)
	if err != nil {
		t.Fatal("prototype operator pool could not open")
	}
	defer operator.Close()
	var schemaVersion, tables int
	if err = operator.QueryRow(ctx, "SELECT max(version),(SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()) FROM schema_migrations").Scan(&schemaVersion, &tables); err != nil {
		t.Fatal("fixture catalog observation failed")
	}
	t.Logf("prototype_product_catalog version=%d tables=%d", schemaVersion, tables)
	function := pgx.Identifier{schema, "prototype_lock_installation"}.Sanitize()
	roleName := "arc111_gateway_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	role := pgx.Identifier{roleName}.Sanitize()
	rolePassword := rand.Text()
	if _, err = operator.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 2 PASSWORD $1", pgx.QueryExecModeSimpleProtocol, rolePassword); err != nil {
		t.Fatal("prototype role could not be created")
	}
	var scram bool
	if err = operator.QueryRow(ctx, "SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname=$1", roleName).Scan(&scram); err != nil || !scram {
		t.Fatal("prototype role does not have a SCRAM verifier")
	}
	var runtime *pgxpool.Pool
	defer func() {
		if runtime != nil {
			runtime.Close()
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if _, e := operator.Exec(cleanup, "DROP OWNED BY "+role); e != nil {
			t.Error("owned prototype grants could not be removed")
		}
		if _, e := operator.Exec(cleanup, "DROP ROLE "+role); e != nil {
			t.Error("owned prototype role could not be removed")
		}
	}()
	ddl := fmt.Sprintf(`BEGIN;
 CREATE FUNCTION %s(expected uuid) RETURNS uuid LANGUAGE SQL SECURITY DEFINER SET search_path=pg_catalog,pg_temp
 BEGIN ATOMIC
 SELECT i.tenant_id FROM %s i JOIN %s t ON t.id=i.tenant_id
 WHERE i.id=1 AND i.tenant_id=expected AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t;
 END;
 REVOKE ALL ON FUNCTION %s(uuid) FROM PUBLIC;
 GRANT EXECUTE ON FUNCTION %s(uuid) TO %s;
 GRANT USAGE ON SCHEMA %s TO %s;
 COMMIT;`, function, pgx.Identifier{schema, "installation_state"}.Sanitize(), pgx.Identifier{schema, "tenants"}.Sanitize(), function, function, role, pgx.Identifier{schema}.Sanitize(), role)
	if _, err = operator.Exec(ctx, ddl); err != nil {
		t.Fatal("closed prototype authority function could not be prepared")
	}
	for _, table := range []string{"installation_state", "tenants", "workspaces", "sites", "cells", "agent_pacing", "schema_migrations", "managed_nodes", "node_tasks", "node_task_history"} {
		if _, err = operator.Exec(ctx, "GRANT SELECT ON TABLE "+pgx.Identifier{schema, table}.Sanitize()+" TO "+role); err != nil {
			t.Fatalf("reviewed SELECT grant unavailable for %s", table)
		}
	}
	for _, table := range []string{"managed_nodes", "node_tasks"} {
		if _, err = operator.Exec(ctx, "GRANT UPDATE ON TABLE "+pgx.Identifier{schema, table}.Sanitize()+" TO "+role); err != nil {
			t.Fatalf("reviewed UPDATE grant unavailable for %s", table)
		}
	}
	for _, table := range []string{"audit_events", "node_task_history"} {
		if _, err = operator.Exec(ctx, "GRANT INSERT ON TABLE "+pgx.Identifier{schema, table}.Sanitize()+" TO "+role); err != nil {
			t.Fatalf("reviewed INSERT grant unavailable for %s", table)
		}
	}
	first := enroll(t, normal, admin, "Gateway role prototype first")
	second := enroll(t, normal, admin, "Gateway role prototype second")
	task, err := normal.CreateTask(ctx, admin, first.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal("prototype task preparation failed")
	}
	traced := &prototypePermissionTrace{t: t}
	limitedConfig := runtimeConfig.Copy()
	limitedConfig.ConnConfig.User = roleName
	limitedConfig.ConnConfig.Password = rolePassword
	limitedConfig.MaxConns = 2
	limitedConfig.MinConns = 0
	limitedConfig.ConnConfig.Tracer = traced
	runtime, err = pgxpool.NewWithConfig(ctx, limitedConfig)
	if err != nil {
		t.Fatal("restricted prototype pool could not open")
	}
	var user, session string
	if err = runtime.QueryRow(ctx, "SELECT current_user,session_user").Scan(&user, &session); err != nil || user != roleName || session != roleName {
		t.Fatal("independent runtime login was not established")
	}
	limitedIdentity := &identity.Service{DB: runtime, Config: account.Config}
	limited := &nodes.Service{DB: runtime, Identity: limitedIdentity, Workspaces: &workspaces.Service{DB: runtime, Identity: limitedIdentity}}
	assertDenied := func(name, query string) {
		t.Helper()
		_, e := runtime.Exec(ctx, query)
		var failure *pgconn.PgError
		if !errors.As(e, &failure) || failure.Code != "42501" {
			t.Fatalf("%s did not refuse with 42501", name)
		}
	}
	assertDenied("direct installation mutation", "UPDATE "+pgx.Identifier{schema, "installation_state"}.Sanitize()+" SET bootstrap_complete=false WHERE false")
	assertDenied("direct tenant mutation", "UPDATE "+pgx.Identifier{schema, "tenants"}.Sanitize()+" SET status='disabled' WHERE false")
	assertDenied("human identities", "SELECT 1 FROM "+pgx.Identifier{schema, "users"}.Sanitize()+" LIMIT 1")
	assertDenied("human memberships", "SELECT 1 FROM "+pgx.Identifier{schema, "workspace_memberships"}.Sanitize()+" LIMIT 1")
	assertDenied("private audit head", "SELECT 1 FROM "+pgx.Identifier{schema, "audit_chain_head"}.Sanitize()+" LIMIT 1")
	assertDenied("migration journal write", "UPDATE "+pgx.Identifier{schema, "schema_migrations"}.Sanitize()+" SET version=version WHERE false")
	observed, err := limited.Heartbeat(ctx, first.NodeID, first.Credential, handshake())
	if err != nil || observed.Status != "active" {
		t.Fatal("actual restricted heartbeat failed; inspect scoped SQL refusal")
	}
	var stored time.Time
	if err = operator.QueryRow(ctx, "SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1", first.NodeID).Scan(&stored); err != nil || !stored.Equal(observed.ServerTime) {
		t.Fatal("restricted heartbeat was not committed with its returned timestamp")
	}
	if _, err = limited.Heartbeat(ctx, first.NodeID, second.Credential, handshake()); err == nil || err.Error() != "AGENT_UNAUTHORIZED" {
		t.Fatal("foreign node credential was not refused")
	}
	claim, err := limited.ClaimFrom(ctx, first.NodeID, first.Credential, uuid.NewString(), wire.ClaimRequest{})
	if err != nil || claim.Lease == nil || claim.Lease.TaskID != task.ID || claim.Lease.Attempt != 1 {
		t.Fatal("actual restricted facts claim failed; inspect scoped SQL refusal")
	}
	acknowledge(t, limited, first, claim)
	var acknowledged, attempt int
	var cursor uint64
	if err = operator.QueryRow(ctx, "SELECT t.attempt,t.acknowledged_attempt,n.task_acknowledged_sequence FROM node_tasks t JOIN managed_nodes n ON n.id=t.node_id WHERE t.id=$1", task.ID).Scan(&attempt, &acknowledged, &cursor); err != nil || attempt != 1 || acknowledged != 1 || cursor != claim.Lease.Sequence {
		t.Fatal("restricted acknowledgement did not preserve the lease and cursor")
	}
	before := traced.refused.Load()
	if _, err = operator.Exec(ctx, "REVOKE EXECUTE ON FUNCTION "+function+"(uuid) FROM "+role); err != nil {
		t.Fatal("prototype execution grant could not be revoked")
	}
	out, err := limited.ClaimFrom(ctx, first.NodeID, first.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: cursor})
	if err == nil || out.Lease != nil || traced.refused.Load() <= before {
		t.Fatal("revoked authority function did not fail the real claim closed")
	}
	t.Log("Actual restricted LOGIN, heartbeat, foreign credential, claim, acknowledgement and permission revocation passed; delegated profiles and product activation are not qualified")
}
