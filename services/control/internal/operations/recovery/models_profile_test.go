package recovery_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM06RestoreIncludesImportsAndUncertainResourcesAtomically(t *testing.T) {
	s := fixture.NewM06(t)
	password := fixture.Bootstrap(t, s)
	ctx := t.Context()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	w := &workspaces.Service{DB: s.DB, Identity: s}
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	m := &models.Service{DB: s.DB, Identity: s, Workspaces: w, RootDirectory: s.Config.Models.ArtifactRoot}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	job, e := m.StartImport(ctx, a, models.ImportRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Synthetic pending restore metadata", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: wire.ModelBytes() + 20000, LicenseAcknowledged: true, Reason: "Isolated restore boundary"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	// This SQL-only restore fixture represents a queued snapshot, never verified model bytes.
	if _, e = s.DB.Exec(ctx, "UPDATE model_imports SET state='queued',uploaded_bytes=expected_bytes WHERE id=$1", job.ID); e != nil {
		t.Fatal(e)
	}
	model := uuid.NewString()
	catalog, _ := json.Marshal(wire.ApprovedModel())
	if _, e = s.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic restore metadata',$4,$5,$6,'ready',$7)", model, a.Me.Workspace.TenantID, a.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), catalog, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	join, e := n.MintForProfile(ctx, a, a.Me.Workspace.ID, "Synthetic restore owner", wire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	gpu := "GPU-12345678-1234-1234-1234-123456789abc"
	h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-restore", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion}}
	enrolled, e := n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic restore GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
	if _, e = n.Heartbeat(ctx, enrolled.NodeID, enrolled.Credential, h); e != nil {
		t.Fatal(e)
	}
	deployment, e := d.Create(ctx, a, deployments.CreateRequest{NodeID: enrolled.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Synthetic retained physical owner"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_model_restore() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic restore audit boundary';END$$;CREATE TRIGGER reject_model_restore BEFORE UPDATE ON model_imports FOR EACH ROW EXECUTE FUNCTION reject_model_restore()`); e != nil {
		t.Fatal(e)
	}
	if recovery.InvalidateRestored(ctx, s, "Synthetic interrupted owner transaction") == nil {
		t.Fatal("partial restore accepted")
	}
	var importState, leaseState, taskState string
	var live bool
	read := func() {
		t.Helper()
		if e = s.DB.QueryRow(ctx, "SELECT m.state,g.state,t.state,n.credential_hash IS NOT NULL FROM model_imports m JOIN gpu_leases g ON g.deployment_id=$2 JOIN deployments d ON d.id=g.deployment_id JOIN node_tasks t ON t.id=d.last_task_id JOIN managed_nodes n ON n.id=d.node_id WHERE m.id=$1", job.ID, deployment.ID).Scan(&importState, &leaseState, &taskState, &live); e != nil {
			t.Fatal(e)
		}
	}
	read()
	if importState != "queued" || leaseState != "held" || taskState != "pending" || !live {
		t.Fatal("restore failed to roll back all owners")
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_model_restore ON model_imports;DROP FUNCTION reject_model_restore()"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, s, "Synthetic approved restored authority invalidation"); e != nil {
		t.Fatal(e)
	}
	read()
	if importState != "failed" || leaseState != "recovery_required" || taskState != "cancelled" || live {
		t.Fatal("restored authority or GPU release")
	}
	var code, observed string
	if e = s.DB.QueryRow(ctx, "SELECT m.error_code,d.observed_state FROM model_imports m JOIN deployments d ON d.id=$2 WHERE m.id=$1", job.ID, deployment.ID).Scan(&code, &observed); e != nil || code != "MODEL_ACTOR_UNAVAILABLE" || observed != "unknown" {
		t.Fatal(code, observed, e)
	}
}
