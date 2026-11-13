package nodes_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type observationCostFixture struct {
	account     *identity.Service
	admin       identity.Auth
	handler     *transport.Handler
	client      *httpfixture.Client
	handshake   wire.Handshake
	node        wire.Enrolled
	path        string
	recorder    agentcost.Recorder
	server      *httptest.Server
	roots       *x509.CertPool
	certificate tls.Certificate
}

func newObservationCostFixture(t *testing.T, profile string, peers int) *observationCostFixture {
	t.Helper()
	f := &observationCostFixture{}
	f.account = fixture.NewConfigured(t, f.recorder.Configure)
	var parameters string
	if err := f.account.DB.QueryRow(t.Context(), "SHOW auto_explain.log_parameter_max_length").Scan(&parameters); err != nil || parameters != "0" {
		t.Fatal("cost recorder must suppress authentication parameter values", err)
	}
	password := fixture.Bootstrap(t, f.account)
	f.admin = fixture.Login(t, f.account, "admin@example.invalid", password, "admin")
	bundle, caKey := agentca.Material(t)
	authority, err := agentidentity.NewAuthority(bundle, caKey, 720*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = transport.New(f.account)
	f.handler.Nodes.Service.Certificates = authority
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		t.Fatal("fixture CA was not loaded")
	}
	server := httpfixture.NewTLSServerAskingClients(t, f.handler, roots)
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	f.client = httpfixture.New(t, server, "admin", schemas)
	token, err := f.handler.Nodes.Service.MintForProfile(t.Context(), f.admin, f.admin.Me.Workspace.ID, "Observation cost node", profile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f.handshake = wire.Handshake{ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile), Facts: facts()}
	key, err := wire.NewCertificateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := wire.NewCertificateRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	body := f.client.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: token.Token, CertificateRequest: csr, Handshake: f.handshake}, 201, "EnrolledResponse")
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &f.node); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair([]byte(f.node.Certificate), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	f.client = f.client.WithCertificate(cert)
	f.server, f.roots, f.certificate = server, roots, cert
	f.path = "/agent/v1" + (wire.Scope{SiteID: f.node.SiteID, CellID: f.node.CellID}).Path() + "/nodes/" + f.node.NodeID + "/heartbeat"
	seedObservationCostFleet(t, f.account.DB, f.node.NodeID, f.admin.Me.User.ID, profile, peers)
	return f
}

func (f *observationCostFixture) restartDispatcher(t *testing.T) {
	t.Helper()
	authority := f.handler.Nodes.Service.Certificates
	f.server.Close()
	f.handler = transport.New(f.account)
	f.handler.Nodes.Service.Certificates = authority
	f.server = httpfixture.NewTLSServerAskingClients(t, f.handler, f.roots)
	t.Cleanup(f.server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	f.client = httpfixture.New(t, f.server, "admin", schemas).WithCertificate(f.certificate)
}

// The independent holder makes a fleet-wide exclusive authority lock fail the
// real request instead of allowing a single-client fixture to conceal it.
func (f *observationCostFixture) record(t *testing.T, exchange func()) agentcost.Cost {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	holder, err := f.account.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = holder.Rollback(cleanup)
	}()
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"SELECT 1 FROM installation_state WHERE id=1 FOR SHARE", nil},
		{"SELECT 1 FROM tenants WHERE id=$1 FOR SHARE", []any{f.admin.Me.Workspace.TenantID}},
		{"SELECT 1 FROM workspaces WHERE id=$1 FOR SHARE", []any{f.admin.Me.Workspace.ID}},
		{"SELECT 1 FROM users WHERE id=$1 FOR SHARE", []any{f.admin.Me.User.ID}},
		{"SELECT 1 FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2 FOR SHARE", []any{f.admin.Me.Workspace.ID, f.admin.Me.User.ID}},
	} {
		if held, err := holder.Exec(ctx, row.sql, row.args...); err != nil || held.RowsAffected() != 1 {
			t.Fatal("shared authority row was not held", err)
		}
	}
	cost, err := f.recorder.Record(exchange)
	if err != nil {
		t.Fatal(err)
	}
	return cost
}

