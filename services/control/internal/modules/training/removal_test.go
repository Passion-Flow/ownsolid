package training_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestTrainingRemovedNodeChallengeAuditRollbackExactReplayAndGPUFence(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	if _, e := recovery.PrepareTrainingRemoval(ctx, f.s.Identity, f.input.NodeID, uuid.NewString(), "Invalid synthetic target"); e == nil {
		t.Fatal("foreign job fenced node")
	}
	if count(t, f, "SELECT count(*) FROM managed_nodes WHERE credential_hash IS NOT NULL AND status='active'") != 1 {
		t.Fatal("invalid target revoked node")
	}
	c, e := recovery.PrepareTrainingRemoval(ctx, f.s.Identity, f.input.NodeID, j.ID, "Explicit synthetic root operator cleanup challenge")
	if e != nil {
		t.Fatal(e)
	}
	if c.Validate(time.Now()) != nil || c.Command.AttemptID != l.Training.AttemptID || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='recovery_required'") != 1 {
		t.Fatal("challenge released physical owner")
	}
	if _, e = f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString()); e == nil {
		t.Fatal("revoked node can execute")
	}
	proof := stopFixture(t, f, l)
	proof.Proof = "owned_container_removed"
	container := strings.Repeat("a", 64)
	proof.ContainerID = &container
	r := wire.RemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Challenge: c, ChallengeSHA256: c.Digest(), Stop: proof, ObservedAt: time.Now().UTC()}
	foreign := r
	foreign.Challenge.ChallengeID = uuid.NewString()
	foreign.ChallengeSHA256 = foreign.Challenge.Digest()
	if e = recovery.ReconcileTrainingRemoval(ctx, f.s.Identity, foreign, "Reject unaudited synthetic challenge"); e == nil {
		t.Fatal("unaudited challenge released GPU")
	}
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_removal() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.removal_reconciled' THEN RAISE EXCEPTION 'SYNTHETIC_REMOVAL_AUDIT_FAILURE'; END IF; RETURN NEW; END$$; CREATE TRIGGER reject_training_removal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_removal()`); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcileTrainingRemoval(ctx, f.s.Identity, r, "Actual transaction rollback with synthetic physical proof"); e == nil {
		t.Fatal("audit failure accepted cleanup")
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='recovery_required'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stopped_at IS NULL") != 1 {
		t.Fatal("audit failure released reservation")
	}
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_training_removal ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcileTrainingRemoval(ctx, f.s.Identity, r, "Explicit synthetic local root attestation"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.ReconcileTrainingRemoval(ctx, f.s.Identity, r, "Exact replay"); e != nil {
		t.Fatal(e)
	}
	changed := r
	changed.ReceiptID = uuid.NewString()
	if e = recovery.ReconcileTrainingRemoval(ctx, f.s.Identity, changed, "Changed replay"); e == nil {
		t.Fatal("changed cleanup replay accepted")
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 || count(t, f, "SELECT count(*) FROM training_jobs WHERE state='failed' AND error_code='TRAINING_OPERATOR_STOPPED'") != 1 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.removal_reconciled'") != 1 {
		t.Fatal("exact stop/audit/release cardinality")
	}
	if count(t, f, "SELECT count(*) FROM training_jobs WHERE state='succeeded' OR accepted_artifact_id IS NOT NULL") != 0 {
		t.Fatal("operator cleanup invented trained success")
	}
}
