//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The shared fixture owns actual CPU MiniLM/Qwen, native Rust and PostgreSQL.
// Its node/GPU observations are explicit control-plane test doubles, never GPU
// qualification. Every workflow below uses the real application service path.
func verifyActualWorkflows(t *testing.T, ctx context.Context, c scenario, handler *identityhttp.Handler, base knowledge.Base, alias distribution.Alias, document knowledge.Document, barrier *atomic.Pointer[completionBarrier], fault *atomic.Int32, control *httptest.Server, certificate tls.Certificate, caFile, root string, quiesceMachine func(), modelCalls *atomic.Int64) {
	t.Helper()
	accounts := c.s.Identity
	accounts.Config.Workflows.Enabled = true
	policy := accounts.Config.Policy()
	policy.HTTP.ShutdownSeconds = 30
	accounts.Config.Runtime = &policy
	{
		public, _, e := ed25519.GenerateKey(nil)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := wire.KeyID(public)
		accounts.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
		if e = handler.Plugins.Service.Start(ctx); e != nil {
			t.Fatal(e)
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if e := handler.Plugins.Service.Stop(cleanup); e != nil {
				t.Error(e)
			}
		}()
	}
	s := handler.Workflows.Service
	if e := s.Start(ctx); e != nil {
		t.Fatal("actual workflow start", e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if e := s.Stop(cleanup); e != nil {
			t.Error("actual workflow cleanup", e)
		}
	}()
	workspace := c.p.Me.Workspace.ID
	draft, e := s.Create(ctx, c.p, workflows.CreateRequest{WorkspaceID: workspace, Name: "Actual Caf\u00e9 \u5de5\u4f5c\u6d41 <img>", Reason: "Synthetic actual workflow acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var graph workflows.Graph
	if json.Unmarshal(draft.DraftGraph, &graph) != nil {
		t.Fatal("actual draft graph")
	}
	for i := range graph.Nodes {
		switch graph.Nodes[i].Kind {
		case workflows.RetrievalNode:
			graph.Nodes[i].Configuration, _ = json.Marshal(workflows.RetrievalConfiguration{BaseID: base.ID, TopK: 1, MinimumScore: 0.1})
		case workflows.AnswerNode:
			graph.Nodes[i].Configuration, _ = json.Marshal(workflows.AnswerConfiguration{AliasID: alias.ID, Instructions: "Answer in one short sentence using the supplied evidence and its citation."})
		}
	}
	save := func(enabled bool) workflows.Workflow {
		t.Helper()
		v, e := s.Update(ctx, c.p, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: enabled, ExpectedRevision: draft.Revision, Reason: "Actual saved graph revision"}, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		draft = v
		return v
	}
	save(true)
	validation, e := s.Validate(ctx, c.p, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual resource validation"})
	if e != nil || !validation.Valid {
		t.Fatal("actual graph validation", validation, e)
	}
	publishInput := workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual immutable publication"}
	publishKey := uuid.NewString()
	version, e := s.Publish(ctx, c.p, workspace, draft.ID, publishInput, publishKey, uuid.NewString())
	if e != nil {
		t.Fatal("actual publish", e)
	}
	replay, e := s.Publish(ctx, c.p, workspace, draft.ID, publishInput, publishKey, uuid.NewString())
	if e != nil || replay.ID != version.ID {
		t.Fatal("actual publish replay", e)
	}
	// Publishing advances metadata revision; retrieve authoritative current state.
	draft, e = s.Get(ctx, c.p, workspace, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	conversation, e := s.CreateConversation(ctx, c.p, workspace, draft.ID, workflows.ConversationRequest{VersionID: version.ID, Reason: "Actual pinned conversation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	wait := func(id string) workflows.Run {
		t.Helper()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(90 * time.Second)
		defer deadline.Stop()
		for {
			r, e := s.GetRun(ctx, c.p, workspace, draft.ID, id)
			if e != nil {
				t.Fatal("actual run read", e)
			}
			if r.State != "pending" && r.State != "running" {
				return r
			}
			select {
			case <-ctx.Done():
				t.Fatal("actual workflow overall deadline")
			case <-deadline.C:
				t.Fatal("actual workflow run deadline")
			case <-ticker.C:
			}
		}
	}
	question := "Which city does the project launch in? Answer in one short sentence with a source citation."
	checkAnswer := func(run workflows.Run) {
		t.Helper()
		if run.State != "succeeded" || !run.ResultAvailable || run.Answer == nil || !strings.Contains(*run.Answer, "Lisbon") || !strings.Contains(*run.Answer, "[S1]") || len(run.Sources) != 1 || run.Sources[0].SourceID != document.VisibleSource.ID || run.Usage == nil || run.Usage.PromptTokens < 1 || run.Usage.CompletionTokens < 1 || run.Usage.TotalTokens != run.Usage.PromptTokens+run.Usage.CompletionTokens {
			t.Fatal("actual durable model answer", run.State, workflowTestCode(run.ErrorCode), workflowTestCode(run.ResultUnavailableCode))
		}
		if len(run.Nodes) != 4 {
			t.Fatal("missing real node traces")
		}
		for i, kind := range []string{"start", "retrieval", "answer", "end"} {
			n := run.Nodes[i]
			if n.Kind != kind || n.State != "succeeded" || n.StartedAt == nil || n.FinishedAt == nil {
				t.Fatal("actual ordered trace", kind, n.State)
			}
		}
	}
	noKeys := func() {
		t.Helper()
		var count int
		if e := accounts.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE revoked_at IS NULL AND expires_at>clock_timestamp()").Scan(&count); e != nil || count != 0 {
			t.Fatal("ordinary transient key leaked", e, count)
		}
	}
	input := workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual durable saved-draft debug"}
	key := uuid.NewString()
	admitted, e := s.Debug(ctx, c.p, workspace, draft.ID, input, key, uuid.NewString())
	if e != nil || admitted.ID == "" || admitted.State != "pending" || admitted.ResultAvailable {
		t.Fatal("durable admission was not distinct from success", e)
	}
	result := wait(admitted.ID)
	checkAnswer(result)
	noKeys()
	var before, after int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&before); e != nil {
		t.Fatal(e)
	}
	again, e := s.Debug(ctx, c.p, workspace, draft.ID, input, key, uuid.NewString())
	if e != nil || again.ID != admitted.ID || again.State != "succeeded" {
		t.Fatal("actual debug replay", e)
	}
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&after); e != nil || before != after {
		t.Fatal("idempotency reinvoked real Rust model", e)
	}
	turn, e := s.RunConversation(ctx, c.p, workspace, draft.ID, conversation.ID, workflows.RunRequest{Question: question, Reason: "Actual version-pinned turn"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	turn = wait(turn.ID)
	checkAnswer(turn)
	if turn.VersionID == nil || *turn.VersionID != version.ID {
		t.Fatal("conversation lost publication pin")
	}
	save(true)
	next, e := s.Publish(ctx, c.p, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual subsequent publication"}, uuid.NewString(), uuid.NewString())
	if e != nil || next.ID == version.ID || next.VersionNumber != 2 {
		t.Fatal("actual version progression", e)
	}
	draft, e = s.Get(ctx, c.p, workspace, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	pinned, e := s.Conversation(ctx, c.p, workspace, draft.ID, conversation.ID)
	if e != nil || pinned.VersionID != version.ID {
		t.Fatal("existing conversation changed version", e)
	}
	other := c.user(t, "workflow-private-reader@example.invalid")
	if _, e = s.GetRun(ctx, other, workspace, draft.ID, result.ID); e == nil || e.Error() != "WORKFLOW_RUN_NOT_FOUND" {
		t.Fatal("another member read private run", e)
	}
	verifyWorkflowElapsedModelDeadline(t, ctx, c, handler, graph, question, barrier)
	verifyWorkflowViewerAuthority(t, ctx, c, s, other, draft.ID, next.ID, graph, question, barrier)
	// Current unchanged source history survives real reindex generation changes.
	fresh, e := handler.Knowledge.Service.GetBase(ctx, c.p, workspace, base.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = handler.Knowledge.Service.Rebuild(ctx, c.p, workspace, base.ID, knowledge.RevisionReason{ExpectedRevision: fresh.Revision, Reason: "Actual unchanged source reindex"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := handler.Knowledge.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual reindex worker", e)
	}
	if e = handler.Knowledge.Service.ReconcileGeneration(ctx); e != nil {
		t.Fatal(e)
	}
	history, e := s.GetRun(ctx, c.p, workspace, draft.ID, result.ID)
	if e != nil || !history.ResultAvailable {
		t.Fatal("unchanged original history lost after reindex", e)
	}
	// Explicit final-audit failure must never persist a successful model answer.
	if _, e = accounts.DB.Exec(ctx, `CREATE FUNCTION workflow_success_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='workflow.run_succeeded' THEN RAISE EXCEPTION 'SYNTHETIC_WORKFLOW_SUCCESS_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$;CREATE TRIGGER workflow_success_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION workflow_success_test_audit()`); e != nil {
		t.Fatal(e)
	}
	rejected, e := s.Debug(ctx, c.p, workspace, draft.ID, workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual final-audit rollback"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	rejected = wait(rejected.ID)
	if rejected.State != "failed" || rejected.Answer != nil || rejected.ResultAvailable || rejected.ErrorCode == nil || *rejected.ErrorCode != "STORAGE_UNAVAILABLE" {
		t.Fatal("failed audit committed answer", rejected.State, workflowTestCode(rejected.ErrorCode))
	}
	if _, e = accounts.DB.Exec(ctx, "DROP TRIGGER workflow_success_test_audit ON audit_events;DROP FUNCTION workflow_success_test_audit()"); e != nil {
		t.Fatal(e)
	}
	noKeys()
	// The transport barrier pauses before actual CPU forwarding; cancellation must
	// prevent success without fabricating a generated answer or releasing unknown
	// provider capacity. All actual ordinary keys must still be revoked.
	held := &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	barrier.Store(held)
	cancelled, e := s.Debug(ctx, c.p, workspace, draft.ID, workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual in-flight cancellation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-held.entered:
	case <-ctx.Done():
		t.Fatal("cancellation did not reach transport barrier")
	}
	if _, e = s.CancelRun(ctx, c.p, workspace, draft.ID, cancelled.ID, "Actual owner cancellation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	close(held.release)
	barrier.Store(nil)
	cancelled = wait(cancelled.ID)
	if cancelled.State != "cancelled" || cancelled.Answer != nil || cancelled.ErrorCode == nil || *cancelled.ErrorCode != "WORKFLOW_RUN_CANCELLED" {
		t.Fatal("cancelled run committed success", cancelled.State, workflowTestCode(cancelled.ErrorCode))
	}
	noKeys()
	verifyWorkflowFaultsAndInterleavings(t, ctx, c, s, &draft, &graph, conversation, question, barrier, fault)
	if os.Getenv("PROJECT_ONE_RAG_BROWSER") == "1" {
		leaf, e := x509.ParseCertificate(certificate.Certificate[0])
		if e != nil {
			t.Fatal(e)
		}
		pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)

		for _, locale := range []string{"en", "zh-CN"} {
			output := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "workflow-browser-"+locale)
			if e = os.Mkdir(output, 0700); e != nil {
				t.Fatal(e)
			}
			nonce := uuid.NewString()
			payload, _ := json.Marshal(map[string]any{"admin": control.URL, "portal": accounts.Config.Server.PortalOrigin, "admin_email": "admin@example.invalid", "admin_password": c.password, "base_id": base.ID, "workspace_id": workspace, "alias_id": alias.ID, "output": output, "spki": base64.StdEncoding.EncodeToString(pin[:]), "browser_locale": locale, "workflow_name": "Workflow UI Caf\u00e9 <img> " + locale, "cancellation_nonce": nonce, "admission_policy": map[string]int{"csrf_count": policy.Rates.CSRFCount, "csrf_window_seconds": policy.Rates.CSRFWindowSeconds}})
			file := filepath.Join(root, "workflow-browser-"+locale+"-input.json")
			if e = os.WriteFile(file, payload, 0600); e != nil {
				t.Fatal(e)
			}
			operation, cancel := context.WithTimeout(ctx, 120*time.Second+time.Duration(policy.Rates.CSRFWindowSeconds)*time.Second)
			admissionJoined := workflowBrowserAdmissionFence(t, operation, c, output, nonce, modelCalls)
			joined := workflowUICancellationFence(t, operation, output, nonce, barrier)
			adminCancellation := filepath.Join(output, "admin-cancellation")
			if e = os.Mkdir(adminCancellation, 0700); e != nil {
				cancel()
				<-joined
				t.Fatal(e)
			}
			adminJoined := workflowUICancellationFence(t, operation, adminCancellation, nonce, barrier)
			command := exec.CommandContext(operation, "node", filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "tests/acceptance/workflows/workflow-flow.mjs"), file)
			command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			e = command.Run()
			cancel()
			<-joined
			<-adminJoined
			<-admissionJoined
			if e != nil {
				t.Fatal("actual dual React workflow browser", locale, e)
			}
		}
	}
	verifyWorkflowRuntimeRole(t, ctx, c, handler, graph, question, document)
	if os.Getenv("PROJECT_ONE_WORKFLOW_OPERATIONS") == "1" {
		verifyWorkflowNativeOperations(t, ctx, c, handler, draft, conversation, result, alias, barrier, quiesceMachine)
	}
	// Actual original deletion withholds previously committed private results.
	current, e := handler.Knowledge.Service.GetDocument(ctx, c.p, workspace, base.ID, document.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = handler.Knowledge.Service.Delete(ctx, c.p, workspace, base.ID, document.ID, knowledge.RevisionReason{ExpectedRevision: current.Revision, Reason: "Actual original revocation after saved answer"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	history, e = s.GetRun(ctx, c.p, workspace, draft.ID, result.ID)
	if e != nil || history.ResultAvailable || history.Answer != nil || history.ResultUnavailableCode == nil {
		t.Fatal("revoked source history leaked", e)
	}
	noKeys()
	var audit string
	if e = accounts.DB.QueryRow(ctx, "SELECT COALESCE(json_agg(row_to_json(a))::text,'[]') FROM audit_events a WHERE action LIKE 'workflow.%'").Scan(&audit); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(audit, question) || strings.Contains(audit, "Lisbon") || strings.Contains(audit, "pk1.") {
		t.Fatal("workflow audit retained private payload")
	}
	receipt, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_durable_workflow_cpu_rust_current_authority", "debug_run": result.ID, "conversation_run": turn.ID, "cancelled_run": cancelled.ID, "immutable_versions": true, "actual_reindex_history": true, "private_history_isolation": true, "audit_failure_no_answer": true, "source_revocation_withholding": true, "all_transient_keys_revoked": true, "actual_dml_only_runtime": true, "actual_cpu": true, "synthetic_node_gpu_observation": true, "target_qualification": false}, "", "  ")
	if e = os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "actual-workflows.json"), append(receipt, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}

func workflowTestCode(value *string) string {
	if value == nil {
		return "NONE"
	}
	return *value
}

// A fixed private file handshake controls only this fixture's already declared
// transport barrier. It adds no product route, provider capability or test timing
// assumption; every owned watcher/barrier is released and joined on failure.
func workflowUICancellationFence(t *testing.T, ctx context.Context, root, nonce string, barrier *atomic.Pointer[completionBarrier]) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var held *completionBarrier
		defer func() {
			if held != nil {
				select {
				case <-held.release:
				default:
					close(held.release)
				}
				barrier.CompareAndSwap(held, nil)
				raw, _ := json.Marshal(map[string]string{"nonce": nonce})
				if e := os.WriteFile(filepath.Join(root, "ui-released.json"), raw, 0600); e != nil {
					t.Error("UI release acknowledgment")
				}
			}
		}()
		read := func(name, operation string) bool {
			raw, e := os.ReadFile(filepath.Join(root, name))
			if os.IsNotExist(e) {
				return false
			}
			if e != nil || len(raw) > 256 {
				t.Error("bounded UI cancellation marker")
				return false
			}
			var v struct {
				Nonce     string `json:"nonce"`
				Operation string `json:"operation"`
			}
			if json.Unmarshal(raw, &v) != nil || v.Nonce != nonce || v.Operation != operation {
				t.Error("exact UI cancellation marker")
				return false
			}
			return true
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if held == nil && read("ui-hold.json", "hold") {
					held = &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
					if !barrier.CompareAndSwap(nil, held) {
						t.Error("UI transport barrier already owned")
						return
					}
					raw, _ := json.Marshal(map[string]string{"nonce": nonce})
					if e := os.WriteFile(filepath.Join(root, "ui-held.json"), raw, 0600); e != nil {
						t.Error("UI barrier acknowledgment")
						return
					}
				}
				if held != nil && read("ui-release.json", "release") {
					return
				}
			}
		}
	}()
	return done
}
