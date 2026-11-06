package training_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// These are explicit synthetic node/container attestations for actual PostgreSQL lifecycle/fence tests, never Docker/GPU evidence.
func stopFixture(t *testing.T, f scenario, l agentwire.Lease) wire.StopReceipt {
	t.Helper()
	sha, e := l.Training.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: f.input.NodeID, TaskID: l.TaskID, Scope: l.Training.Scope, AttemptID: l.Training.AttemptID, AttemptNumber: l.Training.AttemptNumber, CommandSHA256: sha, ImageID: *l.Training.Recipe.Identity.ImageID, GPUUUID: l.Training.GPUUUID, ContainerName: wire.ContainerName(l.Training.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, CompletedAt: time.Now().UTC()}
}

func TestTrainingAgentFailureDiagnosticPersistsWithoutWorkerResultOrSuccess(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	r := stopFixture(t, f, l)
	r.AgentErrorCode = "TRAINING_PROCESSOR_MEMORY_EXCEEDED"
	accepted, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, r, nil)
	if e != nil || accepted.State != "failed" || !accepted.GPUReleased {
		t.Fatal(e)
	}
	var code string
	if e = f.s.DB.QueryRow(ctx, "SELECT error_code FROM training_jobs WHERE id=$1", j.ID).Scan(&code); e != nil || code != r.AgentErrorCode {
		t.Fatal("exact safe agent cause lost", e, code)
	}
	if count(t, f, "SELECT count(*) FROM training_artifacts") != 0 || count(t, f, "SELECT count(*) FROM training_jobs WHERE state='succeeded'") != 0 {
		t.Fatal("OOM invented model success")
	}
}

func TestActualTrainingRestrictedStopAfterExpiryRevocationCancellationAndNodeDisable(t *testing.T) {
	for _, mode := range []string{"expired", "epoch", "membership", "cancel", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			l := claimed(t, f)
			ctx := context.Background()
			receipt := stopFixture(t, f, l)
			var e error
			switch mode {
			case "expired":
				_, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.TaskID)
			case "epoch":
				_, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID)
			case "membership":
				_, e = f.s.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID)
			case "cancel":
				_, e = f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Cancel before exact physical stop report"}, uuid.NewString())
			case "disabled":
				n, err := f.s.Nodes.Get(ctx, f.admin, "", f.input.NodeID)
				if err != nil {
					t.Fatal(err)
				}
				_, e = f.s.Nodes.Update(ctx, f.admin, n.ID, n.Name, "disabled", n.Version, "Disable before restricted physical stop", uuid.NewString())
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.s.RecordStop(ctx, f.input.NodeID, security.Token(), l.TaskID, l.LeaseToken, receipt, nil)
			expected(t, e, "AGENT_UNAUTHORIZED")
			_, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, security.Token(), receipt, nil)
			expected(t, e, "TRAINING_STOP_INVALID")
			first, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, receipt, nil)
			if e != nil || !first.GPUReleased || first.State == "succeeded" || first.Duplicate {
				t.Fatal("restricted exact stop failed", e)
			}
			if (mode == "epoch" || mode == "membership" || mode == "cancel") && first.State != "cancelled" {
				t.Fatal("lost authority did not win over success")
			}
			again, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, receipt, nil)
			if e != nil || !again.Duplicate {
				t.Fatal("restricted receipt replay", e)
			}
			if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stopped_at IS NOT NULL") != 1 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.physical_stop_accepted'") != 1 {
				t.Fatal("physical stop release/audit not exactly once")
			}
			changed := receipt
			changed.ReceiptID = uuid.NewString()
			_, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, changed, nil)
			expected(t, e, "TRAINING_STOP_CONFLICT")
		})
	}
}

