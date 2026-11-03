package training_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func dispatched(t *testing.T, f scenario) training.Job {
	t.Helper()
	hardware(t, f)
	j := queued(t, f)
	if worked, e := f.s.DispatchNext(context.Background()); e != nil || !worked {
		current, readError := f.s.Get(context.Background(), f.portal, f.input.WorkspaceID, j.ID)
		t.Fatal("dispatch fixture", "worked", worked, "error", e, "state", current.State, "wait_code", current.WaitCode, "read_error", readError)
	}
	j, e := f.s.Get(context.Background(), f.portal, f.input.WorkspaceID, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func claimed(t *testing.T, f scenario) agentwire.Lease {
	t.Helper()
	claim, e := f.s.Nodes.Claim(context.Background(), f.input.NodeID, f.credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Training == nil {
		t.Fatal("actual delegated claim", e)
	}
	return *claim.Lease
}

func TestActualTrainingNodeClaimRenewExpiryAndGenericLeaseBypassRefusal(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	ctx := context.Background()
	_, e := f.s.Nodes.Claim(ctx, f.input.NodeID, security.Token(), uuid.NewString())
	expected(t, e, "AGENT_UNAUTHORIZED")
	l := claimed(t, f)
	if l.Attempt != 1 || l.Training.Scope.JobID != j.ID || l.Training.AttemptID != *j.CurrentAttemptID {
		t.Fatal("claim binding")
	}
	again, e := f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
	if e != nil || again.Lease != nil {
		t.Fatal("live attempt delivered twice", e)
	}
	renewed, e := f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken)
	if e != nil || renewed.ValidForMilliseconds < 1 || renewed.LeaseExpiresAt.Before(l.LeaseExpiresAt) {
		t.Fatal("actual renewal", e)
	}
	e = f.s.Nodes.WithLease(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, func(pgx.Tx, nodes.Node, nodes.Task) error { t.Fatal("unguarded training lease effect"); return nil })
	expected(t, e, "TRAINING_AUTHORITY_REQUIRED")
	if _, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.TaskID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken)
	expected(t, e, "AGENT_LEASE_STALE")
	again, e = f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
	if e != nil || again.Lease != nil {
		t.Fatal("expired physical attempt silently reissued", e)
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 || count(t, f, "SELECT attempt FROM node_tasks WHERE id=$1", l.TaskID) != 1 {
		t.Fatal("lease loss duplicated or released physical ownership")
	}
}

func TestActualTrainingCurrentDelegationAndCancellationStopRenewal(t *testing.T) {
	for _, mode := range []string{"epoch", "membership", "role", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			l := claimed(t, f)
			ctx := context.Background()
			var e error
			switch mode {
			case "epoch":
				_, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID)
			case "membership":
				_, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET authority_id=$3 WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID, uuid.NewString())
			case "role":
				_, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID)
			case "cancel":
				_, e = f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Stop authority for actual active attempt"}, uuid.NewString())
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken)
			expected(t, e, "ACCESS_DENIED")
			if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state IN ('held','recovery_required')") != 1 {
				t.Fatal("revocation freed unproven physical GPU")
			}
		})
	}
}

func TestActualTrainingClaimCancellationRaceAndTamperedCommandRollback(t *testing.T) {
	for _, mode := range []string{"cancel_race", "tampered_command"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			ctx := context.Background()
			if mode == "cancel_race" {
				var wg sync.WaitGroup
				start := make(chan struct{})
				var claim agentwire.Claim
				var claimError, cancelError error
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					claim, claimError = f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
				}()
				go func() {
					defer wg.Done()
					<-start
					_, cancelError = f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Concurrent cancellation and node claim"}, uuid.NewString())
				}()
				close(start)
				wg.Wait()
				if cancelError != nil || claimError != nil && claimError.Error() != "ACCESS_DENIED" {
					t.Fatal(claimError, cancelError)
				}
				if claim.Lease != nil {
					_, e := f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, claim.Lease.TaskID, claim.Lease.LeaseToken)
					expected(t, e, "ACCESS_DENIED")
				}
				if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 {
					t.Fatal("cancel/claim race released physical owner")
				}
			} else {
				var task string
				var raw []byte
				if e := f.s.DB.QueryRow(ctx, "SELECT t.id::text,t.payload FROM node_tasks t JOIN training_attempts a ON a.task_id=t.id WHERE a.job_id=$1", j.ID).Scan(&task, &raw); e != nil {
					t.Fatal(e)
				}
				command, e := wire.DecodeCommand(raw)
				if e != nil {
					t.Fatal(e)
				}
				command.DatasetID = uuid.NewString()
				raw, _ = json.Marshal(command)
				if _, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET payload=$2 WHERE id=$1", task, raw); e != nil {
					t.Fatal(e)
				}
				_, e = f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
				expected(t, e, "TRAINING_ATTEMPT_STALE")
				if count(t, f, "SELECT attempt FROM node_tasks WHERE id=$1", task) != 0 {
					t.Fatal("rejected command retained a claim lease")
				}
			}
		})
	}
}

func TestActualTrainingNodeDisableAndRestoreKeepUncertainReservations(t *testing.T) {
	for _, mode := range []string{"disabled", "restore", "restore_audit_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			l := claimed(t, f)
			ctx := context.Background()
			var e error
			if mode == "disabled" {
				n, e := f.s.Nodes.Get(ctx, f.admin, "", f.input.NodeID)
				if e != nil {
					t.Fatal(e)
				}
				_, e = f.s.Nodes.Update(ctx, f.admin, n.ID, n.Name, "disabled", n.Version, "Disable synthetic owned training node", uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
			} else {
				if mode == "restore_audit_failure" {
					if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_restore() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.error_code='TRAINING_RESTORE_INVALIDATED' THEN RAISE EXCEPTION 'SYNTHETIC_TRAINING_RESTORE_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_restore BEFORE UPDATE ON training_jobs FOR EACH ROW EXECUTE FUNCTION reject_training_restore()`); e != nil {
						t.Fatal(e)
					}
				}
				e = recovery.InvalidateRestored(ctx, f.s.Identity, "Synthetic training restoration acceptance")
				if mode == "restore_audit_failure" {
					if e == nil {
						t.Fatal("restore failure reported success")
					}
					if _, e = f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken); e != nil {
						t.Fatal("partial authority invalidation survived rollback", e)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			if count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='recovery_required'", j.ID) != 1 || count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='recovery_required'", j.ID) != 1 {
				t.Fatal("uncertain physical ownership not fenced")
			}
			if _, e = f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken); e == nil {
				t.Fatal("disabled/restored node renewed training")
			}
		})
	}
}

func TestActualTLSMachineTrainingClaimAndRenewMatchesGeneratedSchema(t *testing.T) {
	f := setup(t)
	dispatched(t, f)
	handler := transport.New(f.s.Identity)
	// Reuse the actual already-exclusive dataset store; this fixture owns its eventual close.
	handler.Datasets.Service.Store = f.s.Datasets.Store
	handler.Datasets.Service.Processor = f.s.Datasets.Processor
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	machine := httpfixture.New(t, server, "admin", schemas)
	machine.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	base := "/agent/v1/nodes/" + f.input.NodeID
	result := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")
	lease := result["lease"].(map[string]any)
	if lease["training"] == nil {
		t.Fatal("typed training command absent")
	}
	machine.Call("POST", base+"/tasks/"+lease["task_id"].(string)+"/renewals", map[string]any{"lease_token": lease["lease_token"]}, 200, "RenewedLeaseResponse")
}
