package nodes_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
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
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
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
func TestRestrictedComposedGatewayAuthorityPrototype(t *testing.T) {
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
	gateway, err := transport.NewAgentGateway(limitedIdentity, func() error { return nil })
	if err != nil {
		t.Fatal("actual gateway composition refused its fixture")
	}
	limited := gateway.Nodes
	firstConnection, err := runtime.Acquire(ctx)
	if err != nil {
		t.Fatal("first role budget connection failed")
	}
	secondConnection, err := runtime.Acquire(ctx)
	if err != nil {
		firstConnection.Release()
		t.Fatal("second role budget connection failed")
	}
	third, budgetError := pgx.ConnectConfig(ctx, runtime.Config().ConnConfig.Copy())
	if third != nil {
		third.Close(ctx)
	}
	controlError := account.DB.Ping(ctx)
	secondConnection.Release()
	firstConnection.Release()
	var budgetFailure *pgconn.PgError
	if !errors.As(budgetError, &budgetFailure) || budgetFailure.Code != "53300" {
		t.Fatal("third real role connection did not refuse with 53300")
	}
	if controlError != nil {
		t.Fatal("control-side fixture pool failed while gateway role budget was exhausted")
	}
	t.Log("prototype_aggregate_connection_limit=2 third_connection=53300 control_fixture_pool=available")
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

	// Reviewed additional owner grants are applied only after the explicit missing-right case.
	type observationProbe struct {
		node  wire.Enrolled
		input wire.Handshake
		table string
	}
	var probes []observationProbe
	for _, profile := range []string{wire.SystemdComponentsProfile, wire.DockerInferenceProfile} {
		token, e := normal.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Restricted "+profile, profile, uuid.NewString())
		if e != nil {
			t.Fatal("observation profile preparation failed")
		}
		h := wire.Handshake{ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile), Facts: facts()}
		enrolled, e := normal.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
		if e != nil {
			t.Fatal("observation node enrollment failed")
		}
		table := "inference_nodes"
		if profile == wire.SystemdComponentsProfile {
			table = "managed_components"
			h.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: h.Facts.ObservedAt}
		} else {
			h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: h.Facts.ObservedAt}
		}
		var beforeTime time.Time
		if e = operator.QueryRow(ctx, "SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1", enrolled.NodeID).Scan(&beforeTime); e != nil {
			t.Fatal("initial observation node readback failed")
		}
		refusalCount := traced.refused.Load()
		if _, e = limited.Heartbeat(ctx, enrolled.NodeID, enrolled.Credential, h); e == nil || traced.refused.Load() <= refusalCount {
			t.Fatal("missing observation table privilege did not refuse the actual heartbeat")
		}
		var afterTime time.Time
		var rows int
		if e = operator.QueryRow(ctx, "SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1", enrolled.NodeID).Scan(&afterTime); e != nil || !beforeTime.Equal(afterTime) {
			t.Fatal("failed observation heartbeat changed the node timestamp")
		}
		if e = operator.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{schema, table}.Sanitize()+" WHERE node_id=$1", enrolled.NodeID).Scan(&rows); e != nil || rows != 0 {
			t.Fatal("failed observation heartbeat persisted an observation")
		}
		if _, e = operator.Exec(ctx, "GRANT SELECT,INSERT,UPDATE ON "+pgx.Identifier{schema, table}.Sanitize()+" TO "+role); e != nil {
			t.Fatal("reviewed observation grant failed")
		}
		for pass := 0; pass < 2; pass++ {
			if pass == 1 {
				if h.Component != nil {
					h.Component.State = "unknown"
					h.Component.ErrorCode = "COMPONENT_SYSTEMD_UNAVAILABLE"
				} else {
					h.Inference.ProbeErrorCode = "INFERENCE_NVIDIA_UNAVAILABLE"
				}
			}
			reply, e := limited.Heartbeat(ctx, enrolled.NodeID, enrolled.Credential, h)
			if e != nil || reply.Status != "active" {
				t.Fatal("restricted composed observation heartbeat failed")
			}
			var stored time.Time
			var local bool
			var body []byte
			if e = operator.QueryRow(ctx, "SELECT o.observation,n.last_heartbeat_at,o.tenant_id=n.tenant_id AND o.workspace_id=n.workspace_id AND o.site_id=n.site_id AND o.cell_id=n.cell_id FROM "+pgx.Identifier{schema, table}.Sanitize()+" o JOIN managed_nodes n ON n.id=o.node_id WHERE n.id=$1", enrolled.NodeID).Scan(&body, &stored, &local); e != nil || !stored.Equal(reply.ServerTime) || !local {
				t.Fatal("restricted observation lost its committed timestamp or scope")
			}
			var expectedBody []byte
			if h.Component != nil {
				expectedBody, _ = json.Marshal(h.Component)
			} else {
				expectedBody, _ = json.Marshal(h.Inference)
			}
			var actualValue, expectedValue any
			if json.Unmarshal(body, &actualValue) != nil || json.Unmarshal(expectedBody, &expectedValue) != nil || fmt.Sprint(actualValue) != fmt.Sprint(expectedValue) {
				t.Fatal("restricted observation did not preserve its exact report")
			}
			if _, e = limited.Heartbeat(ctx, enrolled.NodeID, second.Credential, h); e == nil || e.Error() != "AGENT_UNAUTHORIZED" {
				t.Fatal("foreign observation credential was not refused")
			}
			var afterDenied time.Time
			if e = operator.QueryRow(ctx, "SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1", enrolled.NodeID).Scan(&afterDenied); e != nil || !afterDenied.Equal(stored) {
				t.Fatal("denied observation mutated the node")
			}
		}
		probes = append(probes, observationProbe{enrolled, h, table})
		t.Logf("prototype_observation profile=%s first_update_committed=true missing_grant_rollback=true foreign_refused=true", profile)
	}
	if err = limited.EnableLiveness(ctx); err != nil {
		t.Fatal("restricted liveness load failed")
	}
	for _, probe := range probes {
		// Seed the state digest after enabling the actual liveness owner, then queue a repeat.
		if _, err = limited.Heartbeat(ctx, probe.node.NodeID, probe.node.Credential, probe.input); err != nil {
			t.Fatal("restricted liveness state heartbeat failed")
		}
		var synchronous time.Time
		if err = operator.QueryRow(ctx, "SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1", probe.node.NodeID).Scan(&synchronous); err != nil {
			t.Fatal("liveness state readback failed")
		}
		repeat, e := limited.Heartbeat(ctx, probe.node.NodeID, probe.node.Credential, probe.input)
		if e != nil {
			t.Fatal("restricted repeated heartbeat failed")
		}
		var beforeFlush time.Time
		if e = operator.QueryRow(ctx, "SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1", probe.node.NodeID).Scan(&beforeFlush); e != nil || !beforeFlush.Equal(synchronous) {
			t.Fatal("unchanged heartbeat unexpectedly wrote before its flush")
		}
		if e = limited.FlushLiveness(ctx); e != nil {
			t.Fatal("restricted actual observation batch failed")
		}
		var flushed, ownerAt time.Time
		stamp := "received_at"
		if probe.table == "managed_components" {
			stamp = "observed_at"
		}
		if e = operator.QueryRow(ctx, "SELECT n.last_heartbeat_at,o."+stamp+" FROM "+pgx.Identifier{schema, probe.table}.Sanitize()+" o JOIN managed_nodes n ON n.id=o.node_id WHERE n.id=$1", probe.node.NodeID).Scan(&flushed, &ownerAt); e != nil || !flushed.Equal(repeat.ServerTime) || !ownerAt.Equal(repeat.ServerTime) {
			t.Fatal("restricted node/owner flush timestamp did not match the acknowledged repeat")
		}
	}
	if _, err = operator.Exec(ctx, "GRANT SELECT ON "+pgx.Identifier{schema, "control_outbox"}.Sanitize()+" TO "+role); err != nil {
		t.Fatal("reviewed outbox read grant failed")
	}
	assertDenied("outbox insertion", "INSERT INTO "+pgx.Identifier{schema, "control_outbox"}.Sanitize()+"(topic,key) VALUES('prototype','denied')")
	assertDenied("outbox deletion", "DELETE FROM "+pgx.Identifier{schema, "control_outbox"}.Sanitize()+" WHERE false")
	workerContext, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); limited.RunWakes(workerContext) }()
	defer func() { stopWorker(); <-workerDone }()
	waitUntil(t, "restricted outbox reader", limited.WakesDelivering)
	idle := enroll(t, normal, admin, "Restricted held-claim wake")
	result := make(chan wire.Claim, 1)
	claimFailure := make(chan error, 1)
	go func() {
		out, e := limited.ClaimWaiting(workerContext, idle.NodeID, idle.Credential, uuid.NewString(), wire.ClaimRequest{WaitSeconds: 30}, openSeat)
		result <- out
		claimFailure <- e
	}()
	waitUntil(t, "restricted held claim", func() bool { return limited.WatchedNodes() == 1 && runtime.Stat().AcquiredConns() == 0 })
	written, e := normal.CreateTask(ctx, admin, idle.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("control fixture task write failed")
	}
	select {
	case leased := <-result:
		if e = <-claimFailure; e != nil || leased.Lease == nil || leased.Lease.TaskID != written.ID {
			t.Fatal("restricted held claim did not receive the control-side task")
		}
		acknowledge(t, limited, idle, leased)
	case <-time.After(15 * time.Second):
		t.Fatal("restricted outbox did not wake its held claim")
	}
	for _, sample := range limited.WakeSamples() {
		if sample.Name == "ownsolid_node_task_wake_read_failures_total" && sample.Value != 0 {
			t.Fatal("restricted outbox worker reported a read failure")
		}
	}
	t.Log("prototype_actual_liveness_batches=2 held_claim_woken=true outbox_write_rights=false")
	before := traced.refused.Load()
	if _, err = operator.Exec(ctx, "REVOKE EXECUTE ON FUNCTION "+function+"(uuid) FROM "+role); err != nil {
		t.Fatal("prototype execution grant could not be revoked")
	}
	out, err := limited.ClaimFrom(ctx, first.NodeID, first.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: cursor})
	if err == nil || out.Lease != nil || traced.refused.Load() <= before {
		t.Fatal("revoked authority function did not fail the real claim closed")
	}
	t.Log("Actual restricted LOGIN, heartbeat, foreign credential, claim, acknowledgement and permission revocation passed; deployment-bearing observations, delegated profiles and product activation are not qualified")
}
