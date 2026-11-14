//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
)

// This is a real elapsed application deadline at an observed native Rust/backend
// request. The barrier supplies no model answer, failure response or clock mock.
func verifyWorkflowElapsedModelDeadline(t *testing.T, ctx context.Context, c scenario, original *identityhttp.Handler, graph workflows.Graph, question string, barrier *atomic.Pointer[completionBarrier]) {
	t.Helper()
	accounts := *c.s.Identity
	policy := accounts.Config.Policy()
	policy.Workflows.AnswerSeconds = 5
	accounts.Config.Runtime = &policy
	handler := identityhttp.New(&accounts)
	handler.Knowledge.Service.Processor = original.Knowledge.Service.Processor
	s := handler.Workflows.Service
	if e := s.Start(ctx); e != nil {
		t.Fatal("elapsed deadline service start", e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if e := s.Stop(cleanup); e != nil {
			t.Error("elapsed deadline owner join", e)
		}
	}()
	w := c.p.Me.Workspace.ID
	draft, e := s.Create(ctx, c.p, workflows.CreateRequest{WorkspaceID: w, Name: "Actual elapsed model deadline", Reason: "Separate immutable five-second test execution policy"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Update(ctx, c.p, w, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "Actual configured model deadline draft"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	held := &completionBarrier{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	barrier.Store(held)
	defer func() {
		select {
		case <-held.release:
		default:
			close(held.release)
		}
		barrier.CompareAndSwap(held, nil)
	}()
	started := time.Now()
	run, e := s.Debug(ctx, c.p, w, draft.ID, workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual elapsed model deadline without manual cancellation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	operation, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case <-held.entered:
	case <-operation.Done():
		t.Fatal("elapsed deadline never reached real model request")
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, e = s.GetRun(operation, c.p, w, draft.ID, run.ID)
		if e != nil {
			t.Fatal("elapsed deadline durable read", e)
		}
		if run.State != "pending" && run.State != "running" {
			break
		}
		select {
		case <-operation.Done():
			t.Fatal("actual elapsed model deadline did not terminate")
		case <-ticker.C:
		}
	}
	elapsed := time.Since(started)
	if elapsed < 5*time.Second || run.State != "failed" || workflowTestCode(run.ErrorCode) != "WORKFLOW_RUN_TIMEOUT" || run.Answer != nil || run.Usage != nil || run.ResultAvailable || len(run.Sources) != 0 || len(run.Nodes) != 4 {
		t.Fatal("actual elapsed deadline did not fail safely", run.State, workflowTestCode(run.ErrorCode), elapsed)
	}
	for i, node := range run.Nodes {
		expected := []string{"succeeded", "succeeded", "failed", "not_started"}[i]
		if node.State != expected || i == 2 && (workflowTestCode(node.ErrorCode) != "WORKFLOW_RUN_TIMEOUT" || node.StartedAt == nil || node.FinishedAt == nil || node.FinishedAt.Sub(*node.StartedAt) < 4900*time.Millisecond) {
			t.Fatal("elapsed deadline trace fabricated progress", node.Kind, node.State)
		}
	}
	// Release only after observing the durable timeout. Join the actual held HTTP
	// handler before allowing another request to use this transport fixture.
	close(held.release)
	barrier.CompareAndSwap(held, nil)
	select {
	case <-held.finished:
	case <-operation.Done():
		t.Fatal("elapsed deadline backend handler not drained")
	}
	var keys int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE revoked_at IS NULL AND expires_at>clock_timestamp()").Scan(&keys); e != nil || keys != 0 {
		t.Fatal("elapsed deadline retained an ordinary transient key", e, keys)
	}
	raw, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_elapsed_workflow_model_deadline", "run_id": run.ID, "answer_budget_seconds": 5, "test_gateway_idle_budget_seconds": 15, "elapsed_milliseconds": elapsed.Milliseconds(), "real_transport_barrier_entered": true, "manual_cancellation": false, "durable_error": "WORKFLOW_RUN_TIMEOUT", "no_private_answer_or_usage": true, "downstream_not_started": true, "all_transient_keys_revoked": true, "held_handler_joined": true, "provider_capacity_release_claimed": false, "target_qualification": false}, "", "  ")
	if e = os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "workflow-elapsed-deadline.json"), append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
