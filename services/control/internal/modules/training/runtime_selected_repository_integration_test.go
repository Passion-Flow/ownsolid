//go:build repository_integration

package training_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Start the production control loop against a genuinely queued selected-SQL
// job. Joining it never supplies a machine stop receipt or releases a GPU.
func trainingRuntimeStart(t *testing.T, g *trainingRuntimeStory) func() {
	t.Helper()
	policy := g.s.Identity.Config.Policy()
	policy.Training.PollMilliseconds = 100
	g.s.Identity.Config.Runtime = &policy
	ctx, cancel := context.WithCancel(g.ctx)
	done := make(chan struct{})
	go func() { g.s.Run(ctx); close(done) }()
	join := func() {
		t.Helper()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("selected training control worker did not join")
		}
	}
	t.Cleanup(join)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var e error
		g.job, e = g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil {
			t.Fatal("selected control queue read", e)
		}
		if g.job.State == "preparing" && g.job.CurrentAttemptID != nil {
			return join
		}
		if time.Now().After(deadline) {
			t.Fatal("selected control loop did not dispatch durable queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (g trainingRuntimeStory) requireDeliveredOwner(t *testing.T) {
	t.Helper()
	if g.count(t, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=? AND state='held'", g.job.ID) != 1 ||
		g.count(t, "SELECT count(*) FROM training_attempts WHERE id=$1 AND physical_stopped_at IS NULL AND stop_token_hash IS NOT NULL", "SELECT COUNT(*) FROM training_attempts WHERE id=? AND physical_stopped_at IS NULL AND stop_token_hash IS NOT NULL", g.lease.Training.AttemptID) != 1 {
		t.Fatal("control shutdown invented a physical stop or lost delivered ownership")
	}
}

func TestActualPairedTrainingControlRuntime(t *testing.T) {
	t.Run("queue_worker_callbacks_and_join_retain_delivered_owner", func(t *testing.T) {
		g := trainingRuntimeQueue(t, nil)
		join := trainingRuntimeStart(t, &g)
		g.claim(t)
		renewed, handled, e := g.s.RenewTraining(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
		if e != nil || !handled || renewed.ValidForMilliseconds <= 0 {
			t.Fatal("selected worker dispatched heartbeat", e)
		}
		accepted, e := g.s.RecordEvents(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, prelude(*g.lease.Training))
		if e != nil || len(accepted) != 2 || accepted[1].JobState != "running" {
			t.Fatal("selected worker dispatched events", e)
		}
		join()
		g.requireDeliveredOwner(t)
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal("joined selected worker retained store", e)
		}
	})

	t.Run("claim_binding_conflict_rolls_back_task_secret_and_audit", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, false)
		attempt := *g.job.CurrentAttemptID
		// An inconsistent pending task cannot overwrite a retained stop fence.
		// This explicit fault is injected into real SQL, without issuing a lease.
		oldHash := bytes.Repeat([]byte{7}, 32)
		g.exec(t, "UPDATE training_attempts SET stop_token_hash=$1 WHERE id=$2", "UPDATE training_attempts SET stop_token_hash=? WHERE id=?", oldHash, attempt)
		claim, handled, e := g.s.ClaimTraining(g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		if e == nil || e.Error() != "TRAINING_ATTEMPT_STALE" || !handled || !reflect.DeepEqual(claim, agent.Claim{}) {
			t.Fatal("conflicting attempt fence exposed a new claim", e)
		}
		var saved []byte
		var task string
		var unstarted bool
		if e = g.f.Store.QueryRow(g.ctx, g.statement("SELECT stop_token_hash,task_id::text,started_at IS NULL FROM training_attempts WHERE id=$1", "SELECT stop_token_hash,task_id,started_at IS NULL FROM training_attempts WHERE id=?", attempt)).Scan(&saved, &task, &unstarted); e != nil || !bytes.Equal(saved, oldHash) || !unstarted {
			t.Fatal("conflicting claim changed retained attempt fence", e)
		}
		if g.count(t, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='pending' AND attempt=0 AND lease_token_hash IS NULL AND lease_expires_at IS NULL", "SELECT COUNT(*) FROM node_tasks WHERE id=? AND state='pending' AND attempt=0 AND lease_token_hash IS NULL AND lease_expires_at IS NULL", task) != 1 ||
			g.count(t, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='node.task_claimed'", "SELECT COUNT(*) FROM audit_events WHERE target_id=? AND action='node.task_claimed'", task) != 0 {
			t.Fatal("conflicting claim committed task lease or success audit")
		}
	})
}
