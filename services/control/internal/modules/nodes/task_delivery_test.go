package nodes_test

import (
	"context"
	"sync"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/google/uuid"
)

// runTask creates, claims, acknowledges and completes one discovery task and returns its
// sequence.
func runTask(t *testing.T, s *nodes.Service, a identity.Auth, node wire.Enrolled, cursor uint64) uint64 {
	t.Helper()
	ctx := context.Background()
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: cursor})
	if e != nil || claim.Lease == nil || claim.Lease.TaskID != task.ID {
		t.Fatal("claim failed", claim, e)
	}
	acknowledge(t, s, node, claim)
	f := facts()
	if state, e := s.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Facts: &f}, uuid.NewString()); e != nil || state != "succeeded" {
		t.Fatal("completion failed", state, e)
	}
	return claim.Lease.Sequence
}

func TestARC006SequencesArePerNodeAndImmutable(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	one := enroll(t, s, a, "Sequence one")
	two := enroll(t, s, a, "Sequence two")
	cursor := uint64(0)
	for want := uint64(1); want <= 3; want++ {
		if cursor = runTask(t, s, a, one, cursor); cursor != want {
			t.Fatalf("node one sequence %d, want %d", cursor, want)
		}
	}
	if got := runTask(t, s, a, two, 0); got != 1 {
		t.Fatalf("node two starts at %d", got)
	}
	var assigned, acknowledged int64
	if e := s.DB.QueryRow(ctx, "SELECT task_sequence,task_acknowledged_sequence FROM managed_nodes WHERE id=$1", one.NodeID).Scan(&assigned, &acknowledged); e != nil || assigned != 3 || acknowledged != 3 {
		t.Fatal("node cursor", assigned, acknowledged, e)
	}
	if _, e := s.DB.Exec(ctx, "UPDATE node_tasks SET sequence=sequence+10 WHERE node_id=$1", one.NodeID); e == nil {
		t.Fatal("a task sequence changed")
	}
	// A caller cannot choose a sequence either: the insert trigger assigns the next one.
	task, e := s.CreateTask(ctx, a, one.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var sequence int64
	if e = s.DB.QueryRow(ctx, "SELECT sequence FROM node_tasks WHERE id=$1", task.ID).Scan(&sequence); e != nil || sequence != 4 {
		t.Fatal("next sequence", sequence, e)
	}
}

func TestARC006UnacknowledgedLeaseIsReleasedWithoutSpendingAnAttempt(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Release")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	lost, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || lost.Lease == nil {
		t.Fatal(e)
	}
	// The sweep releases an expired lease nobody acknowledged instead of failing it.
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	var attempt, deliveries int
	if e = s.DB.QueryRow(ctx, "SELECT state,attempt,deliveries FROM node_tasks WHERE id=$1", task.ID).Scan(&state, &attempt, &deliveries); e != nil || state != "pending" || attempt != 0 || deliveries != 1 {
		t.Fatal("expired unacknowledged lease", state, attempt, deliveries, e)
	}
	var released int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='node.task_released' AND target_id=$1", task.ID).Scan(&released); e != nil || released != 1 {
		t.Fatal("release audit", released, e)
	}
	// The released task is delivered again on attempt 1, and the lost token is fenced.
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Attempt != 1 || claim.Lease.Sequence != lost.Lease.Sequence {
		t.Fatal("released task not delivered again", e)
	}
	if _, e = s.Renew(ctx, node.NodeID, node.Credential, task.ID, lost.Lease.LeaseToken); e == nil {
		t.Fatal("a released token still renews")
	}
	f := facts()
	_, e = s.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: lost.Lease.LeaseToken, Outcome: "success", Facts: &f}, uuid.NewString())
	expected(t, e, "AGENT_LEASE_STALE")
}

