package nodes_test

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
)

// retiredDays is older than any task retention a test runs with.
const retiredDays = 4000

// leasedTask starts a task and returns it with the lease the node holds on it.
func leasedTask(t *testing.T, s *nodes.Service, a identity.Auth, node wire.Enrolled) (string, wire.Lease) {
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
	return task.ID, *claim.Lease
}

func stepEntry(sequence int, step, state string, at time.Time) wire.ProgressEntry {
	return wire.ProgressEntry{Sequence: sequence, Kind: wire.ProgressKindStep, At: at, Step: step, State: state}
}

func lineEntry(sequence int, message string, at time.Time) wire.ProgressEntry {
	return wire.ProgressEntry{Sequence: sequence, Kind: wire.ProgressKindLog, At: at, Level: wire.ProgressLevelInfo, Message: message}
}

func report(t *testing.T, s *nodes.Service, node wire.Enrolled, task string, lease wire.Lease, entries ...wire.ProgressEntry) int {
	t.Helper()
	out, e := s.RecordTaskProgress(context.Background(), node.NodeID, node.Credential, task, wire.TaskProgress{LeaseToken: lease.LeaseToken, Entries: entries})
	if e != nil {
		t.Fatal(e)
	}
	return out.Accepted
}

func complete(t *testing.T, s *nodes.Service, node wire.Enrolled, task string, lease wire.Lease) {
	t.Helper()
	facts := facts()
	if _, e := s.Complete(context.Background(), node.NodeID, node.Credential, task, wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Facts: &facts}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}

func TestTaskProgressShowsStepsAndLogWhileItRuns(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Progress node")
	task, lease := leasedTask(t, s, a, node)
	start := time.Now().UTC().Truncate(time.Millisecond)
	accepted := report(t, s, node, task, lease,
		stepEntry(1, wire.ProgressStepReadSystem, wire.ProgressStepRunning, start),
		lineEntry(2, "Operating system: ubuntu 24.04", start.Add(time.Second)),
		stepEntry(3, wire.ProgressStepReadSystem, wire.ProgressStepSucceeded, start.Add(3*time.Second)),
		stepEntry(4, wire.ProgressStepReadMemory, wire.ProgressStepRunning, start.Add(3*time.Second)),
	)
	if accepted != 4 {
		t.Fatal(accepted)
	}
	detail, e := s.AdminTask(ctx, a, task)
	if e != nil {
		t.Fatal(e)
	}
	if detail.Task.NodeName != "Progress node" || detail.Task.State != "running" || len(detail.Steps) != 2 {
		t.Fatal(detail)
	}
	first := detail.Steps[0]
	if first.State != wire.ProgressStepSucceeded || first.FinishedAt == nil || first.FinishedAt.Sub(first.StartedAt) != 3*time.Second {
		t.Fatal("the agent's own clock gives the duration", first)
	}
	if detail.Steps[1].State != wire.ProgressStepRunning || detail.Steps[1].FinishedAt != nil {
		t.Fatal(detail.Steps[1])
	}
	log, e := s.AdminTaskLog(ctx, a, task, url.Values{})
	if e != nil || len(log.Items) != 1 || log.Items[0].Message != "Operating system: ubuntu 24.04" || log.Cursor != "1.2" {
		t.Fatal(log, e)
	}
	next, e := s.AdminTaskLog(ctx, a, task, url.Values{"after": {log.Cursor}})
	if e != nil || len(next.Items) != 0 || next.Cursor != log.Cursor {
		t.Fatal("a read past the end keeps its cursor", next, e)
	}
	report(t, s, node, task, lease, lineEntry(5, "Memory read", start.Add(4*time.Second)))
	next, e = s.AdminTaskLog(ctx, a, task, url.Values{"after": {log.Cursor}})
	if e != nil || len(next.Items) != 1 || next.Items[0].Message != "Memory read" || next.Cursor != "1.5" {
		t.Fatal(next, e)
	}
}

func TestARepeatedReportChangesNothing(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "Repeat node")
	task, lease := leasedTask(t, s, a, node)
	at := time.Now().UTC()
	entries := []wire.ProgressEntry{lineEntry(1, "first", at), lineEntry(2, "second", at)}
	if n := report(t, s, node, task, lease, entries...); n != 2 {
		t.Fatal(n)
	}
	if n := report(t, s, node, task, lease, entries...); n != 0 {
		t.Fatal("a repeat is new to nobody", n)
	}
	if n := report(t, s, node, task, lease, entries[1], lineEntry(3, "third", at)); n != 1 {
		t.Fatal("only the new entry counts", n)
	}
	log, e := s.AdminTaskLog(context.Background(), a, task, url.Values{})
	if e != nil || len(log.Items) != 3 {
		t.Fatal(log, e)
	}
}

