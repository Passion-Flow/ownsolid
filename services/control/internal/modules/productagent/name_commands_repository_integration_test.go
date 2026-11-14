//go:build repository_integration

package productagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func (g *executionRunFixture) publishNamedApplication(t *testing.T, name string) string {
	t.Helper()
	flow := g.gates[0].commands.applications.Workflows
	resource, err := flow.Create(g.ctx, g.actors[1], workflows.CreateRequest{WorkspaceID: g.f.Workspace, Name: name, Reason: "Owned name-resolution fixture"}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	version := uuid.NewString()
	g.exec(`INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) SELECT $1,tenant_id,workspace_id,id,1,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=$2`, version, resource.ID)
	g.exec(`UPDATE workflows SET current_version_id=$1 WHERE id=$2`, version, resource.ID)
	return resource.ID
}

func TestApplicationNameResolutionWithDurableRun(t *testing.T) {
	g := newExecutionRunFixture(t)
	lookup := func(prefix, name string) (Result, string, error) {
		encoded, _ := json.Marshal(name)
		id := uuid.NewString()
		result, err := g.runs[0].RunCommandForRequest(g.ctx, g.actors[1], prefix+" "+string(encoded), "", id)
		return result, id, err
	}
	const label = "Caf\u00e9 \u5e73\u53f0  100%_\"x\""
	app := g.publishNamedApplication(t, label)
	for _, prefix := range []string{"find application", "\u67e5\u627e\u5e94\u7528"} {
		result, id, err := lookup(prefix, " Cafe\u0301 \u5e73\u53f0  100%_\"x\" ")
		if err != nil || result.ApplicationResolution == nil || result.ApplicationResolution.State != "resolved" || result.ApplicationResolution.ApplicationID != app || len(result.ApplicationResolution.Candidates) != 1 {
			t.Fatalf("canonical lookup: %+v, %v", result, err)
		}
		g.audit(t, id, 1)
		g.step(t, id, "completed")
		var leaked int
		if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM audit_events WHERE request_id=$1 AND position($2 IN metadata::text)>0`, id, "100%_").Scan(&leaked); err != nil || leaked != 0 {
			t.Fatalf("lookup arguments reached audit metadata: %d, %v", leaked, err)
		}
	}
	for _, name := range []string{strings.ToLower(label), "Caf\u00e9", "absent"} {
		result, _, err := lookup("find application", name)
		if err != nil || result.ApplicationResolution == nil || result.ApplicationResolution.State != "not_found" || result.ApplicationResolution.ApplicationID != "" || len(result.ApplicationResolution.Candidates) != 0 {
			t.Fatalf("exact no-match: %+v, %v", result, err)
		}
	}
	if _, err := g.gates[0].commands.applications.Workflows.Create(g.ctx, g.actors[1], workflows.CreateRequest{
		WorkspaceID: g.f.Workspace, Name: "Unpublished lookup", Reason: "Unpublished lookup fixture",
	}, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if result, _, err := lookup("find application", "Unpublished lookup"); err != nil || result.ApplicationResolution == nil || result.ApplicationResolution.State != "not_found" {
		t.Fatalf("unpublished lookup: %+v, %v", result, err)
	}
	maximum := strings.Repeat("\u5b57", 120)
	maximumID := g.publishNamedApplication(t, maximum)
	if result, _, err := lookup("find application", maximum); err != nil || result.ApplicationResolution.ApplicationID != maximumID {
		t.Fatalf("maximum name: %+v, %v", result, err)
	}
	commandID := g.publishNamedApplication(t, "open home; disable platform")
	if result, _, err := lookup("find application", "open home; disable platform"); err != nil || result.Path != "" || result.ApplicationResolution.ApplicationID != commandID {
		t.Fatalf("command-like data: %+v, %v", result, err)
	}
	duplicate := g.publishNamedApplication(t, "Second visible name")
	g.exec(`UPDATE workflows SET name=$1 WHERE id=$2`, label, duplicate)
	if result, _, err := lookup("find application", label); err != nil || result.ApplicationResolution.State != "ambiguous" || result.ApplicationResolution.ApplicationID != "" || len(result.ApplicationResolution.Candidates) != 2 {
		t.Fatalf("ambiguous selection: %+v, %v", result, err)
	}
	g.exec(`UPDATE workflows SET enabled=false WHERE id=$1`, duplicate)
	g.exec(`UPDATE workflows SET enabled=false WHERE id=$1`, app)
	if result, _, err := lookup("find application", label); err != nil || result.ApplicationResolution.State != "not_found" {
		t.Fatalf("withdrawn publication: %+v, %v", result, err)
	}
	g.exec(`UPDATE workflows SET enabled=true WHERE id=$1`, app)
	// A committed name binding cannot be changed on another process/pool.
	runID := g.start(t)
	step := uuid.NewString()
	if _, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, step, `find application "absent"`, ""); err != nil {
		t.Fatal(err)
	}
	if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, step, `find application "different"`, ""); result != (Result{}) || err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed lookup replay: %+v, %v", result, err)
	}
	g.audit(t, step, 1)
	// A real database refusal after projection must suppress every candidate.
	g.exec(`CREATE FUNCTION reject_name_read() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='product_assistant.find_application' THEN RAISE EXCEPTION 'private name fixture'; END IF; RETURN NEW; END $$`)
	g.exec(`CREATE TRIGGER reject_name_read BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_name_read()`)
	result, id, err := lookup("find application", maximum)
	if err == nil || result != (Result{}) || strings.Contains(err.Error(), "private name fixture") {
		t.Fatalf("audit refusal: %+v, %v", result, err)
	}
	g.audit(t, id, 0)
	g.exec(`DROP TRIGGER reject_name_read ON audit_events`)
	// Scope is rechecked, even when a caller supplies an internal Auth value.
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Lookup foreign tenant','active')`, foreignTenant)
	g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Lookup foreign workspace','active')`, foreignWorkspace, foreignTenant)
	actor := g.actors[1]
	actor.Me.Workspace = &identity.Workspace{ID: foreignWorkspace, TenantID: foreignTenant}
	if result, err := g.runs[0].RunCommandForRequest(g.ctx, actor, `find application "absent"`, "", uuid.NewString()); result != (Result{}) || err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatalf("foreign lookup: %+v, %v", result, err)
	}
	policy := *g.policy.Load()
	policy.User.Executions = 0
	g.policy.Store(&policy)
	if result, _, err := lookup("find application", maximum); result != (Result{}) || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("exhausted lookup: %+v, %v", result, err)
	}
	policy.User.Executions = 100
	g.policy.Store(&policy)
	g.exec(`DELETE FROM workspace_memberships WHERE user_id=$1 AND tenant_id=$2 AND workspace_id=$3`, g.f.User, g.f.Tenant, g.f.Workspace)
	if result, _, err := lookup("find application", maximum); result != (Result{}) || err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatalf("revoked membership lookup: %+v, %v", result, err)
	}
	g.exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, g.f.User, g.f.Tenant, g.f.Workspace, uuid.NewString())
	g.exec(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, g.actors[1].SessionID)
	if result, _, err := lookup("find application", maximum); result != (Result{}) || err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatalf("revoked lookup: %+v, %v", result, err)
	}
}

