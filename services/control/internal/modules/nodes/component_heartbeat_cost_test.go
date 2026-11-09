package nodes_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestComponentHeartbeatCostAtFleetSizes(t *testing.T) {
	const smallFleet, largeFleet = 50, 3000
	small := measureComponentHeartbeats(t, smallFleet, true)
	large := measureComponentHeartbeats(t, largeFleet, false)
	for _, name := range []string{"first component report", "component update", "foreign credential"} {
		a, b := small[name], large[name]
		t.Logf("%s: statements %d/%d, rows %d/%d, blocks %d/%d",
			name, len(a.Statements), len(b.Statements), a.Rows(), b.Rows(), a.Blocks(), b.Blocks())
		if len(a.Statements) == 0 || len(b.Statements) == 0 {
			t.Errorf("%s: missing a measurement", name)
		}
		for _, violation := range agentcost.Violations(wire.CallHeartbeat.Cost(), a, b) {
			t.Errorf("%s: %s", name, violation)
		}
	}
}

func measureComponentHeartbeats(t *testing.T, fleet int, checkRace bool) map[string]agentcost.Cost {
	t.Helper()
	ctx := t.Context()
	measured := map[string]agentcost.Cost{}
	var recorder agentcost.Recorder
	account := fixture.NewConfigured(t, func(c *pgxpool.Config) {
		recorder.Configure(c)
		c.ConnConfig.RuntimeParams["auto_explain.log_parameter_max_length"] = "0"
	})
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	bundle, caKey := agentca.Material(t)
	authority, err := agentidentity.NewAuthority(bundle, caKey, 720*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := transport.New(account)
	handler.Nodes.Service.Certificates = authority
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		t.Fatal("fixture CA was not loaded")
	}
	server := httpfixture.NewTLSServerAskingClients(t, handler, roots)
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	client := httpfixture.New(t, server, "admin", schemas)

	profile := wire.SystemdComponentsProfile
	token, err := handler.Nodes.Service.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Component cost node", profile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	h := wire.Handshake{ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile), Facts: facts()}

	key, err := wire.NewCertificateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := wire.NewCertificateRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	body := client.Call("POST", "/agent/v1/enroll", wire.Enrollment{
		JoinToken: token.Token, CertificateRequest: csr, Handshake: h,
	}, 201, "EnrolledResponse")
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var node wire.Enrolled
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair([]byte(node.Certificate), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	client = client.WithCertificate(cert)
	path := "/agent/v1" + (wire.Scope{SiteID: node.SiteID, CellID: node.CellID}).Path() + "/nodes/" + node.NodeID + "/heartbeat"
	seedComponentCostFleet(t, account.DB, node.NodeID, admin.Me.User.ID, fleet)
	observe := func(t *testing.T, name string, heartbeat func()) {
		t.Helper()
		holder, err := account.DB.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback(context.WithoutCancel(ctx))
		for _, row := range []struct {
			sql  string
			args []any
		}{
			{"SELECT 1 FROM installation_state WHERE id=1 FOR SHARE", nil},
			{"SELECT 1 FROM tenants WHERE id=$1 FOR SHARE", []any{admin.Me.Workspace.TenantID}},
			{"SELECT 1 FROM workspaces WHERE id=$1 FOR SHARE", []any{admin.Me.Workspace.ID}},
			{"SELECT 1 FROM users WHERE id=$1 FOR SHARE", []any{admin.Me.User.ID}},
			{"SELECT 1 FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2 FOR SHARE", []any{admin.Me.Workspace.ID, admin.Me.User.ID}},
		} {
			if _, err := holder.Exec(ctx, row.sql, row.args...); err != nil {
				t.Fatal(err)
			}
		}
		cost, err := recorder.Record(heartbeat)
		if err != nil {
			t.Fatal(err)
		}
		measured[name] = cost
	}
	h.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: h.Facts.ObservedAt}
	client.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
	observe(t, "first component report", func() { client.Call("POST", path, h, 200, "DispositionResponse") })
	first := readComponentHeartbeat(t, account.DB, node.NodeID)
	if first.state != "not_installed" || first.heartbeatAt.IsZero() {
		t.Fatal("first report did not persist")
	}

	h.Component.State = "stopped"
	h.Component.Managed = true
	observe(t, "component update", func() { client.Call("POST", path, h, 200, "DispositionResponse") })
	updated := readComponentHeartbeat(t, account.DB, node.NodeID)
	if updated.state != "stopped" {
		t.Fatal("warm report did not update stored state")
	}

	h.Component.State = "unknown"
	h.Component.ErrorCode = "COMPONENT_SYSTEMD_UNAVAILABLE"
	client.Headers["Authorization"] = "Bearer " + security.Token()
	observe(t, "foreign credential", func() {
		if code := client.Problem("POST", path, h, 401); code != "AGENT_UNAUTHORIZED" {
			t.Fatalf("foreign credential returned %s", code)
		}
	})
	if got := readComponentHeartbeat(t, account.DB, node.NodeID); got != updated {
		t.Fatal("refused heartbeat changed node or component state")
	}

	if checkRace {
		credential := replaceCredentialDuringAgentCall(t, account.DB, node.NodeID, func(ctx context.Context) error {
			_, err := handler.Nodes.Service.Heartbeat(ctx, node.NodeID, node.Credential, h)
			return err
		})
		if got := readComponentHeartbeat(t, account.DB, node.NodeID); got != updated {
			t.Fatal("heartbeat with the replaced credential changed stored state")
		}
		// A disabled node keeps passive reporting authority under its new credential.
		if _, err := account.DB.Exec(ctx, "UPDATE managed_nodes SET status='disabled' WHERE id=$1", node.NodeID); err != nil {
			t.Fatal(err)
		}
		client.Headers["Authorization"] = "Bearer " + credential
		disposition := client.Call("POST", path, h, 200, "DispositionResponse")
		if disposition["status"] != "disabled" || readComponentHeartbeat(t, account.DB, node.NodeID).state != "unknown" {
			t.Fatal("disabled node did not retain passive observation")
		}
	}
	return measured
}

