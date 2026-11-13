package nodes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func measureRetainedInferenceHeartbeats(t *testing.T, f *observationCostFixture, h *wire.Handshake, peers int, measured map[string]agentcost.Cost) {
	t.Helper()
	ctx := t.Context()
	model := uuid.NewString()
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.account.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
		VALUES($1,$2,$3,'Synthetic cost metadata',$4,$5,$6::jsonb,'ready',$7)`, model,
		f.admin.Me.Workspace.TenantID, f.admin.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, f.admin.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	// These protocol observations establish no physical GPU or model execution.
	gpu := "GPU-" + uuid.NewString()
	h.Inference = &wire.InferenceObservation{ObservedAt: h.Facts.ObservedAt, GPUs: []wire.NVIDIAGPU{{
		UUID: gpu, Name: "Synthetic cost GPU", DriverVersion: "580.1", ComputeCapability: 8,
		MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, MIGMode: "Disabled", ObservedAt: h.Facts.ObservedAt,
	}}}
	f.client.Headers = map[string]string{"Authorization": "Bearer " + f.node.Credential}
	f.client.Call("POST", f.path, h, 200, "DispositionResponse")
	deployment, err := f.handler.Deployments.Service.Create(ctx, f.admin, deployments.CreateRequest{
		NodeID: f.node.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Retain the queued deployment across a dispatcher restart",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal("create retained deployment", err)
	}
	seedRetainedInferenceCostHistory(t, f, deployment.ID, peers)
	f.restartDispatcher(t)
	f.client.Headers = map[string]string{"Authorization": "Bearer " + f.node.Credential}
	h.Inference.Deployment = &wire.DeploymentObservation{
		DeploymentID: deployment.ID, ModelID: model, GPUUUID: gpu, CatalogDigest: wire.ModelCatalogDigest(),
		Revision: deployment.Revision, State: "loading",
	}
	var reply map[string]any
	measured["retained deployment cold report"] = f.record(t, func() {
		reply = f.client.Call("POST", f.path, h, 200, "DispositionResponse")
	})
	assertInferenceHeartbeat(t, reply, *h, readInferenceHeartbeat(t, f.account.DB, f.node.NodeID))
	current, err := f.handler.Deployments.Service.Get(ctx, f.admin, "", deployment.ID)
	if err != nil || current.ObservedState != "loading" || current.ResourceState != "held" || !reflect.DeepEqual(current.Observation, h.Inference.Deployment) {
		t.Fatal("cold report lost retained deployment ownership", err)
	}
	stopping, err := f.handler.Deployments.Service.Action(ctx, f.admin, deployment.ID, deployments.ActionRequest{
		Action: "stop", Revision: current.Revision, Reason: "Advance the desired revision before a delayed report",
	}, uuid.NewString(), uuid.NewString())
	if err != nil || stopping.Revision != current.Revision+1 {
		t.Fatal("advance retained desired revision", err)
	}
	before := retainedInferenceCostState(t, f, deployment.ID)
	h.Inference.Deployment.State = "failed"
	measured["stale deployment revision"] = f.record(t, func() {
		reply = f.client.Call("POST", f.path, h, 200, "DispositionResponse")
	})
	assertInferenceHeartbeat(t, reply, *h, readInferenceHeartbeat(t, f.account.DB, f.node.NodeID))
	if after := retainedInferenceCostState(t, f, deployment.ID); after != before {
		t.Fatal("stale heartbeat changed the retained deployment or GPU reservation")
	}
	current, err = f.handler.Deployments.Service.Get(ctx, f.admin, "", deployment.ID)
	if err != nil || current.Revision != stopping.Revision || current.ObservedState != "loading" || current.ResourceState != "held" {
		t.Fatal("stale report changed current ownership", err)
	}
}

func seedRetainedInferenceCostHistory(t *testing.T, f *observationCostFixture, deployment string, peers int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	err := pgx.BeginFunc(ctx, f.account.DB, func(tx pgx.Tx) error {
		for _, seed := range []struct {
			sql  string
			args []any
		}{
			{"SET LOCAL statement_timeout='90s'", nil},
			{"SET LOCAL auto_explain.log_min_duration=-1", nil},
			{`INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,model_kind,catalog_digest,gpu_uuid,desired_state,revision,created_by,site_id,cell_id)
			SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,d.model_id,d.model_kind,d.catalog_digest,
			'GPU-'||gen_random_uuid()::text,'running',1,d.created_by,n.site_id,n.cell_id
			FROM managed_nodes n JOIN deployments d ON d.id=$1 WHERE n.name LIKE 'observation-cost-peer-%'`, []any{deployment}},
			{`INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state,site_id,cell_id)
			SELECT d.id,d.tenant_id,d.workspace_id,d.node_id,d.gpu_uuid,d.id,d.revision,'held',d.site_id,d.cell_id
			FROM deployments d JOIN managed_nodes n ON n.id=d.node_id WHERE n.name LIKE 'observation-cost-peer-%'`, nil},
		} {
			if _, err := tx.Exec(ctx, seed.sql, seed.args...); err != nil {
				return err
			}
		}
		var retained, leases, gpus int
		if err := tx.QueryRow(ctx, `SELECT count(*),count(l.deployment_id),count(DISTINCT l.gpu_uuid)
			FROM deployments d JOIN managed_nodes n ON n.id=d.node_id
			LEFT JOIN gpu_leases l ON l.deployment_id=d.id AND l.state='held'
			WHERE n.name LIKE 'observation-cost-peer-%'`).Scan(&retained, &leases, &gpus); err != nil {
			return err
		}
		if retained != peers || leases != peers || gpus != peers {
			return fmt.Errorf("retained peer history mismatch: deployments=%d held_leases=%d distinct_gpus=%d want=%d", retained, leases, gpus, peers)
		}
		t.Logf("seeded retained deployments=%d held_leases=%d distinct_gpus=%d", retained, leases, gpus)
		_, err := tx.Exec(ctx, "ANALYZE deployments, gpu_leases")
		return err
	})
	if err != nil {
		t.Fatal("seed retained inference history", err)
	}
}

func retainedInferenceCostState(t *testing.T, f *observationCostFixture, deployment string) string {
	t.Helper()
	var state string
	if err := f.account.DB.QueryRow(t.Context(), `SELECT jsonb_build_object('deployment',to_jsonb(d),
		'leases',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id) FROM gpu_leases l WHERE l.deployment_id=d.id))::text
		FROM deployments d WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.node_id=$3 AND d.id=$4`,
		f.admin.Me.Workspace.TenantID, f.admin.Me.Workspace.ID, f.node.NodeID, deployment).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
