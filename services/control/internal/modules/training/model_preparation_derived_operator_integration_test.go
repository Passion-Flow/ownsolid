//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func actualDerivedOperatorReceipt(t *testing.T, g preparationScenario, catalog wire.DerivedCatalog) {
	t.Helper()
	ctx := context.Background()
	// Paired recovery deliberately retains an uncertain owner on the original node.
	// Use a separately enrolled synthetic node; never clear that owner to arrange this test.
	originalNode := g.node.NodeID
	heldBefore := count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE node_id=$1 AND state<>'released'", originalNode)
	currentNode, e := g.f.s.Nodes.Get(ctx, g.f.admin, "", originalNode)
	if e != nil {
		t.Fatal(e)
	}
	join, e := g.f.s.Nodes.MintForProfile(ctx, g.f.admin, g.input.WorkspaceID, "Independent synthetic derived operator node", agentwire.DockerModelLifecycleProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	currentNode.Facts.ObservedAt = time.Now().UTC()
	currentNode.Facts.Hostname = "synthetic-derived-operator"
	g.node, e = g.f.s.Nodes.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.ModelLifecycleVersion, ExecutionProfile: agentwire.DockerModelLifecycleProfile, Capabilities: agentwire.Capabilities(agentwire.DockerModelLifecycleProfile), Facts: currentNode.Facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.input.GPUUUID = "GPU-" + uuid.NewString()
	preparationHardware(t, g)
	d, e := g.f.s.Inference.Create(ctx, g.f.admin, deployments.CreateRequest{NodeID: g.node.NodeID, ModelID: catalog.ModelID, GPUUUID: g.input.GPUUUID, Reason: "Actual accepted files and explicit synthetic root operator attestation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Inference == nil || claim.Lease.Inference.Derived == nil {
		t.Fatal("actual derived operator command", e)
	}
	receipt := agentwire.InferenceRemovalReceipt{Derived: claim.Lease.Inference.Derived, SchemaVersion: 1, ReceiptID: uuid.NewString(), NodeID: g.node.NodeID, DeploymentID: d.ID, ModelID: catalog.ModelID, CatalogDigest: d.CatalogDigest, GPUUUID: d.GPUUUID, LocalRevision: d.Revision, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, ModelCachePresent: true, CompletedAt: time.Now().UTC()}
	if e = recovery.ReconcileInferenceRemoval(ctx, g.f.s.Identity, receipt, "Must fence before root operator receipt"); e == nil || e.Error() != "RECOVERY_NODE_NOT_FENCED" {
		t.Fatal("unfenced derived release", e)
	}
	if e = recovery.FenceInferenceNode(ctx, g.f.s.Identity, g.node.NodeID, "Isolated explicit lifecycle node fence"); e != nil {
		t.Fatal(e)
	}
	if _, e = g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString()); e == nil || e.Error() != "AGENT_UNAUTHORIZED" {
		t.Fatal("fenced lifecycle credential survived", e)
	}
	receipt.CompletedAt = time.Now().UTC()
	wrong := receipt
	wrong.Derived = nil
	wrong.CatalogDigest = agentwire.ModelCatalogDigest()
	if e = recovery.ReconcileInferenceRemoval(ctx, g.f.s.Identity, wrong, "Legacy base receipt cannot release a derived owner"); e == nil || e.Error() != "INFERENCE_OWNERSHIP_CONFLICT" {
		t.Fatal("base receipt released derivative", e)
	}
	wrong = receipt
	identity := *receipt.Derived
	identity.Catalog.Scope.WorkspaceID = uuid.NewString()
	wrong.Derived = &identity
	wrong.CatalogDigest, e = identity.Catalog.Digest()
	if e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcileInferenceRemoval(ctx, g.f.s.Identity, wrong, "Foreign scoped catalog cannot release derivative"); e == nil || e.Error() != "INFERENCE_OWNERSHIP_CONFLICT" {
		t.Fatal("foreign receipt released derivative", e)
	}
	if _, e = g.f.s.DB.Exec(ctx, "CREATE FUNCTION reject_derived_operator_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='inference.removal_reconciled' THEN RAISE EXCEPTION 'explicit synthetic audit failure'; END IF;RETURN NEW;END $$;CREATE TRIGGER reject_derived_operator_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_derived_operator_audit()"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcileInferenceRemoval(ctx, g.f.s.Identity, receipt, "Audit must commit before releasing derivative"); e == nil {
		t.Fatal("failed audit accepted")
	}
	current, e := g.f.s.Inference.Get(ctx, g.f.admin, g.input.WorkspaceID, d.ID)
	if e != nil || current.RemovedAt != nil || current.ResourceState != "recovery_required" {
		t.Fatal("failed receipt lost exact owner", e)
	}
	if _, e = g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_derived_operator_audit ON audit_events;DROP FUNCTION reject_derived_operator_audit()"); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(receipt)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "derived-removal.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := recovery.LoadInferenceReceipt(path)
	if e != nil || loaded.Derived == nil || loaded.Derived.Catalog.ModelID != catalog.ModelID {
		t.Fatal("actual private full-catalog receipt loader", e)
	}
	for i := 0; i < 2; i++ {
		if e = recovery.ReconcileInferenceRemoval(ctx, g.f.s.Identity, loaded, "Explicit synthetic positive physical attestation"); e != nil {
			t.Fatal(e)
		}
	}
	current, e = g.f.s.Inference.Get(ctx, g.f.admin, g.input.WorkspaceID, d.ID)
	if e != nil || current.RemovedAt == nil || current.ResourceState != "released" || count(t, g.f, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='inference.removal_reconciled'", d.ID) != 1 {
		t.Fatal("exact typed receipt not reconciled once", e)
	}
	loaded.ReceiptID = uuid.NewString()
	if e = recovery.ReconcileInferenceRemoval(ctx, g.f.s.Identity, loaded, "Changed receipt replay must fail"); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed receipt replay", e)
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE node_id=$1 AND state<>'released'", originalNode) != heldBefore {
		t.Fatal("independent operator fixture changed the retained recovery owner")
	}
	t.Log("Actual complete-file-derived deployment, explicit1.5 node credential fence, private full-catalog typed receipt, legacy/foreign ownership refusal, audit rollback/retry, exact one-audit replay and positive resource release. Physical root removal is an explicitly synthetic attestation, not hardware qualification.")
}
