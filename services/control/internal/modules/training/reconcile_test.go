package training_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
)

func TestActualTrainingNeverDeliveredCancellationExpiryAndActorLossReleaseOnlyExactOwner(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "epoch", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			ctx := context.Background()
			var e error
			switch mode {
			case "cancel":
				_, e = f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Cancel command before any delivery"}, uuid.NewString())
			case "deadline":
				_, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=(SELECT task_id FROM training_attempts WHERE id=$1)", *j.CurrentAttemptID)
			case "epoch":
				_, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID)
			case "disabled":
				n, err := f.s.Nodes.Get(ctx, f.admin, "", f.input.NodeID)
				if err != nil {
					t.Fatal(err)
				}
				_, e = f.s.Nodes.Update(ctx, f.admin, n.ID, n.Name, "disabled", n.Version, "Disable an undelivered synthetic target", uuid.NewString())
			}
			if e != nil {
				t.Fatal(e)
			}
			worked, e := f.s.ReconcileNext(ctx)
			if e != nil || !worked {
				t.Fatal("never-delivered reconciliation", e)
			}
			if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stop_receipt->>'proof'='control_never_delivered' AND stop_token_hash IS NULL") != 1 || count(t, f, "SELECT count(*) FROM node_tasks WHERE attempt=0 AND state IN ('failed','cancelled')") != 1 {
				t.Fatal("never-delivered proof did not release exact retained ownership")
			}
			worked, e = f.s.ReconcileNext(ctx)
			if e != nil || worked || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.undelivered_stopped'") != 1 {
				t.Fatal("reconciliation replay changed terminal state", e)
			}
		})
	}
}

func TestActualTrainingDeliveredExpiryRequiresStopAndUndeliveredAuditFailureRollsBack(t *testing.T) {
	for _, mode := range []string{"delivered", "audit"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			ctx := context.Background()
			if mode == "delivered" {
				l := claimed(t, f)
				worked, e := f.s.ReconcileNext(ctx)
				if e != nil || worked {
					t.Fatal("healthy attempt changed", e)
				}
				if _, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.TaskID); e != nil {
					t.Fatal(e)
				}
				if _, e = f.s.DB.Exec(ctx, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", j.ID); e != nil {
					t.Fatal(e)
				}
				worked, e = f.s.ReconcileNext(ctx)
				if e != nil || !worked {
					t.Fatal("expired delivered attempt not fenced", e)
				}
				if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='recovery_required'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stopped_at IS NULL AND state='recovery_required'") != 1 {
					t.Fatal("expiry invented physical absence")
				}
				if _, e = f.s.DB.Exec(ctx, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", j.ID); e != nil {
					t.Fatal(e)
				}
				worked, e = f.s.ReconcileNext(ctx)
				if e != nil || worked || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.physical_stop_required'") != 1 {
					t.Fatal("repeated uncertainty audit", e)
				}
				if _, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stopFixture(t, f, l), nil); e != nil {
					t.Fatal("exact stop after recovery fence", e)
				}
			} else {
				if _, e := f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Test never-delivered audit atomicity"}, uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_undelivered() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.undelivered_stopped' THEN RAISE EXCEPTION 'SYNTHETIC_UNDELIVERED_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_undelivered BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_undelivered()`); e != nil {
					t.Fatal(e)
				}
				if _, e := f.s.ReconcileNext(ctx); e == nil {
					t.Fatal("undelivered audit failure reported success")
				}
				if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stopped_at IS NOT NULL") != 0 {
					t.Fatal("unproved release survived failed audit")
				}
			}
		})
	}
}

func TestActualTrainingClaimAndUndeliveredCleanupRaceCannotReleaseDeliveredWork(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	ctx := context.Background()
	if _, e := f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errors := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		claim, e := f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
		if claim.Lease != nil {
			t.Error("revoked actor received work")
		}
		if e != nil && e.Error() != "ACCESS_DENIED" {
			errors <- e
		}
	}()
	go func() { defer wg.Done(); <-start; _, e := f.s.ReconcileNext(ctx); errors <- e }()
	close(start)
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE stop_token_hash IS NULL AND id=$1", *j.CurrentAttemptID) != 1 {
		t.Fatal("cleanup lost exclusive never-delivered decision")
	}
}
