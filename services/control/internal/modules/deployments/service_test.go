package deployments_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// All machine observations/results here are synthetic protocol fixtures, never GPU evidence.
const gpuID = "GPU-12345678-1234-1234-1234-123456789abc"

type scenario struct {
	password string
	s        *deployments.Service
	a, p     identity.Auth
	node     wire.Enrolled
	model    string
	h        wire.Handshake
}

func setup(t *testing.T) scenario {
	t.Helper()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	p := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	w := &workspaces.Service{DB: account.DB, Identity: account}
	n := &nodes.Service{DB: account.DB, Identity: account, Workspaces: w}
	m := &models.Service{DB: account.DB, Identity: account, Workspaces: w}
	s := &deployments.Service{Nodes: n, Models: m}
	n.Inference = s
	m.Referenced = s.HasModelReferences
	h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-inference-node", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	token, e := n.MintForProfile(context.Background(), a, a.Me.Workspace.ID, "Synthetic inference", wire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := n.Enroll(context.Background(), wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	model := uuid.NewString()
	manifest, _ := json.Marshal(wire.ApprovedModel())
	if _, e = account.DB.Exec(context.Background(), "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic metadata only',$4,$5,$6,'ready',$7)", model, a.Me.Workspace.TenantID, a.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpuID, Name: "Synthetic GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, e = n.Heartbeat(context.Background(), enrolled.NodeID, enrolled.Credential, h); e != nil {
		t.Fatal(e)
	}
	return scenario{s: s, a: a, p: p, node: enrolled, model: model, h: h, password: password}
}
func check(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("wanted %s, observed %v", code, e)
	}
}
func (c scenario) input() deployments.CreateRequest {
	return deployments.CreateRequest{NodeID: c.node.NodeID, ModelID: c.model, GPUUUID: gpuID, Reason: "Synthetic deployment acceptance"}
}
func (c scenario) claim(t *testing.T) wire.Lease {
	t.Helper()
	out, e := c.s.Nodes.Claim(context.Background(), c.node.NodeID, c.node.Credential, uuid.NewString())
	if e != nil || out.Lease == nil || out.Lease.Inference == nil {
		t.Fatal("missing typed inference lease", e)
	}
	return *out.Lease
}
func result(lease wire.Lease, state string) wire.Result {
	command := lease.Inference
	d := &wire.DeploymentObservation{DeploymentID: command.DeploymentID, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, Revision: command.Revision, State: state}
	if state == "running" || state == "stopped" {
		d.Managed = true
		d.ContainerID = strings.Repeat("a", 64)
		d.ImageID = wire.ApprovedInferenceRecipe().ImageConfigDigest
	}
	if state == "running" {
		d.Ready = true
		d.Verification = &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}
	}
	return wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: d, ObservedAt: time.Now().UTC()}}
}
func (c scenario) complete(t *testing.T, lease wire.Lease, state string) {
	t.Helper()
	if out, e := c.s.Nodes.Complete(context.Background(), c.node.NodeID, c.node.Credential, lease.TaskID, result(lease, state), uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal(out, e)
	}
}
func TestInferenceExclusiveIntentFencedResultAndConfirmedRelease(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	key := uuid.NewString()
	first, e := c.s.Create(ctx, c.a, c.input(), key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := c.s.Create(ctx, c.a, c.input(), key, uuid.NewString())
	if e != nil || again.ID != first.ID {
		t.Fatal("intent replay", e)
	}
	changed := c.input()
	changed.GPUUUID = "GPU-aaaaaaaa-1234-1234-1234-123456789abc"
	_, e = c.s.Create(ctx, c.a, changed, key, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	lease := c.claim(t)
	bad := result(lease, "running")
	bad.Inference.Deployment.ModelID = uuid.NewString()
	_, e = c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, bad, uuid.NewString())
	check(t, e, "VALIDATION_FAILED")
	saved := result(lease, "running")
	for i := 0; i < 2; i++ {
		if state, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, saved, uuid.NewString()); e != nil || state != "succeeded" {
			t.Fatal("identical result replay", e)
		}
	}
	visible, e := c.s.Get(ctx, c.p, c.a.Me.Workspace.ID, first.ID)
	if e != nil || visible.Stale || visible.ResourceState != "held" || !visible.Observation.Ready {
		t.Fatal("actual persisted protocol state", e, visible)
	}
	stopped, e := c.s.Action(ctx, c.a, first.ID, deployments.ActionRequest{Action: "stop", Revision: 1, Reason: "Synthetic stop"}, uuid.NewString(), uuid.NewString())
	if e != nil || !stopped.Stale || stopped.ResourceState != "held" {
		t.Fatal("intent freed GPU or reused old readiness", e)
	}
	stop := c.claim(t)
	stale := result(stop, "stopped")
	stale.LeaseToken = lease.LeaseToken
	_, e = c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, stop.TaskID, stale, uuid.NewString())
	check(t, e, "AGENT_LEASE_STALE")
	c.complete(t, stop, "stopped")
	stopped, e = c.s.Get(ctx, c.a, "", first.ID)
	if e != nil || stopped.ResourceState != "released" {
		t.Fatal("confirmed stop did not release", e)
	}
	removed, e := c.s.Action(ctx, c.a, first.ID, deployments.ActionRequest{Action: "remove", Revision: 2, Reason: "Synthetic owned removal"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c.complete(t, c.claim(t), "not_installed")
	removed, e = c.s.Get(ctx, c.a, "", removed.ID)
	if e != nil || removed.RemovedAt == nil || removed.ResourceState != "released" {
		t.Fatal("removal not committed", e)
	}
	var ready bool
	if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT state='ready' FROM model_versions WHERE id=$1", c.model).Scan(&ready); e != nil || !ready {
		t.Fatal("shared model metadata lost", e)
	}
}
func TestInferenceConcurrentAllocationOfflineAndRestoreRetainOwnership(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else {
			check(t, e, "NODE_BUSY")
		}
	}
	if wins != 1 {
		t.Fatal("multiple allocations")
	}
	lease := c.claim(t)
	if _, e := c.s.Nodes.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID); e != nil {
		t.Fatal(e)
	}
	if e := c.s.Nodes.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e := c.s.Nodes.DB.QueryRow(ctx, "SELECT state FROM gpu_leases WHERE node_id=$1", c.node.NodeID).Scan(&state); e != nil || state != "held" {
		t.Fatal("timeout freed uncertain GPU", e, state)
	}
	_, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, result(lease, "running"), uuid.NewString())
	check(t, e, "AGENT_LEASE_STALE")
	tx, e := c.s.Nodes.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if e = deployments.InvalidateAuthorities(ctx, tx); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT state FROM gpu_leases WHERE node_id=$1", c.node.NodeID).Scan(&state); e != nil || state != "recovery_required" {
		t.Fatal("restore freed uncertain GPU", e, state)
	}
}
func TestInferenceScopeLegacyAuthorityAndAuditRollback(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	_, e := c.s.Create(ctx, c.p, c.input(), uuid.NewString(), uuid.NewString())
	check(t, e, "ACCESS_DENIED")
	other, e := c.s.Nodes.Workspaces.Create(ctx, c.a, "Other synthetic workspace", c.a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	foreign := uuid.NewString()
	manifest, _ := json.Marshal(wire.ApprovedModel())
	_, e = c.s.Nodes.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Foreign synthetic metadata',$4,$5,$6,'ready',$7)", foreign, other.TenantID, other.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, c.a.Me.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := c.input()
	input.ModelID = foreign
	_, e = c.s.Create(ctx, c.a, input, uuid.NewString(), uuid.NewString())
	check(t, e, "RESOURCE_NOT_FOUND")
	// Force the real audit insert to fail; deployment, task and resource intent must all roll back.
	if _, e = c.s.Nodes.DB.Exec(ctx, "CREATE FUNCTION reject_inference_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'inference.%' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_inference_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_inference_audit()"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("audit failure accepted")
	}
	for _, table := range []string{"deployments", "gpu_leases", "node_tasks"} {
		var count int
		if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); e != nil || count != 0 {
			t.Fatal("partial transaction", table, count, e)
		}
	}
	if _, e = c.s.Nodes.DB.Exec(ctx, "DROP TRIGGER reject_inference_audit ON audit_events; DROP FUNCTION reject_inference_audit()"); e != nil {
		t.Fatal(e)
	}
	readonly, e := c.s.Nodes.Mint(ctx, c.a, c.a.Me.Workspace.ID, "Read only", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	legacy := c.h
	legacy.ProtocolVersion = wire.Version
	legacy.ExecutionProfile = ""
	legacy.Capabilities = wire.Capabilities(wire.ReadOnlyProfile)
	legacy.Inference = nil
	node, e := c.s.Nodes.Enroll(ctx, wire.Enrollment{JoinToken: readonly.Token, Handshake: legacy}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Discover(ctx, c.a, node.NodeID, "Reject inference authority", uuid.NewString(), uuid.NewString())
	check(t, e, "AGENT_PROFILE_REJECTED")
	// The scoped composite FK independently rejects an incorrect model reference.
	_, e = c.s.Nodes.DB.Exec(ctx, "INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by) VALUES($1,$2,$3,$4,$5,$6,'running',1,$7)", uuid.NewString(), c.a.Me.Workspace.TenantID, c.a.Me.Workspace.ID, c.node.NodeID, foreign, gpuID, c.a.Me.User.ID)
	if e == nil {
		t.Fatal("cross-workspace SQL reference accepted")
	}
}

func TestExplicitOperatorFenceReceiptAndAuditRollback(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	d, e := c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	lease := c.claim(t)
	receipt := wire.InferenceRemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), NodeID: c.node.NodeID, DeploymentID: d.ID, ModelID: c.model, CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: gpuID, LocalRevision: d.Revision, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, ModelCachePresent: true, CompletedAt: time.Now().UTC().Add(-time.Minute)}
	// This synthetic receipt tests operator/database fencing only, not actual machine cleanup.
	check(t, recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, receipt, "Reject before fencing"), "RECOVERY_NODE_NOT_FENCED")
	if e = recovery.FenceInferenceNode(ctx, c.s.Nodes.Identity, c.node.NodeID, "Synthetic isolated node fence"); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, result(lease, "running"), uuid.NewString())
	check(t, e, "AGENT_UNAUTHORIZED")
	check(t, recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, receipt, "Reject receipt predating fence"), "RECOVERY_NODE_NOT_FENCED")
	receipt.CompletedAt = time.Now().UTC()
	future := receipt
	future.LocalRevision = d.Revision + 1
	check(t, recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, future, "Reject impossible future revision"), "INFERENCE_OWNERSHIP_CONFLICT")
	if _, e = c.s.Nodes.DB.Exec(ctx, "CREATE FUNCTION reject_reconcile_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='inference.removal_reconciled' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_reconcile_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_reconcile_audit()"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, receipt, "Synthetic removal rollback"); e == nil {
		t.Fatal("unaudited removal reconciled")
	}
	current, e := c.s.Get(ctx, c.a, "", d.ID)
	if e != nil || current.RemovedAt != nil || current.ResourceState != "recovery_required" {
		t.Fatal("audit failure released ownership", e)
	}
	if _, e = c.s.Nodes.DB.Exec(ctx, "DROP TRIGGER reject_reconcile_audit ON audit_events;DROP FUNCTION reject_reconcile_audit()"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, receipt, "Synthetic reviewed root receipt"); e != nil {
			t.Fatal(e)
		}
	}
	current, e = c.s.Get(ctx, c.a, "", d.ID)
	if e != nil || current.RemovedAt == nil || current.ResourceState != "released" {
		t.Fatal("fenced receipt not reconciled", e)
	}
	receipt.ReceiptID = uuid.NewString()
	check(t, recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, receipt, "Reject changed receipt replay"), "IDEMPOTENCY_CONFLICT")
}

