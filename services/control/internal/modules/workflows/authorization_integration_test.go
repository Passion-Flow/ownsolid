//go:build workflow_database_integration && knowledge_encoder_integration

package workflows

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM13ActualViewerPrivateOwnershipAndEveryOperationScopeGuards(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	email := "workflow-scope-user@example.invalid"
	invitation, e := account.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	userPassword := fixture.Password()
	if e = account.Register(ctx, fixture.InvitationToken(t, account, invitation.ID), "Synthetic workflow scope user", userPassword, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	a := fixture.Login(t, account, email, userPassword, "portal")
	w := &workspaces.Service{DB: account.DB, Identity: account}
	owned, e := w.Create(ctx, admin, "Owned workflow permission fixture", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.AddMember(ctx, admin, owned.ID, "admin@example.invalid", "owner", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	foreign, e := w.Create(ctx, admin, "Foreign workflow permission fixture", admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	scratch := t.TempDir()
	if os.Chmod(scratch, 0700) != nil {
		t.Fatal("private scope scratch")
	}
	account.Config.Workflows.Enabled = true
	account.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
	policy := account.Config.Policy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 30
	account.Config.Runtime = &policy
	d := &distribution.Service{DB: account.DB, Identity: account, Workspaces: w}
	k := &knowledge.Service{DB: account.DB, Identity: account, Workspaces: w, Distribution: d}
	if e = k.OpenConfiguredProcessor(ctx); e != nil {
		t.Fatal(e)
	}
	defer k.Processor.Close()
	s := &Service{DB: account.DB, Identity: account, Workspaces: w, Knowledge: k, Distribution: d}
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := s.Stop(cleanup); e != nil {
			t.Error(e)
		}
	}()
	workspace := owned.ID
	input := CreateRequest{WorkspaceID: workspace, Name: "Scope fixture workflow", Reason: "Synthetic actual current permission guards"}
	key := uuid.NewString()
	draft, e := s.Create(ctx, a, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var graph Graph
	if json.Unmarshal(draft.DraftGraph, &graph) != nil {
		t.Fatal("draft graph")
	}
	// Explicit SQL publication/conversation/interrupted-run fixtures isolate
	// authorization mechanics only. They are not model or publication proof.
	version, conversation, run := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES($1,$2,$3,$4,1,1,$5,$6,$7)`, version, owned.TenantID, workspace, draft.ID, draft.DraftGraph, draft.DraftSHA256, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = account.DB.Exec(ctx, `UPDATE workflows SET current_version_id=$2 WHERE id=$1`, draft.ID, version); e != nil {
		t.Fatal(e)
	}
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id,turn_count) VALUES($1,$2,$3,$4,$5,$6,1)`, conversation, owned.TenantID, workspace, draft.ID, version, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,turn_number,version_id,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at,finished_at,error_code) SELECT $1,f.tenant_id,f.workspace_id,f.id,$2,$3,1,$4,f.execution_authority,m.authority_id,'portal',f.draft_graph,f.draft_sha256,'interrupted','Synthetic private scope question',clock_timestamp()+interval '1 minute',clock_timestamp(),'WORKFLOW_RUN_INTERRUPTED' FROM workflows f JOIN workspace_memberships m ON m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$5`, run, a.Me.User.ID, conversation, version, draft.ID); e != nil {
		t.Fatal(e)
	}
	for _, node := range graph.Nodes {
		if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES($1,$2,$3,$4,$5,$6,'not_started')`, owned.TenantID, workspace, draft.ID, run, node.ID, node.Kind); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.GetRun(ctx, a, workspace, draft.ID, run); e != nil {
		t.Fatal("owner interrupted fixture read", e)
	}
	if _, e = s.GetRun(ctx, admin, workspace, draft.ID, run); e == nil || e.Error() != "WORKFLOW_RUN_NOT_FOUND" {
		t.Fatal("metadata admin read another user's private run", e)
	}
	if _, e = s.Conversation(ctx, admin, workspace, draft.ID, conversation); e == nil || e.Error() != "WORKFLOW_CONVERSATION_NOT_FOUND" {
		t.Fatal("metadata admin read another user's conversation", e)
	}
	private, e := s.RunPage(ctx, admin, workspace, draft.ID, nil, url.Values{})
	if e != nil || len(private.Items) != 0 {
		t.Fatal("metadata admin listed private run", e)
	}
	var memberVersion int64
	if e = account.DB.QueryRow(ctx, "SELECT version FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", workspace, a.Me.User.ID).Scan(&memberVersion); e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseGraph(draft.DraftGraph, s.policy(), account.Config.Policy().Knowledge.MaximumTopK)
	if e != nil {
		t.Fatal(e)
	}
	execution := &runExecution{actor: a, tenant: owned.TenantID, workspace: workspace, workflow: draft.ID, id: uuid.NewString(), process: uuid.NewString(), lease: uuid.NewString(), request: uuid.NewString(), draftRevision: &draft.Revision, graph: parsed}
	if e = account.DB.QueryRow(ctx, `SELECT f.execution_authority::text,m.authority_id::text FROM workflows f JOIN workspace_memberships m ON m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$1`, draft.ID, a.Me.User.ID).Scan(&execution.authority, &execution.membership); e != nil {
		t.Fatal(e)
	}
	// Explicit running-row fixture isolates the actual continuation transaction;
	// it is not an admitted/model-executed run or performance evidence.
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,process_owner,lease_id,lease_expires_at,deadline_at,started_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'portal',$9,$10,'running','Synthetic current-editor fence',$11,$12,clock_timestamp()+interval '1 minute',clock_timestamp()+interval '1 minute',clock_timestamp())`, execution.id, owned.TenantID, workspace, draft.ID, a.Me.User.ID, draft.Revision, execution.authority, execution.membership, draft.DraftGraph, draft.DraftSHA256, execution.process, execution.lease); e != nil {
		t.Fatal(e)
	}
	viewer, e := w.ChangeMember(ctx, admin, workspace, a.Me.User.ID, "viewer", memberVersion, "Actual viewer downgrade", uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	continued := false
	e = s.withExecution(ctx, execution, false, func(pgx.Tx, workspaces.Workspace, workflowRecord, runRecord) error { continued = true; return nil })
	if e == nil || e.Error() != "ACCESS_DENIED" || continued {
		t.Fatal("in-flight draft debug retained editor authority after actual viewer downgrade", e, continued)
	}
	if e = s.finishFailure(ctx, execution, InterruptedState, "WORKFLOW_RUN_INTERRUPTED"); e != nil {
		t.Fatal("reduce owned debug fixture after authority loss", e)
	}
	visible, e := s.Get(ctx, a, workspace, draft.ID)
	if e != nil || visible.CanEdit || len(visible.DraftGraph) != 0 || visible.DraftSHA256 != "" {
		t.Fatal("viewer saw shared editor graph/instructions", e)
	}
	versions, e := s.Versions(ctx, a, workspace, draft.ID, url.Values{})
	if e != nil || len(versions.Items) != 1 || len(versions.Items[0].Graph) != 0 {
		t.Fatal("viewer saw immutable graph/instructions", e)
	}
	reason := RevisionReason{ExpectedRevision: draft.Revision, Reason: "Refuse current unauthorized workflow action"}
	checks := map[string]func() error{
		"configuration": func() error { _, e := s.Configuration(ctx, a, workspace); return e },
		"list":          func() error { _, e := s.List(ctx, a, workspace, url.Values{}); return e },
		"resolve":       func() error { _, e := s.ResolveWorkspace(ctx, a, draft.ID); return e },
		"get":           func() error { _, e := s.Get(ctx, a, workspace, draft.ID); return e },
		"create": func() error {
			_, e := s.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Refused workflow", Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
			return e
		},
		"create_replay": func() error { _, e := s.Create(ctx, a, input, key, uuid.NewString()); return e },
		"update": func() error {
			_, e := s.Update(ctx, a, workspace, draft.ID, UpdateRequest{Name: draft.Name, Graph: graph, Enabled: false, ExpectedRevision: draft.Revision, Reason: reason.Reason}, uuid.NewString())
			return e
		},
		"validate": func() error { _, e := s.Validate(ctx, a, workspace, draft.ID, reason); return e },
		"publish": func() error {
			_, e := s.Publish(ctx, a, workspace, draft.ID, reason, uuid.NewString(), uuid.NewString())
			return e
		},
		"versions": func() error { _, e := s.Versions(ctx, a, workspace, draft.ID, url.Values{}); return e },
		"debug": func() error {
			_, e := s.Debug(ctx, a, workspace, draft.ID, DebugRequest{Question: "Synthetic question", ExpectedRevision: draft.Revision, Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
			return e
		},
		"conversation_create": func() error {
			_, e := s.CreateConversation(ctx, a, workspace, draft.ID, ConversationRequest{VersionID: uuid.NewString(), Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
			return e
		},
		"conversation_list": func() error { _, e := s.Conversations(ctx, a, workspace, draft.ID, url.Values{}); return e },
		"conversation_get":  func() error { _, e := s.Conversation(ctx, a, workspace, draft.ID, conversation); return e },
		"conversation_delete": func() error {
			return s.DeleteConversation(ctx, a, workspace, draft.ID, conversation, reason, uuid.NewString())
		},
		"run": func() error {
			_, e := s.RunConversation(ctx, a, workspace, draft.ID, conversation, RunRequest{Question: "Synthetic question", Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
			return e
		},
		"run_get":               func() error { _, e := s.GetRun(ctx, a, workspace, draft.ID, run); return e },
		"run_list":              func() error { _, e := s.RunPage(ctx, a, workspace, draft.ID, nil, url.Values{}); return e },
		"conversation_run_list": func() error { _, e := s.RunPage(ctx, a, workspace, draft.ID, &conversation, url.Values{}); return e },
		"cancel": func() error {
			_, e := s.CancelRun(ctx, a, workspace, draft.ID, run, reason.Reason, uuid.NewString())
			return e
		},
	}
	for _, name := range []string{"create", "update", "validate", "publish", "debug"} {
		t.Run("viewer_"+name, func(t *testing.T) {
			e := checks[name]()
			if e == nil || e.Error() != "ACCESS_DENIED" {
				t.Fatal("viewer mutation allowed", e)
			}
		})
	}
	denyAll := func(phase string) {
		t.Helper()
		var before, after int
		if e = account.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM workflows)+(SELECT count(*) FROM workflow_versions)+(SELECT count(*) FROM workflow_conversations)+(SELECT count(*) FROM workflow_runs)+(SELECT count(*) FROM api_keys)+(SELECT count(*) FROM audit_events)").Scan(&before); e != nil {
			t.Fatal(e)
		}
		for name, check := range checks {
			t.Run(phase+"/"+name, func(t *testing.T) {
				if e := check(); e == nil {
					t.Fatal("unauthorized operation succeeded")
				}
			})
		}
		if e = account.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM workflows)+(SELECT count(*) FROM workflow_versions)+(SELECT count(*) FROM workflow_conversations)+(SELECT count(*) FROM workflow_runs)+(SELECT count(*) FROM api_keys)+(SELECT count(*) FROM audit_events)").Scan(&after); e != nil || before != after {
			t.Fatal("denied operation mutated data/authority/audit", e, before, after)
		}
	}
	workspace, input.WorkspaceID = foreign.ID, foreign.ID
	denyAll("foreign_workspace")
	workspace, input.WorkspaceID = owned.ID, owned.ID
	if _, e = w.ChangeMember(ctx, admin, workspace, a.Me.User.ID, "", viewer.Version, "Actual member removal", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	denyAll("removed_membership")
	if _, e = w.AddMember(ctx, admin, workspace, email, "viewer", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	current, e := w.Get(ctx, admin, workspace, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Update(ctx, admin, workspace, current.Name, "disabled", current.Version, "Actual isolated Workspace disable", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	denyAll("disabled_workspace")
	current, e = w.Get(ctx, admin, workspace, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Update(ctx, admin, workspace, current.Name, "active", current.Version, "Restore exact isolated Workspace", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	// Operator-only status corruption tests current identity guards; the normal
	// account administration lifecycle is covered by M01/M02 acceptance.
	if _, e = account.DB.Exec(ctx, "UPDATE users SET status='disabled' WHERE id=$1", a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	denyAll("disabled_identity")
}
