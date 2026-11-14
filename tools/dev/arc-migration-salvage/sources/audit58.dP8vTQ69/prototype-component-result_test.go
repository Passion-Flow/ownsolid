package deployments_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMutualTLSInferenceObservationCost(t *testing.T) {
	const smallFleet, largeFleet = 50, 3000
	measurements := map[int]map[string]agentcost.Cost{}
	for _, fleet := range []int{smallFleet, largeFleet} {
		t.Run(fmt.Sprint(fleet), func(t *testing.T) {
			measurements[fleet] = measureMutualTLSInferenceObservationCost(t, fleet)
		})
	}
	if t.Failed() {
		return
	}
	for _, name := range []string{"cold first report", "warm update", "foreign credential", "retained current", "stale revision"} {
		small, large := measurements[smallFleet][name], measurements[largeFleet][name]
		t.Logf("%s: statements %d/%d rows %d/%d blocks %d/%d", name, len(small.Statements), len(large.Statements), small.Rows(), large.Rows(), small.Blocks(), large.Blocks())
		if len(small.Statements) == 0 || len(large.Statements) == 0 {
			t.Errorf("%s did not record both measurements", name)
		}
		for _, violation := range agentcost.Violations(wire.CallHeartbeat.Cost(), small, large) {
			t.Errorf("%s: %s", name, violation)
		}
	}
}

