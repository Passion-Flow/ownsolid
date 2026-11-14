package nodes_test

import (
	"context"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/google/uuid"
)

// A task on its last attempt may be running through an outage. The sweep leaves it to its
// deadline and its token still works; only a claim proves the agent no longer holds it
// (ADR 0032).
func TestTheSweepLeavesATaskWhoseAgentIsAway(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	node := enroll(t, s, a, "Node away")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var last wire.Claim
	for attempt := 1; attempt <= 3; attempt++ {
		last, e = s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
		if e != nil || last.Lease == nil || last.Lease.Attempt != attempt {
			t.Fatal("attempt", attempt, e)
		}
		acknowledge(t, s, node, last)
		if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '6 hours' WHERE id=$1", task.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = s.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&state); e != nil || state != "running" {
		t.Fatal("the sweep ended a task whose agent is away", state, e)
	}
	renewed, e := s.Renew(ctx, node.NodeID, node.Credential, task.ID, last.Lease.LeaseToken)
	if e != nil || renewed.ValidForMilliseconds < 1 {
		t.Fatal("renewal after the outage", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '6 hours' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	f := facts()
	got, e := s.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: last.Lease.LeaseToken, Outcome: "success", Facts: &f}, uuid.NewString())
	if e != nil || got != "succeeded" {
		t.Fatal("the result of work finished during an outage", got, e)
	}
}

func TestADeadlineEndsAwayTasksAndTheirTokens(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	node := enroll(t, s, a, "Node away past its deadline")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	acknowledge(t, s, node, claim)
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Renew(ctx, node.NodeID, node.Credential, task.ID, claim.Lease.LeaseToken)
	expected(t, e, "AGENT_LEASE_STALE")
	if e = s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = s.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&state); e != nil || state != "failed" {
		t.Fatal("the deadline did not end the task", state, e)
	}
}