func TestActualTrainingStopScopeStaleFenceAndAtomicAuditRollback(t *testing.T) {
	f := setup(t)
	dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	receipt := stopFixture(t, f, l)
	for _, change := range []func(*wire.StopReceipt){func(r *wire.StopReceipt) { r.Scope.WorkspaceID = uuid.NewString() }, func(r *wire.StopReceipt) { r.CommandSHA256 = strings.Repeat("a", 64) }, func(r *wire.StopReceipt) { r.ContainerName = "unrelated-container" }, func(r *wire.StopReceipt) { r.Proof = "owned_container_removed" }, func(r *wire.StopReceipt) { r.OwnedContainerAbsent = false }, func(r *wire.StopReceipt) { r.AttemptNumber++ }} {
		bad := receipt
		change(&bad)
		_, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, bad, nil)
		expected(t, e, "TRAINING_STOP_INVALID")
	}
	if _, e := f.s.DB.Exec(ctx, "UPDATE gpu_leases SET revision=revision+1 WHERE training_job_id=$1", l.Training.Scope.JobID); e != nil {
		t.Fatal(e)
	}
	_, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, receipt, nil)
	expected(t, e, "TRAINING_ATTEMPT_STALE")
	if _, e = f.s.DB.Exec(ctx, "UPDATE gpu_leases SET revision=revision-1 WHERE training_job_id=$1", l.Training.Scope.JobID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_stop() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.physical_stop_accepted' THEN RAISE EXCEPTION 'SYNTHETIC_STOP_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_stop BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_stop()`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, receipt, nil); e == nil {
		t.Fatal("stop audit failure reported success")
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stopped_at IS NOT NULL") != 0 || count(t, f, "SELECT count(*) FROM node_tasks WHERE state='running'") != 1 {
		t.Fatal("failed stop audit changed physical/business state")
	}
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_training_stop ON audit_events"); e != nil {
		t.Fatal(e)
	}
	result, _ := json.Marshal(map[string]any{"schema_version": 1, "worker_version": wire.WorkerVersion, "status": "failed", "job_id": l.Training.Scope.JobID, "attempt_id": l.Training.AttemptID, "error_code": "TRAINING_DEVICE_UNAVAILABLE"})
	sha := digest(result)
	receipt.OutcomeSHA256 = &sha
	var wg sync.WaitGroup
	start := make(chan struct{})
	responses := make(chan wire.StopAcceptance, 2)
	errors := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, receipt, result)
			responses <- response
			errors <- e
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal("concurrent same stop", e)
		}
	}
	duplicates := 0
	for r := range responses {
		if r.Duplicate {
			duplicates++
		}
		if r.State != "failed" {
			t.Fatal("failure outcome became success")
		}
	}
	if duplicates != 1 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.physical_stop_accepted'") != 1 {
		t.Fatal("concurrent stop accepted twice")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_attempts SET stop_token_hash=decode(repeat('b',64),'hex') WHERE id=$1", l.Training.AttemptID); e == nil {
		t.Fatal("delivered stop-token hash could be replaced")
	}
}

func TestActualTrainingAlteredFrozenInputCannotClaimOrStop(t *testing.T) {
	for _, mode := range []string{"claim", "stop"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			ctx := context.Background()
			var l agentwire.Lease
			if mode == "stop" {
				l = claimed(t, f)
			}
			var task string
			var raw []byte
			if e := f.s.DB.QueryRow(ctx, "SELECT task_id::text,t.payload FROM training_attempts a JOIN node_tasks t ON t.id=a.task_id WHERE a.id=$1", *j.CurrentAttemptID).Scan(&task, &raw); e != nil {
				t.Fatal(e)
			}
			command, e := wire.DecodeCommand(raw)
			if e != nil {
				t.Fatal(e)
			}
			command.Inputs.Train.SHA256 = strings.Repeat("d", 64)
			raw, _ = json.Marshal(command)
			if _, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET payload=$2 WHERE id=$1", task, raw); e != nil {
				t.Fatal(e)
			}
			if mode == "claim" {
				_, e = f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
			} else {
				l.Training = &command
				_, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, task, l.LeaseToken, stopFixture(t, f, l), nil)
			}
			expected(t, e, "TRAINING_ATTEMPT_STALE")
			if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 {
				t.Fatal("tampered command released physical owner")
			}
		})
	}
}