func TestConcurrentReportsStoreEachEntryOnce(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "Concurrent node")
	task, lease := leasedTask(t, s, a, node)
	at := time.Now().UTC()
	entries := make([]wire.ProgressEntry, 50)
	for index := range entries {
		entries[index] = lineEntry(index+1, fmt.Sprintf("line %d", index+1), at)
	}
	results := make([]int, 4)
	errs := make([]error, len(results))
	var wg sync.WaitGroup
	for index := range results {
		wg.Go(func() {
			out, e := s.RecordTaskProgress(context.Background(), node.NodeID, node.Credential, task, wire.TaskProgress{LeaseToken: lease.LeaseToken, Entries: entries})
			results[index], errs[index] = out.Accepted, e
		})
	}
	wg.Wait()
	total := 0
	for index, accepted := range results {
		if errs[index] != nil {
			t.Fatal(errs[index])
		}
		total += accepted
	}
	if total != len(entries) {
		t.Fatal("every entry is stored once", total, results)
	}
}

func TestOnlyTheLeaseCanReport(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Lease node")
	other := enroll(t, s, a, "Other node")
	task, lease := leasedTask(t, s, a, node)
	entries := []wire.ProgressEntry{lineEntry(1, "line", time.Now().UTC())}
	stale := wire.TaskProgress{LeaseToken: "not-the-lease-token-not-the-lease-token-not-the-lease-token-xxxx", Entries: entries}
	if _, e := s.RecordTaskProgress(ctx, node.NodeID, node.Credential, task, stale); e == nil {
		t.Fatal("a wrong lease reported")
	}
	good := wire.TaskProgress{LeaseToken: lease.LeaseToken, Entries: entries}
	if _, e := s.RecordTaskProgress(ctx, other.NodeID, other.Credential, task, good); e == nil {
		t.Fatal("another node reported")
	}
	if _, e := s.RecordTaskProgress(ctx, node.NodeID, other.Credential, task, good); e == nil {
		t.Fatal("another node's credential reported")
	}
	complete(t, s, node, task, lease)
	if _, e := s.RecordTaskProgress(ctx, node.NodeID, node.Credential, task, good); e == nil {
		t.Fatal("a finished task took a report")
	}
	if log, e := s.AdminTaskLog(ctx, a, task, url.Values{}); e != nil || len(log.Items) != 0 {
		t.Fatal(log, e)
	}
}

func TestAMalformedReportIsRefused(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "Malformed node")
	task, lease := leasedTask(t, s, a, node)
	_, e := s.RecordTaskProgress(context.Background(), node.NodeID, node.Credential, task, wire.TaskProgress{LeaseToken: lease.LeaseToken, Entries: []wire.ProgressEntry{lineEntry(1, "one\ntwo", time.Now())}})
	if e == nil || e.Error() != "TASK_PROGRESS_INVALID" {
		t.Fatal(e)
	}
}

func TestAFinishedTaskKeepsItsProgressAfterItLeavesTheLiveTable(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Finished node")
	task, lease := leasedTask(t, s, a, node)
	at := time.Now().UTC()
	report(t, s, node, task, lease, stepEntry(1, wire.ProgressStepReadDisk, wire.ProgressStepRunning, at), lineEntry(2, "Disk read", at))
	complete(t, s, node, task, lease)

	// A real task keeps its creation time, which is how its progress is found.
	for _, table := range []string{"node_tasks", "node_task_history"} {
		if _, e := s.DB.Exec(ctx, `UPDATE `+table+` SET completed_at=completed_at-make_interval(days=>$2) WHERE id=$1`, task, retiredDays); e != nil {
			t.Fatal(e)
		}
	}
	pruneOnce(t, s)
	if exists(t, s, inTasks, task) {
		t.Fatal("the task should have left the live table")
	}
	detail, e := s.AdminTask(ctx, a, task)
	if e != nil || detail.Task.State != "succeeded" || len(detail.Steps) != 1 {
		t.Fatal(detail, e)
	}
	if step := detail.Steps[0]; step.State != wire.ProgressStepSucceeded || step.FinishedAt == nil {
		t.Fatal("a step the agent left open takes the task's end", step)
	}
	log, e := s.AdminTaskLog(ctx, a, task, url.Values{})
	if e != nil || len(log.Items) != 1 || log.Items[0].Message != "Disk read" {
		t.Fatal(log, e)
	}
}