func measureMutualTLSInferenceObservationCost(t *testing.T, fleet int) map[string]agentcost.Cost {
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

	profile := wire.DockerInferenceProfile
	token, err := handler.Nodes.Service.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Inference cost node", profile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	h := wire.Handshake{
		ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "inference-cost-node",
			LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30,
			DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion},
	}
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
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: now}
	seedInferenceCostFleet(t, account.DB, node.NodeID, admin.Me.User.ID, fleet)
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
		measured[name] = checkHeartbeatCost(t, &recorder, heartbeat)
	}
	for _, probe := range []struct {
		name, credential, probeError string
	}{
		{name: "cold first report", credential: node.Credential},
		{name: "warm update", credential: node.Credential, probeError: "INFERENCE_NVIDIA_UNAVAILABLE"},
		{name: "foreign credential", credential: security.Token()},
	} {
		t.Run(probe.name, func(t *testing.T) {
			h.Inference.ProbeErrorCode = probe.probeError
			client.Headers = map[string]string{"Authorization": "Bearer " + probe.credential}
			observe(t, probe.name, func() {
				if probe.credential == node.Credential {
					client.Call("POST", path, h, 200, "DispositionResponse")
				} else if code := client.Problem("POST", path, h, 401); code != "AGENT_UNAUTHORIZED" {
					t.Errorf("foreign credential: %s", code)
				}
			})
		})
	}
	var site, cell string
	err = account.DB.QueryRow(ctx, `SELECT site_id,cell_id,observation FROM inference_nodes WHERE node_id=$1`, node.NodeID).Scan(&site, &cell, &raw)
	var stored wire.InferenceObservation
	if err != nil || site != node.SiteID || cell != node.CellID || json.Unmarshal(raw, &stored) != nil || stored.ProbeErrorCode != "INFERENCE_NVIDIA_UNAVAILABLE" {
		t.Fatalf("observation did not persist in its node scope: %q/%q, %v", site, cell, err)
	}

	t.Run("cold report for retained deployment", func(t *testing.T) {
		model := uuid.NewString()
		catalog, err := json.Marshal(wire.ApprovedModel())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := account.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
			VALUES($1,$2,$3,'Approved catalogue fixture',$4,$5,$6,'ready',$7)`,
			model, admin.Me.Workspace.TenantID, admin.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), catalog, admin.Me.User.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		h.Inference = &wire.InferenceObservation{ObservedAt: now, GPUs: []wire.NVIDIAGPU{{
			UUID: gpuID, Name: "Protocol fixture GPU", DriverVersion: "580.1", ComputeCapability: 8,
			MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, MIGMode: "Disabled", ObservedAt: now,
		}}}
		client.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
		client.Call("POST", path, h, 200, "DispositionResponse")
		deployment, err := handler.Deployments.Service.Create(ctx, admin, deployments.CreateRequest{
			NodeID: node.NodeID, ModelID: model, GPUUUID: gpuID, Reason: "Commit the deployment before restarting the dispatcher",
		}, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		claim, err := handler.Nodes.Service.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
		if err != nil || claim.Lease == nil || claim.Lease.Inference == nil {
			t.Fatal("claim retained deployment", err)
		}
		observed := result(*claim.Lease, "running")
		if state, err := handler.Nodes.Service.Complete(ctx, node.NodeID, node.Credential, claim.Lease.TaskID, observed, uuid.NewString()); err != nil || state != "succeeded" {
			t.Fatal("complete retained deployment", state, err)
		}

		server.Close()
		restarted := transport.New(account)
		restarted.Nodes.Service.Certificates = authority
		coldServer := httpfixture.NewTLSServerAskingClients(t, restarted, roots)
		t.Cleanup(coldServer.Close)
		cold := httpfixture.New(t, coldServer, "admin", schemas).WithCertificate(cert)
		cold.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
		h.Inference.Deployment = observed.Inference.Deployment
		h.Inference.Deployment.State = "loading"
		h.Inference.Deployment.Ready = false
		h.Inference.Deployment.Verification = nil
		observe(t, "retained current", func() {
			cold.Call("POST", path, h, 200, "DispositionResponse")
		})
		current, err := restarted.Deployments.Service.Get(ctx, admin, "", deployment.ID)
		if err != nil || current.ObservedState != "loading" || current.ResourceState != "held" || current.Observation == nil || current.Observation.Revision != deployment.Revision {
			t.Fatalf("current report did not update the retained deployment: %+v, %v", current, err)
		}

		// A delayed report from before the new desired revision cannot replace
		// the stored observation or release the deployment's reservation.
		stopping, err := restarted.Deployments.Service.Action(ctx, admin, deployment.ID, deployments.ActionRequest{
			Action: "stop", Revision: current.Revision, Reason: "Advance desired state before the delayed report",
		}, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		h.Inference.Deployment.State = "failed"
		t.Run("stale revision", func(t *testing.T) {
			observe(t, "stale revision", func() {
				cold.Call("POST", path, h, 200, "DispositionResponse")
			})
		})
		current, err = restarted.Deployments.Service.Get(ctx, admin, "", deployment.ID)
		if err != nil || current.Revision != stopping.Revision || current.ObservedState != "loading" || current.ResourceState != "held" {
			t.Fatalf("delayed report changed retained deployment: %+v, %v", current, err)
		}
	})
	return measured
}

func seedInferenceCostFleet(t *testing.T, db *pgxpool.Pool, node, actor string, fleet int) {
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
     SELECT gen_random_uuid(),m.tenant_id,m.workspace_id,'inference-cost-peer-'||g,m.status,m.protocol_version,m.facts,m.execution_profile,m.capabilities,sha256(g::text::bytea),clock_timestamp()
     FROM managed_nodes m,generate_series(1,$1::integer) g WHERE m.id=$2`, []any{fleet, node}},
			{`INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation,site_id,cell_id)
     SELECT tenant_id,workspace_id,id,'{"gpus":[]}',site_id,cell_id FROM managed_nodes WHERE name LIKE 'inference-cost-peer-%'`, nil},
			{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
     SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,'collect_facts.v1','succeeded',gen_random_uuid(),$1,clock_timestamp(),'{}',clock_timestamp()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'inference-cost-peer-%'`, []any{actor}},
			{`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id)
     SELECT gen_random_uuid(),clock_timestamp(),n.tenant_id,n.workspace_id,'system','node.task_claimed','success','node_task',gen_random_uuid(),gen_random_uuid()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'inference-cost-peer-%'`, nil},
			{"ANALYZE managed_nodes, inference_nodes, node_tasks, audit_events", nil},
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

func checkHeartbeatCost(t *testing.T, recorder *agentcost.Recorder, heartbeat func()) agentcost.Cost {
	t.Helper()
	cost, err := recorder.Record(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("statements=%d rows=%d blocks=%d", len(cost.Statements), cost.Rows(), cost.Blocks())
	if n := len(cost.Statements); n == 0 || n > wire.CallHeartbeat.Cost().Statements {
		for _, statement := range cost.Statements {
			t.Log(strings.TrimSpace(statement.Query))
		}
		t.Errorf("ran %d statements for a ceiling of %d", n, wire.CallHeartbeat.Cost().Statements)
	}
	return cost
}

func TestInferenceObservationKeepsNodeScopeOnConcurrentFirstWrites(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	nodes := c.s.Nodes
	db := nodes.DB
	token, err := nodes.MintForProfile(ctx, c.a, c.a.Me.Workspace.ID, "Concurrent first observations", c.h.Profile(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	h := c.h
	h.Inference = nil
	enrolled, err := nodes.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	node, err := nodes.Get(ctx, c.a, "", enrolled.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	// Change only this disposable schema's defaults to distinguish the two
	// identity sources. The persisted node still has its original scope.
	if _, err := db.Exec(ctx, `CREATE OR REPLACE FUNCTION local_site_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT 'other-site'::varchar $$;
		CREATE OR REPLACE FUNCTION local_cell_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT 'other-cell'::varchar $$`); err != nil {
		t.Fatal(err)
	}
	write := func(code string) error {
		observation := wire.InferenceObservation{
			GPUs: []wire.NVIDIAGPU{}, ObservedAt: time.Now().UTC(), ProbeErrorCode: code,
		}
		return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			return c.s.ObserveInferenceWithin(ctx, transaction.PostgreSQLTransaction(tx), node, observation)
		})
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, code := range []string{"INFERENCE_NVIDIA_UNAVAILABLE", "INFERENCE_GPU_PROBE_INVALID"} {
		go func() {
			<-start
			results <- write(code)
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	read := func(site, cell string) wire.InferenceObservation {
		t.Helper()
		var gotSite, gotCell string
		var raw []byte
		err := db.QueryRow(ctx, `SELECT site_id,cell_id,observation FROM inference_nodes WHERE node_id=$1 AND tenant_id=$2 AND workspace_id=$3`,
			node.ID, node.TenantID, node.WorkspaceID).Scan(&gotSite, &gotCell, &raw)
		var observation wire.InferenceObservation
		if err != nil || gotSite != site || gotCell != cell || json.Unmarshal(raw, &observation) != nil {
			t.Fatalf("wrong retained observation scope: %q/%q, want %q/%q: %v", gotSite, gotCell, site, cell, err)
		}
		return observation
	}
	code := read(node.SiteID, node.CellID).ProbeErrorCode
	if code != "INFERENCE_NVIDIA_UNAVAILABLE" && code != "INFERENCE_GPU_PROBE_INVALID" {
		t.Fatalf("unexpected concurrent observation: %q", code)
	}
	if _, err := db.Exec(ctx, `UPDATE inference_nodes SET site_id='legacy-site',cell_id='legacy-cell' WHERE node_id=$1`, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := write("INFERENCE_DOCKER_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	if code := read("legacy-site", "legacy-cell").ProbeErrorCode; code != "INFERENCE_DOCKER_UNAVAILABLE" {
		t.Fatalf("retained observation was not updated: %q", code)
	}
}

func TestComponentAgentExchangeCost(t *testing.T) {
	const smallFleet, largeFleet = 50, 3000
	samples := map[int]map[string]agentcost.Cost{}
	for _, fleet := range []int{smallFleet, largeFleet} {
		t.Run(fmt.Sprint(fleet), func(t *testing.T) { samples[fleet] = measureComponentExchangeCost(t, fleet) })
	}
	if t.Failed() {
		return
	}
	for _, exchange := range []struct {
		name string
		call wire.Call
	}{
		{"first component report", wire.CallHeartbeat}, {"component update", wire.CallHeartbeat}, {"foreign credential", wire.CallHeartbeat},
		{"component claim", wire.CallClaim}, {"component renew", wire.CallRenew}, {"component result", wire.CallResult},
		{"component result replay", wire.CallResult},
	} {
		a, b := samples[smallFleet][exchange.name], samples[largeFleet][exchange.name]
		t.Logf("%s: statements %d/%d rows %d/%d blocks %d/%d", exchange.name, len(a.Statements), len(b.Statements), a.Rows(), b.Rows(), a.Blocks(), b.Blocks())
		if len(a.Statements) == 0 || len(b.Statements) == 0 {
			t.Errorf("%s: missing measurements", exchange.name)
		}
		for _, violation := range agentcost.Violations(exchange.call.Cost(), a, b) {
			t.Errorf("%s: %s", exchange.name, violation)
		}
	}
}

func measureComponentExchangeCost(t *testing.T, fleet int) map[string]agentcost.Cost {
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
	now := time.Now().UTC()
	h := wire.Handshake{
		ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "component-cost-node",
			LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30,
			DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion},
	}
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
	path := "/agent/v1" + (wire.Scope{SiteID: node.SiteID, CellID: node.CellID}).Path() + "/nodes/" + node.NodeID
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
		if fleet == 50 && (name == "first component report" || name == "component update" || name == "component result") {
			for _, statement := range cost.Statements {
				first, _, _ := strings.Cut(strings.TrimSpace(statement.Query), "\n")
				t.Logf("trace %s %s", name, first)
			}
		}
	}
	h.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: now}
	client.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
	observe(t, "first component report", func() { client.Call("POST", path+"/heartbeat", h, 200, "DispositionResponse") })
	h.Component.State, h.Component.ErrorCode = "unknown", "COMPONENT_SYSTEMD_UNAVAILABLE"
	observe(t, "component update", func() { client.Call("POST", path+"/heartbeat", h, 200, "DispositionResponse") })
	client.Headers["Authorization"] = "Bearer " + security.Token()
	observe(t, "foreign credential", func() {
		if code := client.Problem("POST", path+"/heartbeat", h, 401); code != "AGENT_UNAUTHORIZED" {
			t.Fatalf("foreign credential: %s", code)
		}
	})
	client.Headers["Authorization"] = "Bearer " + node.Credential
	task, err := handler.Components.Service.Request(ctx, admin, node.NodeID, "start", uuid.NewString(), "Component cost probe", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var leased struct{ Lease *wire.Lease }
	observe(t, "component claim", func() {
		body := client.Call("POST", path+"/tasks/claims", wire.ClaimRequest{}, 200, "")
		data, err := json.Marshal(body)
		if err != nil || json.Unmarshal(data, &leased) != nil || leased.Lease == nil || leased.Lease.Component == nil || leased.Lease.TaskID != task.ID || leased.Lease.Component.Action != "start" {
			t.Fatal("expected exact component claim", err)
		}
	})
	taskPath := path + "/tasks/" + task.ID
	observe(t, "component renew", func() {
		client.Call("POST", taskPath+"/renewals", map[string]string{"lease_token": leased.Lease.LeaseToken}, 200, "")
	})
	failed := wire.Result{LeaseToken: leased.Lease.LeaseToken, Outcome: "failure", ErrorCode: h.Component.ErrorCode, Component: h.Component}
	if fleet == 50 {
		snapshot := func() string {
			t.Helper()
			var raw string
			if err := account.DB.QueryRow(ctx, `SELECT jsonb_build_array(to_jsonb(t),to_jsonb(c),
				(SELECT chain_position FROM audit_chain_head WHERE singleton))::text FROM node_tasks t
				JOIN managed_components c ON c.node_id=t.node_id WHERE t.id=$1`, task.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			return raw
		}
		before := snapshot()
		for _, fence := range []string{
			"UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1",
			"UPDATE node_tasks SET lease_token_hash=decode(repeat('fe',32),'hex') WHERE id=$1",
		} {
			handler.Nodes.Service.Components = interruptedComponentResult{handler.Components.Service, fence}
			code := client.Problem("POST", taskPath+"/results", failed, 409)
			handler.Nodes.Service.Components = handler.Components.Service
			if code != "AGENT_LEASE_STALE" || snapshot() != before {
				t.Fatalf("late owner result did not roll back: %s", code)
			}
		}
		t.Log("Post-callback deadline and credential loss rejected with unchanged task, component and audit head")
	}
	observe(t, "component result", func() { client.Call("POST", taskPath+"/results", failed, 200, "") })
	observe(t, "component result replay", func() { client.Call("POST", taskPath+"/results", failed, 200, "") })
	view, err := handler.Components.Service.Get(ctx, admin, admin.Me.Workspace.ID, node.NodeID)
	if err != nil || view.Observation == nil || view.Observation.ErrorCode != failed.ErrorCode {
		t.Fatal("component result did not persist", err)
	}
	var state string
	if err := account.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&state); err != nil || state != "failed" {
		t.Fatal("component outcome did not settle", state, err)
	}
	return measured
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
     SELECT tenant_id,workspace_id,id,'node_exporter','1.12.1','not_installed','not_installed',false,'{}',clock_timestamp(),site_id,cell_id FROM managed_nodes WHERE name LIKE 'component-cost-peer-%'`, nil},
			{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
     SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,'collect_facts.v1','succeeded',gen_random_uuid(),$1,clock_timestamp(),'{}',clock_timestamp()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'component-cost-peer-%'`, []any{actor}},
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

type interruptedComponentResult struct {
	*components.Service
	fence string
}

func (c interruptedComponentResult) RecordResultWithin(ctx context.Context, q transaction.Executor, n nodes.Node, task nodes.Task, input wire.Result) error {
	if err := c.Service.RecordResultWithin(ctx, q, n, task, input); err != nil {
		return err
	}
	_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, c.fence, task.ID))
	return err
}
