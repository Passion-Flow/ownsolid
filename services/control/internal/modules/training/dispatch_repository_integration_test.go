//go:build repository_integration

package training_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func dispatchSetup(t *testing.T) trainingOperatorStory {
	t.Helper()
	g := trainingOperatorSetup(t)
	g.hardware(t)
	return g
}
func dispatchWant(t *testing.T, g trainingOperatorStory, wantState, wantWait string, attempts, tasks, leases int) {
	t.Helper()
	var state string
	var wait *string
	e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT state,wait_code FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT state,wait_code FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND id=?", g.f.Tenant, g.f.Workspace, g.job.ID)).Scan(&state, &wait)
	if e != nil || state != wantState || (wantWait == "" && wait != nil) || (wantWait != "" && (wait == nil || *wait != wantWait)) {
		t.Fatal("actual dispatch state", state, wait, e)
	}
	for i, pair := range [][2]string{
		{"SELECT COUNT(*) FROM training_attempts WHERE job_id=$1", "SELECT COUNT(*) FROM training_attempts WHERE job_id=?"},
		{"SELECT COUNT(*) FROM node_tasks WHERE node_id=$1 AND operation='training.run.v1'", "SELECT COUNT(*) FROM node_tasks WHERE node_id=? AND operation='training.run.v1'"},
		{"SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=$1", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=?"},
	} {
		id := g.job.ID
		if i == 1 {
			id = g.input.NodeID
		}
		var n int
		if e := g.f.Store.QueryRow(g.ctx, g.statement(pair[0], pair[1], id)).Scan(&n); e != nil || n != []int{attempts, tasks, leases}[i] {
			t.Fatal("actual dispatch cardinality", i, n, e)
		}
	}
}
func dispatchOutcome(t *testing.T, g trainingOperatorStory, want bool, code string) {
	t.Helper()
	got, e := g.s.DispatchNext(g.ctx)
	if got != want || code == "" && e != nil || code != "" && (e == nil || e.Error() != code) {
		t.Fatal("actual dispatch outcome", got, e, "want", want, code)
	}
}
func dispatchDue(t *testing.T, g trainingOperatorStory) {
	t.Helper()
	g.exec(t, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET next_dispatch_at=UTC_TIMESTAMP(6) WHERE id=?", g.job.ID)
}
func dispatchCommand(t *testing.T, g trainingOperatorStory) wire.Command {
	t.Helper()
	var raw []byte
	e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT t.payload FROM node_tasks t JOIN training_attempts a ON a.task_id=t.id WHERE a.job_id=$1", "SELECT t.payload FROM node_tasks t JOIN training_attempts a ON a.task_id=t.id WHERE a.job_id=?", g.job.ID)).Scan(&raw)
	if e != nil {
		t.Fatal(e)
	}
	c, e := wire.DecodeCommand(raw)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

// Test-only failure injection keeps real statements/rollback/commit. No product
// hook, fake processor or fabricated successful repository is used.
type dispatchFailureStore struct {
	transaction.Store
	calls, hit int
}
type dispatchFailureExecutor struct {
	transaction.Executor
	count int
	owner *dispatchFailureStore
}

func (s *dispatchFailureStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	return s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if s.calls == 2 {
			return fn(ctx, &dispatchFailureExecutor{Executor: q, owner: s})
		}
		return fn(ctx, q)
	})
}
func (q *dispatchFailureExecutor) Exec(ctx context.Context, s transaction.Statement) (int64, error) {
	q.count++
	if q.count == 6 {
		q.owner.hit++
		return 0, errors.New("STORAGE_UNAVAILABLE")
	}
	return q.Executor.Exec(ctx, s)
}

func TestActualPairedTrainingDispatch(t *testing.T) {
	t.Run("actual_typed_dispatch_and_no_duplicate", func(t *testing.T) {
		g := dispatchSetup(t)
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "preparing", "", 1, 1, 1)
		c := dispatchCommand(t, g)
		if c.Scope.JobID != g.job.ID || c.Scope.TenantID != g.f.Tenant || c.Scope.WorkspaceID != g.f.Workspace || c.AttemptNumber != 1 || c.GPUUUID != g.input.GPUUUID || c.Inputs.Train.Path != "train.jsonl" || c.Resume != nil {
			t.Fatal("actual frozen command")
		}
		var deadline bool
		if e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT deadline_at>clock_timestamp() FROM node_tasks WHERE id=(SELECT task_id FROM training_attempts WHERE job_id=$1)", "SELECT deadline_at>UTC_TIMESTAMP(6) FROM node_tasks WHERE id=(SELECT task_id FROM training_attempts WHERE job_id=?)", g.job.ID)).Scan(&deadline); e != nil || !deadline {
			t.Fatal("database deadline", e)
		}
		dispatchOutcome(t, g, false, "")
		dispatchWant(t, g, "preparing", "", 1, 1, 1)
	})
	t.Run("current_actor_and_membership_lifetime", func(t *testing.T) {
		g := dispatchSetup(t)
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "failed", "", 0, 0, 0)
		g.exec(t, "UPDATE users SET auth_epoch=1 WHERE id=$1", "UPDATE users SET auth_epoch=1 WHERE id=?", g.f.User)
		a := g.portal(t, "owner")
		g.a = a
		j, e := g.create(uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		g.job = j
		g.exec(t, "UPDATE workspace_memberships SET authority_id=$1 WHERE user_id=$2 AND workspace_id=$3", "UPDATE workspace_memberships SET authority_id=? WHERE user_id=? AND workspace_id=?", uuid.NewString(), a.Me.User.ID, g.f.Workspace)
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "failed", "", 0, 0, 0)
	})
	t.Run("scoped_unavailable_stale_hardware_waits", func(t *testing.T) {
		g := dispatchSetup(t)
		g.exec(t, "UPDATE inference_nodes SET received_at=clock_timestamp()-interval '1 day' WHERE node_id=$1", "UPDATE inference_nodes SET received_at=TIMESTAMPADD(DAY,-1,UTC_TIMESTAMP(6)) WHERE node_id=?", g.input.NodeID)
		dispatchOutcome(t, g, false, "")
		dispatchWant(t, g, "queued", "TRAINING_PREFLIGHT_REQUIRED", 0, 0, 0)
		g.exec(t, "UPDATE managed_nodes SET status='disabled' WHERE id=$1", "UPDATE managed_nodes SET status='disabled' WHERE id=?", g.input.NodeID)
		dispatchDue(t, g)
		dispatchOutcome(t, g, false, "")
		dispatchWant(t, g, "queued", "NODE_UNAVAILABLE", 0, 0, 0)
	})
	t.Run("actual_partition_corruption_never_reserves", func(t *testing.T) {
		g := dispatchSetup(t)
		if e := os.WriteFile(g.partition, []byte("changed"), 0600); e != nil {
			t.Fatal(e)
		}
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "failed", "", 0, 0, 0)
		var code string
		if e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT error_code FROM training_jobs WHERE id=$1", "SELECT error_code FROM training_jobs WHERE id=?", g.job.ID)).Scan(&code); e != nil || code != "TRAINING_INPUT_UNAVAILABLE" {
			t.Fatal(code, e)
		}
	})
	t.Run("recipe_model_and_attempt_limits", func(t *testing.T) {
		g := dispatchSetup(t)
		g.owner.Config.Training.WorkerSourceSHA256 = strings.Repeat("e", 64)
		dispatchOutcome(t, g, false, "")
		dispatchWant(t, g, "queued", "TRAINING_RECIPE_UNAVAILABLE", 0, 0, 0)
		g.owner.Config.Training.WorkerSourceSHA256 = strings.Repeat("c", 64)
		g.exec(t, "UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", g.input.ModelID)
		dispatchDue(t, g)
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "failed", "", 0, 0, 0)
		// Explicit retained exhausted-attempt envelope; no attempt is executed.
		previous := g.attempt(t, 1)
		policy := g.owner.Config.Policy()
		policy.Training.MaxAttempts = 1
		if e := policy.Validate(); e != nil {
			t.Fatal(e)
		}
		g.owner.Config.Runtime = &policy
		g.exec(t, "UPDATE training_jobs SET state='queued',error_code=NULL,finished_at=NULL,attempt_number=1,current_attempt_id=$1,next_dispatch_at=clock_timestamp() WHERE id=$2", "UPDATE training_jobs SET state='queued',error_code=NULL,finished_at=NULL,attempt_number=1,current_attempt_id=?,next_dispatch_at=UTC_TIMESTAMP(6) WHERE id=?", previous, g.job.ID)
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "failed", "", 1, 0, 0)
		var code string
		if e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT error_code FROM training_jobs WHERE id=$1", "SELECT error_code FROM training_jobs WHERE id=?", g.job.ID)).Scan(&code); e != nil || code != "TRAINING_ATTEMPTS_EXHAUSTED" {
			t.Fatal("actual configured attempt ceiling", code, e)
		}
	})
	t.Run("shared_physical_owner_and_savepoint_contention", func(t *testing.T) {
		g := dispatchSetup(t)
		other, e := g.create(uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		id := uuid.NewString()
		g.seed(t, "gpu_leases", map[string]any{"id": id, "node_id": g.input.NodeID, "gpu_uuid": g.input.GPUUUID, "training_job_id": other.ID, "revision": 1, "state": "held"})
		dispatchOutcome(t, g, false, "")
		dispatchWant(t, g, "queued", "TRAINING_RESOURCE_BUSY", 0, 0, 0)
		var owner, state string
		if e := g.f.Store.QueryRow(g.ctx, g.statement("SELECT training_job_id::text,state FROM gpu_leases WHERE id=$1", "SELECT training_job_id,state FROM gpu_leases WHERE id=?", id)).Scan(&owner, &state); e != nil || owner != other.ID || state != "held" {
			t.Fatal("physical owner changed", owner, state, e)
		}
		// Released exact owner may be reused; a held exact owner never is.
		g.exec(t, "UPDATE gpu_leases SET state='released' WHERE id=$1", "UPDATE gpu_leases SET state='released' WHERE id=?", id)
		if _, e := g.s.Cancel(g.ctx, g.a, g.f.Workspace, other.ID, training.ActionRequest{ExpectedRevision: other.Revision, Reason: "Remove competing synthetic queue intent"}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		dispatchDue(t, g)
		dispatchOutcome(t, g, true, "")
		dispatchWant(t, g, "preparing", "", 1, 1, 1)
	})
	t.Run("node_busy_rolls_back_reservation", func(t *testing.T) {
		g := dispatchSetup(t)
		g.seed(t, "node_tasks", map[string]any{"id": uuid.NewString(), "node_id": g.input.NodeID, "operation": agent.CollectFacts, "state": "pending", "idempotency_key": uuid.NewString(), "requested_by": g.f.User, "deadline_at": time.Now().UTC().Add(time.Minute)})
		dispatchOutcome(t, g, false, "")
		dispatchWant(t, g, "queued", "NODE_BUSY", 0, 0, 0)
	})
	t.Run("concurrent_dispatch_one_attempt", func(t *testing.T) {
		g := dispatchSetup(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		worked := 0
		var failures []error
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				v, e := g.s.DispatchNext(g.ctx)
				mu.Lock()
				defer mu.Unlock()
				if v {
					worked++
				}
				if e != nil && e.Error() != "TRAINING_ATTEMPT_STALE" {
					failures = append(failures, e)
				}
			}()
		}
		close(start)
		wg.Wait()
		if worked != 1 || len(failures) != 0 {
			t.Fatal("one current attempt winner", worked, failures)
		}
		dispatchWant(t, g, "preparing", "", 1, 1, 1)
	})
	t.Run("atomic_audit_rollback_unknown_false_and_recovery", func(t *testing.T) {
		g := dispatchSetup(t)
		fault := &dispatchFailureStore{Store: g.f.Store}
		x := trainingOperatorWrapped(g, fault)
		dispatchOutcome(t, x, false, "STORAGE_UNAVAILABLE")
		if fault.hit != 1 {
			t.Fatal("expected actual audit boundary fault", fault.hit)
		}
		dispatchWant(t, g, "queued", "", 0, 0, 0)
		x = trainingOperatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 2, unknown: true})
		dispatchOutcome(t, x, false, string(transaction.CommitUnknown))
		dispatchWant(t, g, "preparing", "", 1, 1, 1)
		dispatchOutcome(t, g, false, "")
		attempts, e := g.s.Attempts(g.ctx, g.a, g.f.Workspace, g.job.ID, url.Values{})
		if e != nil || len(attempts.Items) != 1 {
			t.Fatal("unknown outcome readback", e)
		}
	})
	t.Run("cancelled_candidate_context_and_component_fences", func(t *testing.T) {
		g := dispatchSetup(t)
		x := trainingOperatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 2, before: func() {
			if _, e := g.cancel(g.job.Revision); e != nil {
				t.Fatal(e)
			}
		}})
		dispatchOutcome(t, x, false, "")
		dispatchWant(t, g, "cancelled", "", 0, 0, 0)
		j, e := g.create(uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		g.job = j
		wrong := *g.s.Inference
		copyNode := *g.s.Nodes
		wrong.Nodes = &copyNode
		s := *g.s
		s.Inference = &wrong
		x = g
		x.s = &s
		dispatchOutcome(t, x, false, "TRAINING_STORAGE_MISMATCH")
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		v, e := g.s.DispatchNext(ctx)
		if v || e == nil || e.Error() != string(transaction.Cancelled) {
			t.Fatal("cancelled dispatch", v, e)
		}
		ctx, cancel = context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		v, e = g.s.DispatchNext(ctx)
		if v || e == nil || e.Error() != string(transaction.Timeout) {
			t.Fatal("expired dispatch", v, e)
		}
		if !reflect.DeepEqual(g.s.Identity, g.owner) {
			t.Fatal("fixture owner coherence")
		}
	})
}