type componentHeartbeatSnapshot struct {
	state, observation      string
	observedAt, heartbeatAt time.Time
}

func readComponentHeartbeat(t *testing.T, db *pgxpool.Pool, node string) componentHeartbeatSnapshot {
	t.Helper()
	var state componentHeartbeatSnapshot
	if err := db.QueryRow(t.Context(), `SELECT c.observed_state,c.observation::text,c.observed_at,n.last_heartbeat_at
		FROM managed_components c JOIN managed_nodes n ON n.id=c.node_id WHERE n.id=$1`, node).
		Scan(&state.state, &state.observation, &state.observedAt, &state.heartbeatAt); err != nil {
		t.Fatal(err)
	}
	return state
}

func replaceCredentialDuringAgentCall(t *testing.T, db *pgxpool.Pool, node string, call func(context.Context) error) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.WithoutCancel(ctx))
	credential := security.Token()
	var holderPID int32
	if err := holder.QueryRow(ctx, "UPDATE managed_nodes SET credential_hash=$1 WHERE id=$2 RETURNING pg_backend_pid()",
		security.Digest("node-credential:v1:"+credential), node).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- call(ctx) }()
	// Confirm discovery saw the old row and the transaction is waiting on
	// the locked node before making the new credential visible.
	wait, cancelWait := context.WithTimeout(ctx, 2*time.Second)
	defer cancelWait()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		var blocked bool
		if err := db.QueryRow(wait, `SELECT EXISTS(SELECT FROM pg_stat_activity
			WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, holderPID).Scan(&blocked); err != nil {
			t.Fatalf("find Agent call waiting on the node lock: %v", err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Agent call finished before the credential transaction committed: %v", err)
		case <-wait.Done():
			t.Fatal("Agent call did not reach the held node lock")
		case <-poll.C:
		}
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		expected(t, err, "AGENT_UNAUTHORIZED")
	case <-ctx.Done():
		t.Fatal("Agent call did not finish after credential rotation")
	}
	return credential
}

func seedComponentCostFleet(t *testing.T, db *pgxpool.Pool, node, actor string, fleet int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for _, seed := range []struct {
			sql  string
			args []any
		}{
			{"SET LOCAL statement_timeout='90s'", nil},
			{"SET LOCAL auto_explain.log_min_duration=-1", nil},
			{`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,protocol_version,facts,execution_profile,capabilities,credential_hash,last_heartbeat_at)
     SELECT gen_random_uuid(),m.tenant_id,m.workspace_id,'component-cost-peer-'||g,m.status,m.protocol_version,m.facts,m.execution_profile,m.capabilities,sha256(g::text::bytea),clock_timestamp()
     FROM managed_nodes m,generate_series(1,$1::integer) g WHERE m.id=$2`, []any{fleet, node}},
			{`INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,site_id,cell_id)
     SELECT tenant_id,workspace_id,id,$1,$2,'not_installed','not_installed',false,'{}',clock_timestamp(),site_id,cell_id FROM managed_nodes WHERE name LIKE 'component-cost-peer-%'`, []any{wire.NodeExporter, wire.NodeExporterVersion}},
			{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
     SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,$2,'succeeded',gen_random_uuid(),$1,clock_timestamp(),'{}',clock_timestamp()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'component-cost-peer-%'`, []any{actor, wire.CollectFacts}},
			{`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id)
     SELECT gen_random_uuid(),clock_timestamp(),n.tenant_id,n.workspace_id,'system','node.task_claimed','success','node_task',gen_random_uuid(),gen_random_uuid()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'component-cost-peer-%'`, nil},
			{"ANALYZE managed_nodes, managed_components, node_tasks, audit_events", nil},
		} {
			if _, err := tx.Exec(ctx, seed.sql, seed.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
