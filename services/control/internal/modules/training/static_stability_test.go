package training_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
)

// silence moves a lease far into the past, as a long outage does, and makes the job due.
func silence(t *testing.T, f scenario, task, table, job string) {
	t.Helper()
	ctx := context.Background()
	if _, e := f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '6 hours' WHERE id=$1", task); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.DB.Exec(ctx, "UPDATE "+table+" SET next_dispatch_at=clock_timestamp() WHERE id=$1", job); e != nil {
		t.Fatal(e)
	}
}

func trainingEvent(job, attempt string, sequence int) []byte {
	raw, _ := json.Marshal(map[string]any{"sequence": sequence, "job_id": job, "attempt_id": attempt, "global_step": 0, "kind": "phase", "phase": "loading_model"})
	return raw
}

// An outage of any length before the deadline leaves the job running, shows lost contact,
// and gives the agent its token back when it returns (ADR 0032).
func TestATrainingJobKeepsRunningThroughAnOutage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	j := dispatched(t, f)
	l := claimed(t, f)
	silence(t, f, l.TaskID, "training_jobs", j.ID)

	worked, e := f.s.ReconcileNext(ctx)
	if e != nil || worked {
		t.Fatal("the outage changed the job", worked, e)
	}
	if count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='preparing' AND wait_code='TRAINING_NODE_UNREACHABLE' AND error_code IS NULL", j.ID) != 1 {
		t.Fatal("lost contact is not shown, or the job moved")
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", j.ID) != 1 {
		t.Fatal("the outage moved the GPU reservation")
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.physical_stop_required' AND target_id=$1", j.ID) != 0 {
		t.Fatal("the outage asked for a physical stop")
	}
	// Reconciling again changes nothing and writes nothing new.
	revision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
	silence(t, f, l.TaskID, "training_jobs", j.ID)
	if _, e = f.s.ReconcileNext(ctx); e != nil || count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != revision {
		t.Fatal("a steady outage kept changing the job", e)
	}

	// No other claim can take the task while the agent is away.
	again, e := f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
	if e != nil || again.Lease != nil {
		t.Fatal("an attempt that may be running was delivered again", e)
	}

	// The agent returns: what it held back is accepted with the same token.
	if _, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, trainingEvent(j.ID, *j.CurrentAttemptID, 1)); e != nil {
		t.Fatal("an event after the outage", e)
	}
	renewed, e := f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken)
	if e != nil || renewed.ValidForMilliseconds < 1 {
		t.Fatal("renewal after the outage", e)
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", j.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ReconcileNext(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND wait_code IS NULL", j.ID) != 1 {
		t.Fatal("lost contact is still shown after the agent returned")
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", j.ID) != 1 {
		t.Fatal("the GPU reservation moved after the agent returned")
	}
}

func TestAnAgentReturningAfterACancelIsRefusedAndCanStop(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	j := dispatched(t, f)
	l := claimed(t, f)
	silence(t, f, l.TaskID, "training_jobs", j.ID)
	if _, e := f.s.ReconcileNext(ctx); e != nil {
		t.Fatal(e)
	}
	current, e := f.s.Get(ctx, f.portal, j.WorkspaceID, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Cancel(ctx, f.portal, j.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: current.Revision, Reason: "Cancelled while the node is away"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken)
	expected(t, e, "ACCESS_DENIED")
	accepted, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stopFixture(t, f, l), nil)
	if e != nil || !accepted.GPUReleased || accepted.State != "cancelled" {
		t.Fatal("stop after the refusal", accepted, e)
	}
}

func TestTheDeadlineEndsAuthorityAfterAnOutage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	j := dispatched(t, f)
	l := claimed(t, f)
	silence(t, f, l.TaskID, "training_jobs", j.ID)
	if _, e := f.s.ReconcileNext(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.TaskID); e != nil {
		t.Fatal(e)
	}
	_, e := f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken)
	expected(t, e, "AGENT_LEASE_STALE")
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", j.ID); e != nil {
		t.Fatal(e)
	}
	if worked, e := f.s.ReconcileNext(ctx); e != nil || !worked {
		t.Fatal("deadline not reconciled", worked, e)
	}
	if count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='recovery_required' AND wait_code IS NULL AND error_code='TRAINING_LEASE_EXPIRED'", j.ID) != 1 {
		t.Fatal("the deadline did not require a physical stop")
	}
}