func TestModelRetirementRejectsReferencesAndPersistsAuditedAdmissionClosure(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	d, e := c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Models.Retire(ctx, c.p, c.model, "Reject portal retirement", uuid.NewString())
	check(t, e, "ACCESS_DENIED")
	_, e = c.s.Models.Retire(ctx, c.a, c.model, "Reject referenced retirement", uuid.NewString())
	check(t, e, "MODEL_VERSION_REFERENCED")
	lease := c.claim(t)
	failed := result(lease, "running")
	failed.Outcome = "failure"
	failed.ErrorCode = "INFERENCE_GPU_OOM"
	failed.Inference.ErrorCode = failed.ErrorCode
	failed.Inference.Deployment.ErrorCode = failed.ErrorCode
	failed.Inference.Deployment.Ready = false
	if _, e = c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, failed, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	d, e = c.s.Action(ctx, c.a, d.ID, deployments.ActionRequest{Action: "remove", Revision: 1, Reason: "Remove failed synthetic deployment"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c.complete(t, c.claim(t), "not_installed")
	if _, e = c.s.Nodes.DB.Exec(ctx, "CREATE FUNCTION reject_retire_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='model.version_retired' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_retire_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_retire_audit()"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Models.Retire(ctx, c.a, c.model, "Synthetic retirement rollback", uuid.NewString()); e == nil {
		t.Fatal("retirement ignored audit failure")
	}
	if _, e = c.s.Models.Get(ctx, c.a, "", c.model); e != nil {
		t.Fatal("audit failure retired version", e)
	}
	if _, e = c.s.Nodes.DB.Exec(ctx, "DROP TRIGGER reject_retire_audit ON audit_events;DROP FUNCTION reject_retire_audit()"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		retired, e := c.s.Models.Retire(ctx, c.a, c.model, "Synthetic reviewed retirement", uuid.NewString())
		if e != nil || retired.State != "deleted" || retired.ArtifactRetention != "immutable_files_retained" {
			t.Fatal(retired, e)
		}
	}
	_, e = c.s.Models.Get(ctx, c.a, "", c.model)
	check(t, e, "RESOURCE_NOT_FOUND")
	tx, e := c.s.Nodes.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	check(t, c.s.Models.ReadyInTransaction(ctx, tx, c.a.Me.Workspace.TenantID, c.a.Me.Workspace.ID, c.model), "RESOURCE_NOT_FOUND")
}

func TestReenrollmentCannotBypassUnknownGPUUUIDReservation(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	original, e := c.s.Create(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	join, e := c.s.Nodes.MintForProfile(ctx, c.a, c.a.Me.Workspace.ID, "Synthetic re-enrolled identity", wire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrollment := c.h
	enrollment.Inference = nil
	replacement, e := c.s.Nodes.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: enrollment}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Nodes.Heartbeat(ctx, replacement.NodeID, replacement.Credential, c.h); e != nil {
		t.Fatal(e)
	}
	request := c.input()
	request.NodeID = replacement.NodeID
	_, e = c.s.Create(ctx, c.a, request, uuid.NewString(), uuid.NewString())
	check(t, e, "INFERENCE_RESOURCE_BUSY")
	old, e := c.s.Nodes.Get(ctx, c.a, "", c.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Nodes.Update(ctx, c.a, old.ID, old.Name, "removed", old.Version, "Synthetic audited identity retirement while GPU remains unknown", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	view, e := c.s.Get(ctx, c.a, "", original.ID)
	if e != nil || view.ResourceState != "recovery_required" {
		t.Fatal("node removal freed unknown GPU", e)
	}
	_, e = c.s.Create(ctx, c.a, request, uuid.NewString(), uuid.NewString())
	check(t, e, "INFERENCE_RESOURCE_BUSY")
	var count int
	if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT count(*) FROM deployments").Scan(&count); e != nil || count != 1 {
		t.Fatal("conflicting intent partially committed", e, count)
	}
	var completed time.Time
	if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&completed); e != nil {
		t.Fatal(e)
	}
	receipt := wire.InferenceRemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), NodeID: c.node.NodeID, DeploymentID: original.ID, ModelID: c.model, CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: gpuID, LocalRevision: original.Revision, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, ModelCachePresent: true, CompletedAt: completed}
	// Synthetic receipt verifies the database fence only; actual target removal remains independently required.
	if e = recovery.ReconcileInferenceRemoval(ctx, c.s.Nodes.Identity, receipt, "Synthetic exact old-identity removal attestation"); e != nil {
		t.Fatal(e)
	}
	created, e := c.s.Create(ctx, c.a, request, uuid.NewString(), uuid.NewString())
	if e != nil || created.NodeID != replacement.NodeID {
		t.Fatal("explicit reconciliation did not unblock new identity", e)
	}
	if e = c.s.Nodes.DB.QueryRow(ctx, "SELECT count(*) FROM gpu_leases WHERE gpu_uuid=$1 AND state IN ('held','recovery_required')", gpuID).Scan(&count); e != nil || count != 1 {
		t.Fatal("UUID reservation is not globally exclusive", e, count)
	}
}
