package distribution_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strings"
	"testing"
	"time"
)

// Go-only acceptance uses explicit synthetic operator/machine evidence; native fence CLI is tested independently.
func (c scenario) fenceFixture(access access, challenge string) wire.FenceReceipt {
	digest := sha256.Sum256([]byte(c.s.Identity.Config.Server.AdminOrigin))
	now := uint64(time.Now().UnixMilli())
	return wire.FenceReceipt{SchemaVersion: 1, GatewayID: access.gateway.Gateway.ID, NodeID: c.node.NodeID, ControlOriginSHA256: hex.EncodeToString(digest[:]), ChallengeID: challenge, StateFenceSHA256: strings.Repeat("a", 64), FencedAtUnixMilliseconds: now, CheckedAtUnixMilliseconds: now, ExclusiveStateLockConfirmed: true, PersistentFenceConfirmed: true, GatewayVersion: "0.7.0"}
}
func (c scenario) stopForRecovery(t *testing.T, alias distribution.Alias, revision int64) int64 {
	t.Helper()
	ctx := context.Background()
	stopped, e := c.s.Deployments.Action(ctx, c.a, alias.DeploymentID, deployments.ActionRequest{Action: "stop", Revision: revision, Reason: "Synthetic recovery backend stop"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := c.s.Nodes.Claim(ctx, c.node.NodeID, c.node.Credential, uuid.NewString())
	if e != nil || claimed.Lease == nil || claimed.Lease.Inference == nil {
		t.Fatal("typed stop lease", e)
	}
	lease := claimed.Lease
	command := lease.Inference
	observation := &agentwire.DeploymentObservation{DeploymentID: command.DeploymentID, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, Revision: command.Revision, State: "stopped", Managed: true, ContainerID: strings.Repeat("a", 64), ImageID: agentwire.ApprovedInferenceRecipe().ImageConfigDigest}
	result := agentwire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{}, Deployment: observation, ObservedAt: time.Now().UTC()}}
	if state, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, result, uuid.NewString()); e != nil || state != "succeeded" {
		t.Fatal("synthetic stopped evidence", e)
	}
	return stopped.Revision
}

func TestOrderedRecoveryRequiresRevocationFenceAndNewBackendStopAndRetainsLateUsage(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	input := access.input()
	if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "Synthetic recovery", uuid.NewString(), uuid.NewString())
	check(t, e, "GATEWAY_RECOVERY_REQUIRES_REVOCATION")
	if _, e = c.s.RevokeGateway(ctx, c.a, access.alias.WorkspaceID, access.gateway.Gateway.ID, "Synthetic recovery fencing", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	intent := uuid.NewString()
	challenge, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "Synthetic recovery", intent, uuid.NewString())
	if e != nil || challenge.State != "pending_fence" {
		t.Fatal("scoped challenge", e)
	}
	replay, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "Synthetic recovery", intent, uuid.NewString())
	if e != nil || replay.ID != challenge.ID {
		t.Fatal("challenge intent replay", e)
	}
	_, e = c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "Changed reason", intent, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = c.s.ConfirmRecovery(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, 1, "Synthetic confirm", uuid.NewString())
	check(t, e, "GATEWAY_RECOVERY_STATE_CONFLICT")
	// A stop before the gateway fence was accepted is deliberately insufficient.
	revision := c.stopForRecovery(t, access.alias, access.alias.DeploymentRevision)
	receipt := c.fenceFixture(access, challenge.ID)
	wrong := receipt
	wrong.NodeID = uuid.NewString()
	_, e = c.s.AcceptRecoveryFence(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, wrong, "Synthetic wrong scope", uuid.NewString())
	check(t, e, "GATEWAY_FENCE_SCOPE_CONFLICT")
	accepted, e := c.s.AcceptRecoveryFence(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, receipt, "Synthetic actual fence attestation", uuid.NewString())
	if e != nil || accepted.State != "fenced" || accepted.FenceAcceptedAt == nil {
		t.Fatal("persisted fence acceptance", e)
	}
	changed := receipt
	changed.StateFenceSHA256 = strings.Repeat("b", 64)
	_, e = c.s.AcceptRecoveryFence(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, changed, "Synthetic changed receipt", uuid.NewString())
	check(t, e, "GATEWAY_FENCE_RECEIPT_CONFLICT")
	_, e = c.s.ConfirmRecovery(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, revision, "Synthetic confirm", uuid.NewString())
	check(t, e, "GATEWAY_BACKEND_STOP_UNCONFIRMED")
	revision = c.stopForRecovery(t, access.alias, revision)
	confirmed, e := c.s.ConfirmRecovery(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, revision, "Synthetic confirm", uuid.NewString())
	if e != nil || confirmed.State != "completed" || confirmed.ConfirmedAt == nil {
		t.Fatal("reviewed reconciliation", e)
	}
	if _, e = c.s.ConfirmRecovery(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, revision, "Synthetic confirm", uuid.NewString()); e != nil {
		t.Fatal("confirm replay", e)
	}
	_, e = c.s.ConfirmRecovery(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, revision, "Changed confirmation", uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	var held, unknown bool
	if e = c.s.DB.QueryRow(ctx, "SELECT capacity_held,outcome IS NULL FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&held, &unknown); e != nil || held || !unknown {
		t.Fatal("recovery invented usage or retained capacity", e)
	}
	if _, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal("late first actual receipt", e)
	}
	var reconciled, known bool
	if e = c.s.DB.QueryRow(ctx, "SELECT capacity_held,reconciled_at IS NOT NULL,(outcome->'usage'->>'total_tokens')::integer=9 FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&held, &reconciled, &known); e != nil || held || !reconciled || !known {
		t.Fatal("late receipt lost reconciliation provenance", e)
	}
}

