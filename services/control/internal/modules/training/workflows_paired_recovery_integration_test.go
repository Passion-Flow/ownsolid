//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type cumulativeWorkflowRecovery struct {
	draft          workflows.Workflow
	run, authority string
}

// The draft is created by the real service. Explicit SQL represents a process
// lost while retrieving, without claiming that a model ran or produced output.
// Actual complete published CPU runs are covered by the separate causal gate.
func prepareCumulativeWorkflowRecovery(t *testing.T, g preparationScenario) *cumulativeWorkflowRecovery {
	t.Helper()
	a := g.f.s.Identity
	if !a.Config.WorkflowsRuntime() {
		return nil
	}
	// The explicit predecessor fixture starts with its delivered workflow feature enabled.
	a.Config.Workflows.Enabled = true
	s := &workflows.Service{DB: a.DB, Identity: a, Workspaces: g.f.s.Workspaces}
	d, e := s.Create(t.Context(), g.f.admin, workflows.CreateRequest{WorkspaceID: g.f.admin.Me.Workspace.ID, Name: "Matched workflow recovery", Reason: "Retain a real editable draft beside a declared crashed retrieval fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var authority, membership string
	if e = a.DB.QueryRow(t.Context(), `SELECT f.execution_authority::text,m.authority_id::text FROM workflows f JOIN workspace_memberships m ON m.tenant_id=f.tenant_id AND m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$1`, d.ID, g.f.admin.Me.User.ID).Scan(&authority, &membership); e != nil {
		t.Fatal(e)
	}
	run := uuid.NewString()
	if _, e = a.DB.Exec(t.Context(), `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,process_owner,lease_id,lease_expires_at,deadline_at,started_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'admin',$9,$10,'running','Declared matched recovery fixture; no model answer',$11,$12,clock_timestamp()+interval '5 minutes',clock_timestamp()+interval '5 minutes',clock_timestamp())`, run, g.f.admin.Me.Workspace.TenantID, d.WorkspaceID, d.ID, g.f.admin.Me.User.ID, d.Revision, authority, membership, d.DraftGraph, d.DraftSHA256, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var graph workflows.Graph
	if json.Unmarshal(d.DraftGraph, &graph) != nil {
		t.Fatal("matched draft graph")
	}
	for _, node := range graph.Nodes {
		state := "not_started"
		if node.Kind == workflows.RetrievalNode {
			state = "running"
		}
		if _, e = a.DB.Exec(t.Context(), `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state,started_at) VALUES($1,$2,$3,$4,$5,$6,$7::text,CASE WHEN $7::text='running' THEN clock_timestamp() END)`, g.f.admin.Me.Workspace.TenantID, d.WorkspaceID, d.ID, run, node.ID, node.Kind, state); e != nil {
			t.Fatal(e)
		}
	}
	return &cumulativeWorkflowRecovery{draft: d, run: run, authority: authority}
}

func cumulativeWorkflowRows(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, e := pool.Query(t.Context(), "SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename")
	if e != nil {
		t.Fatal(e)
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		tables = append(tables, name)
	}
	rows.Close()
	var entries int
	if e := pool.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&entries); e != nil {
		t.Fatal(e)
	}
	expected := 0
	switch entries {
	case 19:
		expected = 80
	case 20:
		expected = 84
	case 21, 22:
		expected = 86
	default:
		t.Fatal("exact cumulative ledger required", entries)
	}
	if rows.Err() != nil || len(tables) != expected {
		t.Fatal("exact matched cumulative catalog", rows.Err(), len(tables))
	}
	out := map[string]string{}
	for _, name := range tables {
		var raw string
		if e = pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]') FROM "+pgx.Identifier{name}.Sanitize()+" t").Scan(&raw); e != nil {
			t.Fatal(e)
		}
		out[name] = raw
	}
	return out
}

func assertCumulativeWorkflowRows(t *testing.T, pool *pgxpool.Pool, before map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(before, cumulativeWorkflowRows(t, pool)) {
		t.Fatal("late matched workflow failure partially invalidated an earlier owner")
	}
}

func verifyCumulativeWorkflowRecovery(t *testing.T, state *cumulativeWorkflowRecovery, g preparationScenario, pool *pgxpool.Pool) {
	t.Helper()
	if state == nil {
		return
	}
	a := *g.f.s.Identity
	a.DB = pool
	a.Storage = nil
	workspace := *g.f.s.Workspaces
	workspace.DB = pool
	workspace.Identity = &a
	s := &workflows.Service{DB: pool, Identity: &a, Workspaces: &workspace}
	// Get rechecks identity before entering the Workspace owner, so the old
	// session cannot use the original Workspace service to regain authority.
	if _, e := s.Get(t.Context(), g.f.admin, state.draft.WorkspaceID, state.draft.ID); e == nil {
		t.Fatal("old workflow session survived matched restore")
	}
	fresh := fixture.Login(t, &a, "admin@example.invalid", g.f.password, "admin")
	d, e := s.Get(t.Context(), fresh, state.draft.WorkspaceID, state.draft.ID)
	if e != nil || d.DraftSHA256 != state.draft.DraftSHA256 || string(d.DraftGraph) != string(state.draft.DraftGraph) || d.Revision != state.draft.Revision {
		t.Fatal("matched workflow draft changed", e)
	}
	var interrupted, fenced, rotated bool
	if e = pool.QueryRow(t.Context(), `SELECT r.state='interrupted' AND r.error_code='WORKFLOW_RUN_INTERRUPTED' AND r.question='Declared matched recovery fixture; no model answer',r.answer IS NULL AND r.evidence IS NULL AND r.alias_snapshot IS NULL AND r.usage IS NULL AND r.process_owner IS NULL AND r.lease_id IS NULL AND r.lease_expires_at IS NULL,f.execution_authority<>$2::uuid FROM workflow_runs r JOIN workflows f ON f.id=r.workflow_id WHERE r.id=$1`, state.run, state.authority).Scan(&interrupted, &fenced, &rotated); e != nil || !interrupted || !fenced || !rotated {
		t.Fatal("matched workflow old authority/output survived", e)
	}
	var untouched, stopped int
	if e = pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE state='not_started'),count(*) FILTER(WHERE state='interrupted' AND error_code='WORKFLOW_RUN_INTERRUPTED') FROM workflow_node_traces WHERE run_id=$1`, state.run).Scan(&untouched, &stopped); e != nil || untouched != 3 || stopped != 1 {
		t.Fatal("matched workflow fabricated execution", e)
	}
	t.Log("Actual M13 matched four-store+SQL+SSO-keyring restore retained draft/source/vector bytes, rolled every selected catalog owner back on a real late workflow fault, interrupted declared lost retrieval/lease without output or automatic model replay, rotated authority and required fresh login")
}