func TestARC006DeliveriesAreBounded(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Deliveries")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	maximum := s.Identity.Config.Policy().Nodes.MaximumTaskDeliveries
	for delivery := 1; delivery <= maximum; delivery++ {
		claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
		if e != nil || claim.Lease == nil || claim.Lease.Attempt != 1 {
			t.Fatal("delivery", delivery, e)
		}
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease != nil {
		t.Fatal("delivered beyond the bound", e)
	}
	var state, code string
	if e = s.DB.QueryRow(ctx, "SELECT state,error_code FROM node_tasks WHERE id=$1", task.ID).Scan(&state, &code); e != nil || state != "failed" || code != "TASK_ATTEMPTS_EXHAUSTED" {
		t.Fatal("undelivered task", state, code, e)
	}
}

func TestARC006ResyncIsDecidedByCursorAndRateLimited(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Resync")
	cursor := runTask(t, s, a, node, 0)
	cursor = runTask(t, s, a, node, cursor)
	if cursor != 2 {
		t.Fatal("cursor", cursor)
	}
	// In step: no resync and no lease.
	claim, e := s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: 2})
	if e != nil || claim.Lease != nil || claim.Resync != nil {
		t.Fatal("in-step claim", claim, e)
	}
	// Control behind the agent, for example after a database restore.
	claim, e = s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: 9})
	if e != nil || claim.Resync == nil || claim.Resync.AcknowledgedSequence != 2 || claim.Lease != nil {
		t.Fatal("resync not requested", claim, e)
	}
	// The same node cannot resync again within the interval.
	_, e = s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: 0})
	var rate *identity.RateError
	if e == nil || e.Error() != "RATE_LIMITED" || !asRate(e, &rate) || rate.RetryAfter < 1 {
		t.Fatal("resync not rate limited", e)
	}
	// After the interval, an agent that lost its cursor resyncs too.
	if _, e = s.DB.Exec(ctx, "UPDATE managed_nodes SET resync_granted_at=clock_timestamp()-interval '1 day' WHERE id=$1", node.NodeID); e != nil {
		t.Fatal(e)
	}
	claim, e = s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: 0})
	if e != nil || claim.Resync == nil || claim.Resync.AcknowledgedSequence != 2 {
		t.Fatal("lost cursor not resynced", claim, e)
	}
	var granted int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='node.resync_granted' AND target_id=$1", node.NodeID).Scan(&granted); e != nil || granted != 2 {
		t.Fatal("resync audit", granted, e)
	}
	// With the adopted cursor the next task is delivered normally.
	if got := runTask(t, s, a, node, claim.Resync.AcknowledgedSequence); got != 3 {
		t.Fatal("after resync", got)
	}
}

// An acknowledgement proves a live lease of the calling node's own task. Every refusal
// must leave the task unacknowledged, or a lease no agent runs would look delivered.
func TestARC006AcknowledgementIsFenced(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Fenced")
	other := enroll(t, s, a, "Fenced other")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal("claim failed", e)
	}
	ack := wire.TaskAcknowledgementRequest{Sequence: claim.Lease.Sequence, LeaseToken: claim.Lease.LeaseToken}
	unacknowledged := func() {
		t.Helper()
		var attempt int
		var sequence int64
		if e := s.DB.QueryRow(ctx, "SELECT t.acknowledged_attempt,n.task_acknowledged_sequence FROM node_tasks t JOIN managed_nodes n ON n.id=t.node_id WHERE t.id=$1", task.ID).Scan(&attempt, &sequence); e != nil || attempt != 0 || sequence != 0 {
			t.Fatal("a refused acknowledgement changed the task", attempt, sequence, e)
		}
	}
	// Another node holds valid credentials, but the task is not in its scope.
	_, e = s.Acknowledge(ctx, other.NodeID, other.Credential, task.ID, ack, uuid.NewString())
	expected(t, e, "AGENT_LEASE_STALE")
	// Another node's credential does not authenticate this node.
	_, e = s.Acknowledge(ctx, node.NodeID, other.Credential, task.ID, ack, uuid.NewString())
	expected(t, e, "AGENT_UNAUTHORIZED")
	_, e = s.Acknowledge(ctx, node.NodeID, node.Credential, uuid.NewString(), ack, uuid.NewString())
	expected(t, e, "AGENT_LEASE_STALE")
	_, e = s.Acknowledge(ctx, node.NodeID, node.Credential, task.ID, wire.TaskAcknowledgementRequest{Sequence: wire.MaxTaskSequence + 1, LeaseToken: ack.LeaseToken}, uuid.NewString())
	expected(t, e, "VALIDATION_FAILED")
	unacknowledged()
	// An expired lease cannot be acknowledged; the agent must not execute it.
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Acknowledge(ctx, node.NodeID, node.Credential, task.ID, ack, uuid.NewString())
	expected(t, e, "AGENT_LEASE_STALE")
	unacknowledged()
	// A disabled node is refused even with a live lease.
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()+interval '1 minute' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE managed_nodes SET status='disabled' WHERE id=$1", node.NodeID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Acknowledge(ctx, node.NodeID, node.Credential, task.ID, ack, uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	unacknowledged()
}