func TestATimelineShowsTheLatestAttemptAndTheLogShowsThemAll(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Attempts node")
	task, lease := leasedTask(t, s, a, node)
	at := time.Now().UTC()
	report(t, s, node, task, lease, stepEntry(1, wire.ProgressStepReadSystem, wire.ProgressStepRunning, at), lineEntry(2, "First attempt", at))
	if _, e := s.DB.Exec(ctx, `INSERT INTO node_task_progress(tenant_id,workspace_id,node_id,task_id,task_created_at,attempt,sequence,kind,step,step_state,reported_at)
 SELECT tenant_id,workspace_id,node_id,task_id,task_created_at,2,1,'step','read_disk','running',reported_at FROM node_task_progress WHERE task_id=$1 AND sequence=1`, task); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, `INSERT INTO node_task_progress(tenant_id,workspace_id,node_id,task_id,task_created_at,attempt,sequence,kind,level,message,reported_at)
 SELECT tenant_id,workspace_id,node_id,task_id,task_created_at,2,2,'log','info','Second attempt',reported_at FROM node_task_progress WHERE task_id=$1 AND attempt=1 AND sequence=1`, task); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, `UPDATE node_tasks SET attempt=2 WHERE id=$1`, task); e != nil {
		t.Fatal(e)
	}
	detail, e := s.AdminTask(ctx, a, task)
	if e != nil || len(detail.Steps) != 1 || detail.Steps[0].Step != wire.ProgressStepReadDisk {
		t.Fatal(detail, e)
	}
	log, e := s.AdminTaskLog(ctx, a, task, url.Values{})
	if e != nil || len(log.Items) != 2 || log.Items[0].Attempt != 1 || log.Items[1].Attempt != 2 || log.Cursor != "2.2" {
		t.Fatal(log, e)
	}
	rest, e := s.AdminTaskLog(ctx, a, task, url.Values{"after": {"1.2"}})
	if e != nil || len(rest.Items) != 1 || rest.Items[0].Message != "Second attempt" {
		t.Fatal(rest, e)
	}
}

func TestTheTaskListCoversEveryNodeAndBothTables(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	first := enroll(t, s, a, "List node one")
	second := enroll(t, s, a, "List node two")
	done := finishedTask(t, s, a, first)
	running, _ := leasedTask(t, s, a, second)
	waiting, e := s.CreateTask(ctx, a, first.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	ids := func(values url.Values) []string {
		t.Helper()
		page, e := s.AdminTasks(ctx, a, values)
		if e != nil {
			t.Fatal(e)
		}
		out := []string{}
		for _, item := range page.Items {
			out = append(out, item.ID)
		}
		return out
	}
	if got := ids(url.Values{}); len(got) != 3 || got[0] != waiting.ID || got[1] != running || got[2] != done {
		t.Fatal("newest first across nodes and tables", got)
	}
	if got := ids(url.Values{"state": {"succeeded"}}); len(got) != 1 || got[0] != done {
		t.Fatal(got)
	}
	if got := ids(url.Values{"state": {"running"}}); len(got) != 1 || got[0] != running {
		t.Fatal(got)
	}
	if got := ids(url.Values{"state": {"failed"}}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := ids(url.Values{"mine": {"true"}}); len(got) != 2 {
		t.Fatal("mine lists unfinished tasks only", got)
	}

	page, e := s.AdminTasks(ctx, a, url.Values{"limit": {"2"}})
	if e != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatal(page, e)
	}
	rest, e := s.AdminTasks(ctx, a, url.Values{"limit": {"2"}, "cursor": {*page.NextCursor}})
	if e != nil || len(rest.Items) != 1 || rest.Items[0].ID != done || rest.NextCursor != nil {
		t.Fatal(rest, e)
	}
	if _, e = s.AdminTasks(ctx, a, url.Values{"state": {"running"}, "cursor": {*page.NextCursor}}); e == nil || e.Error() != "INVALID_CURSOR" {
		t.Fatal("a cursor belongs to its filter", e)
	}
}

func TestMineListsOnlyWhatThePersonStarted(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Mine node")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	someone := uuid.NewString()
	if _, e = s.DB.Exec(ctx, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,'someone@example.invalid','someone@example.invalid','Someone','en','active')`, someone); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE node_tasks SET requested_by=$2 WHERE id=$1`, task.ID, someone); e != nil {
		t.Fatal(e)
	}
	page, e := s.AdminTasks(ctx, a, url.Values{"mine": {"true"}})
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
}

func TestAnotherTenantsTaskIsNotThere(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	foreign, foreignTenant := uuid.NewString(), uuid.NewString()
	if _, e := s.DB.Exec(ctx, `INSERT INTO node_task_history(id,tenant_id,workspace_id,node_id,operation,state,attempt,created_at,updated_at,deadline_at,completed_at)
 VALUES($1,$2,$2,$2,'collect_facts.v1','succeeded',1,clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp())`, foreign, foreignTenant); e != nil {
		t.Fatal(e)
	}
	page, e := s.AdminTasks(ctx, a, url.Values{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
	if _, e = s.AdminTask(ctx, a, foreign); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal(e)
	}
	if _, e = s.AdminTaskLog(ctx, a, foreign, url.Values{}); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal(e)
	}
}

func TestOnlyAdministratorsReadTasks(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	portal := a
	portal.Me.Audience = "portal"
	if _, e := s.AdminTasks(ctx, portal, url.Values{}); e == nil {
		t.Fatal("the portal listed tasks")
	}
	if _, e := s.AdminTask(ctx, portal, uuid.NewString()); e == nil {
		t.Fatal("the portal read a task")
	}
	if _, e := s.AdminTaskLog(ctx, portal, uuid.NewString(), url.Values{}); e == nil {
		t.Fatal("the portal read a log")
	}
}
