//go:build workflow_database_integration

package workflows

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

// The live run is an explicit SQL ownership/state fixture. Real PostgreSQL
// transitions and audit rollback are exercised without model-success fixtures.
func TestM13ActualNodeTransitionsAuditRollbackAndIndependentFailureCommit(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	account.Config.Workflows.Enabled = true
	s := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	draft, e := s.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Actual trace transition fixture", Reason: "Synthetic explicit SQL state fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	graph, e := ParseGraph(draft.DraftGraph, s.policy(), account.Config.Policy().Knowledge.MaximumTopK)
	if e != nil {
		t.Fatal(e)
	}
	var tenant, authority, membership string
	if e = account.DB.QueryRow(ctx, `SELECT f.tenant_id::text,f.execution_authority::text,m.authority_id::text FROM workflows f JOIN workspace_memberships m ON m.tenant_id=f.tenant_id AND m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$1`, draft.ID, a.Me.User.ID).Scan(&tenant, &authority, &membership); e != nil {
		t.Fatal(e)
	}
	revision := draft.Revision
	execution := &runExecution{actor: a, tenant: tenant, workspace: workspace, workflow: draft.ID, id: uuid.NewString(), process: uuid.NewString(), lease: uuid.NewString(), membership: membership, authority: authority, request: uuid.NewString(), draftRevision: &revision, graph: graph}
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,process_owner,lease_id,lease_expires_at,deadline_at,started_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'portal',$9,$10,'running','Synthetic private transition question',$11,$12,clock_timestamp()+interval '60 seconds',clock_timestamp()+interval '120 seconds',clock_timestamp())`, execution.id, tenant, workspace, draft.ID, a.Me.User.ID, revision, authority, membership, draft.DraftGraph, draft.DraftSHA256, execution.process, execution.lease); e != nil {
		t.Fatal(e)
	}
	for _, node := range graph.Graph.Nodes {
		if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES($1,$2,$3,$4,$5,$6,'not_started')`, tenant, workspace, draft.ID, execution.id, node.ID, node.Kind); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.nodeTransition(ctx, execution, StartNode, NotStartedState, RunningState, TraceResult{QuestionBytes: len("Synthetic private transition question")}); e != nil {
		t.Fatal("actual trace start SQL", e)
	}
	if _, e = account.DB.Exec(ctx, `CREATE FUNCTION workflow_node_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='workflow.node_succeeded' THEN RAISE EXCEPTION 'SYNTHETIC_NODE_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$;CREATE TRIGGER workflow_node_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION workflow_node_test_audit()`); e != nil {
		t.Fatal(e)
	}
	if e = s.nodeTransition(ctx, execution, StartNode, RunningState, SucceededState, TraceResult{}); e == nil {
		t.Fatal("node audit failure committed")
	}
	var state string
	if e = account.DB.QueryRow(ctx, "SELECT state FROM workflow_node_traces WHERE run_id=$1 AND kind='start'", execution.id).Scan(&state); e != nil || state != RunningState {
		t.Fatal("node audit rollback lost running state", state, e)
	}
	if _, e = account.DB.Exec(ctx, "DROP TRIGGER workflow_node_test_audit ON audit_events;DROP FUNCTION workflow_node_test_audit()"); e != nil {
		t.Fatal(e)
	}
	if e = s.finishFailure(ctx, execution, FailedState, "STORAGE_UNAVAILABLE"); e != nil {
		t.Fatal("independent failure", e)
	}
	var failed, unexecuted int
	if e = account.DB.QueryRow(ctx, "SELECT count(*) FILTER(WHERE state='failed'),count(*) FILTER(WHERE state='not_started') FROM workflow_node_traces WHERE run_id=$1", execution.id).Scan(&failed, &unexecuted); e != nil || failed != 1 || unexecuted != 3 {
		t.Fatal("failure fabricated downstream execution", failed, unexecuted, e)
	}
	if e = account.DB.QueryRow(ctx, "SELECT state FROM workflow_runs WHERE id=$1", execution.id).Scan(&state); e != nil || state != FailedState {
		t.Fatal("independent terminal result", state, e)
	}
}