func TestApplicationNameResolutionFinalFences(t *testing.T) {
	t.Run("policy revoked after owner read", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		g.publishNamedApplication(t, "Protected application")
		var calls atomic.Int64
		source := g.gates[0].policy
		g.gates[0].policy = func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) (ExecutionBudgetPolicy, error) {
			p, err := source(ctx, tx, subject)
			if calls.Add(1) == 4 {
				p.UserAccess = BudgetAccessDisabled
			}
			return p, err
		}
		runID := g.start(t)
		id := uuid.NewString()
		result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, `find application "Protected application"`, "")
		if result != (Result{}) || err != ErrCommandDenied {
			t.Fatalf("lookup final policy: %+v, %v", result, err)
		}
		g.audit(t, id, 1)
		g.step(t, id, "suppressed")
	})
	t.Run("cross-instance cancel during owner read", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		g.publishNamedApplication(t, "Protected application")
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int64
		g.gates[0].commands.executionPolicy = func(ctx context.Context) (bool, error) {
			if calls.Add(1) == 4 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return false, ctx.Err()
				}
			}
			return true, nil
		}
		runID := g.start(t)
		id := uuid.NewString()
		finished := make(chan error, 1)
		go func() {
			result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, `find application "Protected application"`, "")
			if result != (Result{}) {
				err = errors.New("LOOKUP_RESULT_ESCAPED_CANCEL")
			}
			finished <- err
		}()
		select {
		case <-entered:
		case <-g.ctx.Done():
			t.Fatal("lookup never reached its owner read")
		}
		state, err := g.runs[1].Cancel(g.ctx, g.actors[1], runID)
		close(release)
		if outcome := <-finished; err != nil || state.State != ExecutionRunStopped || outcome != errRunConflict {
			t.Fatalf("cancelled lookup: %+v, %v, command %v", state, err, outcome)
		}
		g.audit(t, id, 1)
		g.step(t, id, "suppressed")
	})
}