func compareObservationCosts(t *testing.T, names []string, small, large map[string]agentcost.Cost) {
	t.Helper()
	if len(small) != len(names) || len(large) != len(names) {
		t.Fatal("observation exchange coverage changed", len(small), len(large))
	}
	for _, name := range names {
		a, b := small[name], large[name]
		t.Logf("%s: statements %d/%d, rows %d/%d, blocks %d/%d", name, len(a.Statements), len(b.Statements), a.Rows(), b.Rows(), a.Blocks(), b.Blocks())
		if len(a.Statements) == 0 || len(b.Statements) == 0 {
			t.Errorf("%s: missing a measurement", name)
		}
		for _, violation := range agentcost.Violations(wire.CallHeartbeat.Cost(), a, b) {
			t.Errorf("%s: %s", name, violation)
		}
	}
}

func seedObservationCostFleet(t *testing.T, db *pgxpool.Pool, node, actor, profile string, peers int) {
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
    SELECT gen_random_uuid(),m.tenant_id,m.workspace_id,'observation-cost-peer-'||g,m.status,m.protocol_version,m.facts,m.execution_profile,m.capabilities,sha256(g::text::bytea),clock_timestamp()
    FROM managed_nodes m,generate_series(1,$1::integer) g WHERE m.id=$2`, []any{peers, node}},
			{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
    SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,$2,'succeeded',gen_random_uuid(),$1,clock_timestamp(),'{}',clock_timestamp()
    FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'observation-cost-peer-%'`, []any{actor, wire.CollectFacts}},
			{`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id)
    SELECT gen_random_uuid(),clock_timestamp(),n.tenant_id,n.workspace_id,'system','node.task_claimed','success','node_task',gen_random_uuid(),gen_random_uuid()
    FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'observation-cost-peer-%'`, nil},
		} {
			if _, err := tx.Exec(ctx, seed.sql, seed.args...); err != nil {
				return err
			}
		}
		var ownerTable string
		switch profile {
		case wire.SystemdComponentsProfile:
			ownerTable = "managed_components"
			if _, err := tx.Exec(ctx, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,site_id,cell_id)
    SELECT tenant_id,workspace_id,id,$1,$2,'not_installed','not_installed',false,'{}',clock_timestamp(),site_id,cell_id FROM managed_nodes WHERE name LIKE 'observation-cost-peer-%'`, wire.NodeExporter, wire.NodeExporterVersion); err != nil {
				return err
			}
		case wire.DockerInferenceProfile:
			ownerTable = "inference_nodes"
			if _, err := tx.Exec(ctx, `INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation,site_id,cell_id)
    SELECT tenant_id,workspace_id,id,jsonb_build_object('gpus','[]'::jsonb,'observed_at',clock_timestamp()),site_id,cell_id FROM managed_nodes WHERE name LIKE 'observation-cost-peer-%'`); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported observation cost profile: %s", profile)
		}
		var nodes, observations, tasks, audits int
		if err := tx.QueryRow(ctx, `SELECT
   (SELECT count(*) FROM managed_nodes WHERE name LIKE 'observation-cost-peer-%'),
   (SELECT count(*) FROM `+ownerTable+` o JOIN managed_nodes n ON n.id=o.node_id WHERE n.name LIKE 'observation-cost-peer-%'),
   (SELECT count(*) FROM node_tasks t JOIN managed_nodes n ON n.id=t.node_id WHERE n.name LIKE 'observation-cost-peer-%'),
   (SELECT count(*) FROM audit_events WHERE action='node.task_claimed')`).Scan(&nodes, &observations, &tasks, &audits); err != nil {
			return err
		}
		if nodes != peers || observations != peers || tasks != peers*4 || audits != peers*4 {
			return fmt.Errorf("unexpected observation history: peers=%d observations=%d tasks=%d audits=%d", nodes, observations, tasks, audits)
		}
		t.Logf("seeded peers=%d observations=%d tasks=%d audits=%d; one additional enrolled node", nodes, observations, tasks, audits)
		_, err := tx.Exec(ctx, "ANALYZE managed_nodes, managed_components, inference_nodes, node_tasks, audit_events")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
