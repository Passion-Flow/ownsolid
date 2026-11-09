package deployments_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
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
	ctx := t.Context()
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
			checkHeartbeatCost(t, &recorder, func() {
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
		checkHeartbeatCost(t, &recorder, func() {
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
			checkHeartbeatCost(t, &recorder, func() {
				cold.Call("POST", path, h, 200, "DispositionResponse")
			})
		})
		current, err = restarted.Deployments.Service.Get(ctx, admin, "", deployment.ID)
		if err != nil || current.Revision != stopping.Revision || current.ObservedState != "loading" || current.ResourceState != "held" {
			t.Fatalf("delayed report changed retained deployment: %+v, %v", current, err)
		}
	})
}

func checkHeartbeatCost(t *testing.T, recorder *agentcost.Recorder, heartbeat func()) {
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
