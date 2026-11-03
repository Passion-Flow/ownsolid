//go:build workflow_database_integration

package workflows

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

// SQL terminal/publication rows below are explicit retention constraint fixtures,
// never successful model execution or product publication evidence.
func TestM13ActualRetentionIsBoundedAuditAtomicAndExcludesActiveRuns(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	account.Config.Workflows.Enabled = true
	policy := account.Config.Policy()
	policy.Workflows.RetentionDays = 1
	policy.Workflows.MaintenanceBatch = 2
	account.Config.Runtime = &policy
	s := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	draft, e := s.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Bounded private retention", Reason: "Synthetic retention fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var tenant, authority, membership string
	if e = account.DB.QueryRow(ctx, `SELECT f.tenant_id::text,f.execution_authority::text,m.authority_id::text FROM workflows f JOIN workspace_memberships m ON m.tenant_id=f.tenant_id AND m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$1`, draft.ID, a.Me.User.ID).Scan(&tenant, &authority, &membership); e != nil {
		t.Fatal(e)
	}
	version, conversation := uuid.NewString(), uuid.NewString()
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES($1,$2,$3,$4,1,1,$5,$6,$7)`, version, tenant, workspace, draft.ID, draft.DraftGraph, draft.DraftSHA256, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id,turn_count) VALUES($1,$2,$3,$4,$5,$6,2)`, conversation, tenant, workspace, draft.ID, version, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	insert := func(old bool, conv bool, active bool) string {
		t.Helper()
		id := uuid.NewString()
		days := 0
		if old {
			days = 2
		}
		var c, v any
		var revision any = int64(1)
		var turn any
		if conv {
			c = conversation
			v = version
			revision = nil
			turn = 2
			if active {
				turn = 1
			}
		}
		state := "failed"
		var finished any = true
		if active {
			state = "pending"
			finished = nil
		}
		_, e = account.DB.Exec(ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,turn_number,version_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,error_code,deadline_at,finished_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'portal',$12,$13,$14::text,'Synthetic bounded private question',CASE WHEN $14::text='failed' THEN 'WORKFLOW_RUN_TIMEOUT' ELSE NULL END,clock_timestamp()+interval '4 minutes',CASE WHEN $15::boolean IS NULL THEN NULL ELSE clock_timestamp()-make_interval(days=>$16) END)`, id, tenant, workspace, draft.ID, a.Me.User.ID, c, turn, v, revision, authority, membership, draft.DraftGraph, draft.DraftSHA256, state, finished, days)
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	old1, old2, recent, deleted, active := insert(true, false, false), insert(true, false, false), insert(false, false, false), insert(false, true, false), insert(false, true, true)
	if e = s.DeleteConversation(ctx, a, workspace, draft.ID, conversation, RevisionReason{ExpectedRevision: 1, Reason: "Delete private test conversation"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetRun(ctx, a, workspace, draft.ID, deleted); e == nil || e.Error() != "WORKFLOW_RUN_NOT_FOUND" {
		t.Fatal("deleted conversation run remained readable", e)
	}
	if _, e = account.DB.Exec(ctx, `CREATE FUNCTION workflow_retention_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='workflow.retention_completed' THEN RAISE EXCEPTION 'SYNTHETIC_RETENTION_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$;CREATE TRIGGER workflow_retention_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION workflow_retention_test_audit()`); e != nil {
		t.Fatal(e)
	}
	exists := func(id string) bool {
		t.Helper()
		var present bool
		if e = account.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM workflow_runs WHERE id=$1)", id).Scan(&present); e != nil {
			t.Fatal(e)
		}
		return present
	}
	if _, e = s.Cleanup(ctx); e == nil {
		t.Fatal("failed retention audit committed")
	}
	for _, id := range []string{old1, old2, recent, deleted, active} {
		if !exists(id) {
			t.Fatal("audit failure removed retained row")
		}
	}
	if _, e = account.DB.Exec(ctx, "DROP TRIGGER workflow_retention_test_audit ON audit_events;DROP FUNCTION workflow_retention_test_audit()"); e != nil {
		t.Fatal(e)
	}
	result, e := s.Cleanup(ctx)
	if e != nil || result.Runs != 2 || result.Conversations != 0 {
		t.Fatal("first bounded batch", result, e)
	}
	if exists(old1) || exists(old2) || !exists(recent) || !exists(deleted) || !exists(active) {
		t.Fatal("retention batch scope or live exclusion")
	}
	result, e = s.Cleanup(ctx)
	if e != nil || result.Runs != 1 || result.Conversations != 0 || exists(deleted) || !exists(active) || !exists(recent) {
		t.Fatal("deleted body cleanup/live conversation", result, e)
	}
	// Simulated expired-row reduction only; actual process crash is a separate case.
	if _, e = account.DB.Exec(ctx, `UPDATE workflow_runs SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',finished_at=clock_timestamp() WHERE id=$1`, active); e != nil {
		t.Fatal(e)
	}
	result, e = s.Cleanup(ctx)
	if e != nil || result.Runs != 1 || result.Conversations != 1 || exists(active) || !exists(recent) {
		t.Fatal("terminal delete and conversation capacity recovery", result, e)
	}
	var creates int
	if e = account.DB.QueryRow(ctx, "SELECT count(*) FROM workflow_mutation_intents WHERE purpose='create_workflow'").Scan(&creates); e != nil || creates != 1 {
		t.Fatal("creation intent was purged", e)
	}
}
