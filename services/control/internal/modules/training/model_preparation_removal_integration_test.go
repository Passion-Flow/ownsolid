//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func TestActualCpuModelPreparationRemovedNodeChallengeAuditRollbackExactReplayAndGPUFence(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx := context.Background()
	if _, e := recovery.PreparePreparationRemoval(ctx, g.f.s.Identity, g.node.NodeID, uuid.NewString(), "Reject foreign synthetic target"); e == nil {
		t.Fatal("foreign target fenced node")
	}
	if count(t, g.f, "SELECT count(*) FROM managed_nodes WHERE id=$1 AND credential_hash IS NOT NULL AND status='active'", g.node.NodeID) != 1 {
		t.Fatal("invalid target revoked node")
	}
	c, e := recovery.PreparePreparationRemoval(ctx, g.f.s.Identity, g.node.NodeID, p.ID, "Explicit local operator challenge with synthetic physical observation")
	if e != nil {
		t.Fatal(e)
	}
	if c.Validate(time.Now()) != nil || c.Command.AttemptID != l.Preparation.AttemptID || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'", p.ID) != 1 {
		t.Fatal("challenge released physical owner")
	}
	if _, e = g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString()); e == nil {
		t.Fatal("revoked node executes")
	}
	stop := preparationStopReceipt(t, g, l)
	stop.Proof = "owned_container_removed"
	cid := strings.Repeat("a", 64)
	stop.ContainerID = &cid
	r := wire.RemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Challenge: c, ChallengeSHA256: c.Digest(), Stop: stop, ObservedAt: time.Now().UTC()}
	foreign := r
	foreign.Challenge.ChallengeID = uuid.NewString()
	foreign.ChallengeSHA256 = foreign.Challenge.Digest()
	if e = recovery.ReconcilePreparationRemoval(ctx, g.f.s.Identity, foreign, "Refuse unaudited challenge"); e == nil {
		t.Fatal("unaudited proof released GPU")
	}
	if _, e = g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_removal() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_removal_reconciled' THEN RAISE EXCEPTION 'SYNTHETIC_REMOVAL_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_removal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_removal()`); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcilePreparationRemoval(ctx, g.f.s.Identity, r, "Actual atomic transaction with synthetic physical receipt"); e == nil {
		t.Fatal("audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL", *p.CurrentAttemptID) != 1 {
		t.Fatal("audit failure partially released owner")
	}
	if _, e = g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_removal ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcilePreparationRemoval(ctx, g.f.s.Identity, r, "Explicit isolated local root attestation"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcilePreparationRemoval(ctx, g.f.s.Identity, r, "Exact immutable replay"); e != nil {
		t.Fatal(e)
	}
	changed := r
	changed.ReceiptID = uuid.NewString()
	if e = recovery.ReconcilePreparationRemoval(ctx, g.f.s.Identity, changed, "Changed replay refused"); e == nil {
		t.Fatal("changed receipt accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='failed' AND error_code='MODEL_PREPARATION_OPERATOR_STOPPED'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_removal_reconciled'") != 1 {
		t.Fatal("release/result/audit cardinality")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_artifacts WHERE preparation_id=$1", p.ID) != 0 {
		t.Fatal("operator cleanup manufactured accepted result")
	}
}