// The agent executes a lease once control confirms it, and control redelivers a lease it
// believes was lost. If both won, one task would run twice, so exactly one may.
func TestARC006AcknowledgementAndRedeliveryNeverBothWin(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Race")
	cursor := uint64(0)
	acknowledgedWins, redeliveryWins := 0, 0
	for round := 0; round < 40; round++ {
		task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{AcknowledgedSequence: cursor})
		if e != nil || first.Lease == nil {
			t.Fatal("claim failed", first, e)
		}
		var group sync.WaitGroup
		var ackError, claimError error
		var second wire.Claim
		start := make(chan struct{})
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			_, ackError = s.Acknowledge(ctx, node.NodeID, node.Credential, task.ID, wire.TaskAcknowledgementRequest{Sequence: first.Lease.Sequence, LeaseToken: first.Lease.LeaseToken}, uuid.NewString())
		}()
		go func() {
			defer group.Done()
			<-start
			second, claimError = s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
		}()
		close(start)
		group.Wait()
		if claimError != nil {
			t.Fatal("claim during acknowledgement", claimError)
		}
		winner := first.Lease.LeaseToken
		switch {
		case ackError == nil && second.Lease == nil:
			acknowledgedWins++
		case ackError != nil && ackError.Error() == "AGENT_LEASE_STALE" && second.Lease != nil && second.Lease.LeaseToken != winner:
			redeliveryWins++
			winner = second.Lease.LeaseToken
			acknowledge(t, s, node, second)
		default:
			t.Fatal("acknowledgement and redelivery disagree", ackError, second.Lease)
		}
		var attempt, acknowledged int
		if e = s.DB.QueryRow(ctx, "SELECT attempt,acknowledged_attempt FROM node_tasks WHERE id=$1", task.ID).Scan(&attempt, &acknowledged); e != nil || attempt != 1 || acknowledged != 1 {
			t.Fatal("attempt after the race", attempt, acknowledged, e)
		}
		f := facts()
		if state, e := s.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: winner, Outcome: "success", Facts: &f}, uuid.NewString()); e != nil || state != "succeeded" {
			t.Fatal("completion failed", state, e)
		}
		cursor = first.Lease.Sequence
	}
	t.Log("acknowledgement won", acknowledgedWins, "redelivery won", redeliveryWins)
}

// Concurrent requests for one node serialize on the node row. The losers roll back, so
// they leave no gap in the node's sequence.
func TestARC006ConcurrentTaskCreationLeavesNoSequenceGap(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Concurrent")
	const requests = 8
	errs := make([]error, requests)
	var group sync.WaitGroup
	start := make(chan struct{})
	for i := range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, errs[i] = s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
		}()
	}
	close(start)
	group.Wait()
	created := 0
	for _, e := range errs {
		if e == nil {
			created++
		}
	}
	if created != 1 {
		t.Fatal("one in-flight task per node", created, errs)
	}
	var assigned, highest int64
	if e := s.DB.QueryRow(ctx, "SELECT n.task_sequence,(SELECT max(t.sequence) FROM node_tasks t WHERE t.node_id=n.id) FROM managed_nodes n WHERE n.id=$1", node.NodeID).Scan(&assigned, &highest); e != nil || assigned != 1 || highest != 1 {
		t.Fatal("sequence gap", assigned, highest, e)
	}
}

func asRate(e error, target **identity.RateError) bool {
	r, ok := e.(*identity.RateError)
	if ok {
		*target = r
	}
	return ok
}
