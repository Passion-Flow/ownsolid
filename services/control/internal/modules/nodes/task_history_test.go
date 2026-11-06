package nodes_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// finishedTask runs one task of node to its end through the real calls.
func finishedTask(t *testing.T, s *nodes.Service, a identity.Auth, node wire.Enrolled) string {
	t.Helper()
	ctx := context.Background()
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(claim, e)
	}
	if _, e = s.Acknowledge(ctx, node.NodeID, node.Credential, task.ID, wire.TaskAcknowledgementRequest{Sequence: claim.Lease.Sequence, LeaseToken: claim.Lease.LeaseToken}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	facts := facts()
	if _, e = s.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Facts: &facts}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	return task.ID
}

func exists(t *testing.T, s *nodes.Service, query, id string) bool {
	t.Helper()
	var found bool
	if e := s.DB.QueryRow(context.Background(), query, id).Scan(&found); e != nil {
		t.Fatal(e)
	}
	return found
}

const (
	inTasks   = `SELECT EXISTS(SELECT 1 FROM node_tasks WHERE id=$1)`
	inHistory = `SELECT EXISTS(SELECT 1 FROM node_task_history WHERE id=$1)`
)

func age(t *testing.T, s *nodes.Service, id string, days int) {
	t.Helper()
	ctx := context.Background()
	if _, e := s.DB.Exec(ctx, `UPDATE node_tasks SET completed_at=completed_at-make_interval(days=>$2),created_at=created_at-make_interval(days=>$2) WHERE id=$1`, id, days); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, `UPDATE node_task_history SET completed_at=completed_at-make_interval(days=>$2),created_at=created_at-make_interval(days=>$2) WHERE id=$1`, id, days); e != nil {
		t.Fatal(e)
	}
}

func pruneOnce(t *testing.T, s *nodes.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := s.PruneTasks(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestEveryWayATaskEndsReachesTheHistoryOnce(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "History node")
	succeeded := finishedTask(t, s, a, node)
	if !exists(t, s, inHistory, succeeded) {
		t.Fatal("a completed task has no history")
	}
	expired, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, expired.ID); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e = s.Sweep(bounded); e != nil {
		t.Fatal(e)
	}
	var state string
	var count int
	if e = s.DB.QueryRow(ctx, `SELECT max(state),count(*) FROM node_task_history WHERE id=$1`, expired.ID).Scan(&state, &count); e != nil || state != "failed" || count != 1 {
		t.Fatal(state, count, e)
	}
	// Touching a finished task records nothing more.
	if _, e = s.DB.Exec(ctx, `UPDATE node_tasks SET state=state WHERE id=$1`, succeeded); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM node_task_history WHERE id=$1`, succeeded).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestARolledBackFinishLeavesNoHistory(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Rolled back")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE node_tasks SET state='cancelled',completed_at=clock_timestamp() WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if exists(t, s, inHistory, task.ID) {
		t.Fatal("history outlived a rollback")
	}
}

func TestTheTaskListReadsLiveTasksAndTheHistory(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Listed node")
	old := finishedTask(t, s, a, node)
	age(t, s, old, 40)
	pruneOnce(t, s)
	if exists(t, s, inTasks, old) {
		t.Fatal("the old task was not pruned")
	}
	recent := finishedTask(t, s, a, node)
	live, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	want := []string{live.ID, recent, old}
	got := []string{}
	cursor := ""
	for range 3 {
		values := url.Values{"limit": {"1"}}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		page, e := s.Tasks(ctx, a, a.Me.Workspace.ID, node.NodeID, values)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Items {
			got = append(got, v.ID)
		}
		if page.NextCursor != nil {
			cursor = *page.NextCursor
		}
	}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("listed %v, want %v", got, want)
	}
}

func TestPruneKeepsWhatIsRecentReferencedOrMissingItsHistory(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Pruned node")
	recent := finishedTask(t, s, a, node)
	unrecorded := finishedTask(t, s, a, node)
	age(t, s, unrecorded, 40)
	if _, e := s.DB.Exec(ctx, `DELETE FROM node_task_history WHERE id=$1`, unrecorded); e != nil {
		t.Fatal(e)
	}
	expired := finishedTask(t, s, a, node)
	age(t, s, expired, 40)
	pruneOnce(t, s)
	if !exists(t, s, inTasks, recent) || !exists(t, s, inTasks, unrecorded) {
		t.Fatal("pruned a task it must keep")
	}
	if exists(t, s, inTasks, expired) || !exists(t, s, inHistory, expired) {
		t.Fatal("the expired task did not move to the history alone")
	}
}

func TestPruneWalksPastTasksItCannotRemove(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	policy := config.DefaultRuntimePolicy()
	policy.Nodes.TaskSweepBatch = 1
	s.Identity.Config.Runtime = &policy
	node := enroll(t, s, a, "Walked node")
	kept := finishedTask(t, s, a, node)
	age(t, s, kept, 50)
	if _, e := s.DB.Exec(ctx, `DELETE FROM node_task_history WHERE id=$1`, kept); e != nil {
		t.Fatal(e)
	}
	removed := finishedTask(t, s, a, node)
	age(t, s, removed, 40)
	pruneOnce(t, s)
	if !exists(t, s, inTasks, removed) {
		t.Fatal("a batch of one reached the second task")
	}
	pruneOnce(t, s)
	if exists(t, s, inTasks, removed) {
		t.Fatal("the walk stayed on a task it cannot remove")
	}
	// A walk that reaches the end starts over.
	pruneOnce(t, s)
	pruneOnce(t, s)
	if !exists(t, s, inTasks, kept) {
		t.Fatal("removed a task with no history")
	}
}
