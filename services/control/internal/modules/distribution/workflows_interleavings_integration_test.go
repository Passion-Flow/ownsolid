//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
)

func verifyWorkflowFaultsAndInterleavings(t *testing.T, ctx context.Context, c scenario, s *workflows.Service, draft *workflows.Workflow, graph *workflows.Graph, conversation workflows.Conversation, question string, barrier *atomic.Pointer[completionBarrier], fault *atomic.Int32) {
	t.Helper()
	w := c.p.Me.Workspace.ID
	count := func(table string) int {
		t.Helper()
		var value int
		if table != "gateway_requests" && table != "workflow_runs" {
			t.Fatal("fixed count table required")
		}
		if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	wait := func(id string) workflows.Run {
		t.Helper()
		operation, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			r, e := s.GetRun(operation, c.p, w, draft.ID, id)
			if e != nil {
				t.Fatal(e)
			}
			if r.State != "pending" && r.State != "running" {
				return r
			}
			select {
			case <-operation.Done():
				t.Fatal("controlled workflow terminal deadline")
			case <-ticker.C:
			}
		}
	}
	save := func(enabled bool) {
		t.Helper()
		v, e := s.Update(ctx, c.p, w, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: *graph, Enabled: enabled, ExpectedRevision: draft.Revision, Reason: "Controlled workflow revision/authority interleaving"}, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		*draft = v
	}
	debug := func() workflows.Run {
		t.Helper()
		r, e := s.Debug(ctx, c.p, w, draft.ID, workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual controlled workflow failure"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	failed := func(run workflows.Run, kind, code string) {
		t.Helper()
		if run.State != "failed" || run.Answer != nil || run.ResultAvailable || workflowTestCode(run.ErrorCode) != code || len(run.Nodes) != 4 {
			t.Fatal("incorrect safe node failure", run.State, workflowTestCode(run.ErrorCode))
		}
		seen := false
		for _, node := range run.Nodes {
			if node.Kind == kind {
				seen = true
				if node.State != "failed" || workflowTestCode(node.ErrorCode) != code {
					t.Fatal("failing node missing safe cause", node.Kind, node.State)
				}
			} else if seen && node.State != "not_started" {
				t.Fatal("failed node fabricated downstream execution", node.Kind, node.State)
			}
		}
		if !seen {
			t.Fatal("missing failing kind")
		}
	}
	// Actual encoder/vector search is required to produce no matching context.
	for i := range graph.Nodes {
		if graph.Nodes[i].Kind == workflows.RetrievalNode {
			var input workflows.RetrievalConfiguration
			if json.Unmarshal(graph.Nodes[i].Configuration, &input) != nil {
				t.Fatal("retrieval configuration")
			}
			input.MinimumScore = 1
			graph.Nodes[i].Configuration, _ = json.Marshal(input)
		}
	}
	save(true)
	before := count("gateway_requests")
	failed(wait(debug().ID), "retrieval", "KNOWLEDGE_NO_CONTEXT")
	if count("gateway_requests") != before {
		t.Fatal("empty actual context invoked model")
	}
	for i := range graph.Nodes {
		if graph.Nodes[i].Kind == workflows.RetrievalNode {
			var input workflows.RetrievalConfiguration
			if json.Unmarshal(graph.Nodes[i].Configuration, &input) != nil {
				t.Fatal("retrieval configuration")
			}
			input.MinimumScore = 0.1
			graph.Nodes[i].Configuration, _ = json.Marshal(input)
		}
	}
	save(true)
	fault.Store(1)
	failed(wait(debug().ID), "answer", "KNOWLEDGE_GATEWAY_RESPONSE_INVALID")
	if fault.Load() != 0 {
		t.Fatal("controlled backend failure was not exercised")
	}
	fault.Store(2)
	failed(wait(debug().ID), "answer", "KNOWLEDGE_INVALID_CITATIONS")
	if fault.Load() != 0 {
		t.Fatal("actual CPU response citation corruption was not exercised")
	}
	hold := func() (*completionBarrier, func()) {
		t.Helper()
		held := &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
		barrier.Store(held)
		var once sync.Once
		release := func() { once.Do(func() { close(held.release); barrier.CompareAndSwap(held, nil) }) }
		t.Cleanup(release)
		return held, release
	}
	entered := func(held *completionBarrier) {
		t.Helper()
		select {
		case <-held.entered:
		case <-ctx.Done():
			t.Fatal("controlled completion transport not reached")
		}
	}
	// Two actual callers race distinct turns in one conversation. Only one durable
	// admission may win; the other must see the persisted active-turn constraint.
	held, release := hold()
	defer release()
	type outcome struct {
		run workflows.Run
		err error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r, e := s.RunConversation(ctx, c.p, w, draft.ID, conversation.ID, workflows.RunRequest{Question: question, Reason: "Concurrent actual conversation admission"}, uuid.NewString(), uuid.NewString())
			results <- outcome{r, e}
		}()
	}
	close(start)
	var winner workflows.Run
	busy := 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			if winner.ID != "" {
				t.Fatal("two active conversation turns")
			}
			winner = result.run
		} else if result.err.Error() == "WORKFLOW_CONVERSATION_BUSY" {
			busy++
		} else {
			t.Fatal("concurrent turn error", result.err)
		}
	}
	if winner.ID == "" || busy != 1 {
		t.Fatal("missing exact admission/busy outcomes")
	}
	entered(held)
	second := debug()
	_, e := s.Debug(ctx, c.p, w, draft.ID, workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Refuse process and Workspace overflow"}, uuid.NewString(), uuid.NewString())
	if e == nil || e.Error() != "WORKFLOW_CAPACITY_EXCEEDED" {
		t.Fatal("active capacity not bounded", e)
	}
	for _, run := range []workflows.Run{winner, second} {
		if _, e = s.CancelRun(ctx, c.p, w, draft.ID, run.ID, "Cancel only admitted controlled test work", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	release()
	for _, run := range []workflows.Run{winner, second} {
		r := wait(run.ID)
		if r.State != "cancelled" || r.Answer != nil || r.ResultAvailable {
			t.Fatal("concurrent cancellation committed success", r.State)
		}
	}
	// A real request is held after retrieval/model admission while another writer
	// changes exactly the saved debug revision. The final commit must be denied.
	held, release = hold()
	defer release()
	changed := debug()
	entered(held)
	save(true)
	release()
	r := wait(changed.ID)
	if r.State != "failed" || r.Answer != nil || workflowTestCode(r.ErrorCode) != "WORKFLOW_DRAFT_CHANGED" {
		t.Fatal("stale debug committed", r.State, workflowTestCode(r.ErrorCode))
	}
	held, release = hold()
	defer release()
	disabled := debug()
	entered(held)
	save(false)
	release()
	r = wait(disabled.ID)
	code := workflowTestCode(r.ErrorCode)
	if r.State == "succeeded" || r.State == "pending" || r.State == "running" || r.Answer != nil || r.ResultAvailable || !strings.Contains("|WORKFLOW_DISABLED|WORKFLOW_AUTHORITY_CHANGED|WORKFLOW_RUN_CANCELLED|", "|"+code+"|") {
		t.Fatal("disabled workflow committed", r.State, code)
	}
	save(true)
	var keys int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE revoked_at IS NULL AND expires_at>clock_timestamp()").Scan(&keys); e != nil || keys != 0 {
		t.Fatal("fault/interleaving ordinary key leaked", e, keys)
	}
}