func TestRecoveryChallengeSupersessionExpiryAndAdminBoundaries(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	input := access.input()
	if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e := c.s.CreateRecoveryChallenge(ctx, access.owner, access.alias.WorkspaceID, input.RequestID, "Synthetic forbidden", uuid.NewString(), uuid.NewString())
	check(t, e, "ACCESS_DENIED")
	if _, e = c.s.RevokeGateway(ctx, c.a, access.alias.WorkspaceID, access.gateway.Gateway.ID, "Synthetic revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	first, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "First recovery intent", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	second, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "New recovery intent", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	previous, e := c.s.RecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, first.ID)
	if e != nil || previous.State != "superseded" {
		t.Fatal("old challenge history lost", e)
	}
	_, e = c.s.AcceptRecoveryFence(ctx, c.a, access.alias.WorkspaceID, input.RequestID, first.ID, c.fenceFixture(access, first.ID), "Synthetic stale", uuid.NewString())
	check(t, e, "GATEWAY_RECOVERY_STATE_CONFLICT")
	if _, e = c.s.DB.Exec(ctx, "UPDATE gateway_recoveries SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", second.ID); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.AcceptRecoveryFence(ctx, c.a, access.alias.WorkspaceID, input.RequestID, second.ID, c.fenceFixture(access, second.ID), "Synthetic expired", uuid.NewString())
	check(t, e, "GATEWAY_RECOVERY_STATE_CONFLICT")
	var held bool
	if e = c.s.DB.QueryRow(ctx, "SELECT capacity_held FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&held); e != nil || !held {
		t.Fatal("expired challenge freed capacity", e)
	}
}

func TestRestoredGatewayRecoveryNeedsNewFenceAndReviewedNodeRemovalEvidence(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	input := access.input()
	if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.s.RevokeGateway(ctx, c.a, access.alias.WorkspaceID, access.gateway.Gateway.ID, "Synthetic revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	old, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "Before restore", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, c.s.Identity, "Synthetic operator restore"); e != nil {
		t.Fatal(e)
	}
	c.a = fixture.Login(t, c.s.Identity, "admin@example.invalid", c.password, "admin")
	invalidated, e := c.s.RecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, old.ID)
	if e != nil || invalidated.State != "invalidated" {
		t.Fatal("restored recovery nonce remained valid", e)
	}
	challenge, e := c.s.CreateRecoveryChallenge(ctx, c.a, access.alias.WorkspaceID, input.RequestID, "After restore", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.AcceptRecoveryFence(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, c.fenceFixture(access, challenge.ID), "Synthetic fence attestation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var model, gpu string
	if e = c.s.DB.QueryRow(ctx, "SELECT model_id::text,gpu_uuid FROM deployments WHERE id=$1", access.alias.DeploymentID).Scan(&model, &gpu); e != nil {
		t.Fatal(e)
	}
	receipt := agentwire.InferenceRemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), NodeID: c.node.NodeID, DeploymentID: access.alias.DeploymentID, ModelID: model, CatalogDigest: agentwire.ModelCatalogDigest(), GPUUUID: gpu, LocalRevision: access.alias.DeploymentRevision, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, ModelCachePresent: true, CompletedAt: time.Now().UTC()}
	if e = recovery.ReconcileInferenceRemoval(ctx, c.s.Identity, receipt, "Synthetic reviewed root cleanup receipt"); e != nil {
		t.Fatal(e)
	}
	result, e := c.s.ConfirmRecovery(ctx, c.a, access.alias.WorkspaceID, input.RequestID, challenge.ID, access.alias.DeploymentRevision, "Synthetic restored reconciliation", uuid.NewString())
	if e != nil || result.State != "completed" {
		t.Fatal("restored independent recovery", e)
	}
	var held, unknown bool
	if e = c.s.DB.QueryRow(ctx, "SELECT capacity_held,outcome IS NULL FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&held, &unknown); e != nil || held || !unknown {
		t.Fatal("restored recovery fabricated usage", e)
	}
}
