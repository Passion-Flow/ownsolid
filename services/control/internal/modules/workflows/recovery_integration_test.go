//go:build workflow_database_integration

package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

// Explicit SQL timestamps/owner rows simulate a crashed process and another
// live owner. PostgreSQL transitions/rollback are real; no process-crash or
// provider/model invocation qualification is claimed by this test.
func TestM13ActualExpiredRunRecoveryIsBoundedAtomicAndPreservesLiveOwner(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	account.Config.Workflows.Enabled = true
	service := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	draft, e := service.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Actual bounded recovery fixture", Reason: "Synthetic owned recovery input"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var tenant, authority, membership string
	if e = account.DB.QueryRow(ctx, `SELECT f.tenant_id::text,f.execution_authority::text,m.authority_id::text FROM workflows f JOIN workspace_memberships m ON m.tenant_id=f.tenant_id AND m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$1`, draft.ID, a.Me.User.ID).Scan(&tenant, &authority, &membership); e != nil {
		t.Fatal(e)
	}
	var graph Graph
	if json.Unmarshal(draft.DraftGraph, &graph) != nil {
		t.Fatal("graph")
	}
	insert := func(expired bool) string {
		t.Helper()
		id := uuid.NewString()
		seconds := 180
		if expired {
			seconds = -2
		}
		if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,process_owner,lease_id,lease_expires_at,deadline_at,started_at) VALUES($1,$2,$3,$4,$5,1,$6,$7,'portal',$8,$9,'running','Synthetic private recovery question',$10,$11,clock_timestamp()+make_interval(secs=>$12),clock_timestamp()+make_interval(secs=>$12),clock_timestamp()-interval '3 seconds')`, id, tenant, workspace, draft.ID, a.Me.User.ID, authority, membership, draft.DraftGraph, draft.DraftSHA256, uuid.NewString(), uuid.NewString(), seconds); e != nil {
			t.Fatal(e)
		}
		for _, node := range graph.Nodes {
			if node.Kind == RetrievalNode {
				_, e = account.DB.Exec(ctx, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state,started_at) VALUES($1,$2,$3,$4,$5,$6,'running',clock_timestamp()-interval '3 seconds')`, tenant, workspace, draft.ID, id, node.ID, node.Kind)
			} else {
				_, e = account.DB.Exec(ctx, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES($1,$2,$3,$4,$5,$6,'not_started')`, tenant, workspace, draft.ID, id, node.ID, node.Kind)
			}
			if e != nil {
				t.Fatal(e)
			}
		}
		return id
	}
	expired, live := insert(true), insert(false)
	if _, e = account.DB.Exec(ctx, `CREATE FUNCTION workflow_recovery_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='workflow.run_interrupted' THEN RAISE EXCEPTION 'SYNTHETIC_RECOVERY_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$; CREATE TRIGGER workflow_recovery_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION workflow_recovery_test_audit()`); e != nil {
		t.Fatal(e)
	}
	if e = service.RecoverExpired(ctx); e == nil {
		t.Fatal("failed interruption audit committed")
	}
	state := func(id string) string {
		t.Helper()
		var value string
		if e = account.DB.QueryRow(ctx, "SELECT state FROM workflow_runs WHERE id=$1", id).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	if state(expired) != "running" || state(live) != "running" {
		t.Fatal("audit failure changed durable run")
	}
	if _, e = account.DB.Exec(ctx, "DROP TRIGGER workflow_recovery_test_audit ON audit_events; DROP FUNCTION workflow_recovery_test_audit()"); e != nil {
		t.Fatal(e)
	}
	if e = service.RecoverExpired(ctx); e != nil {
		t.Fatal(e)
	}
	if state(expired) != "interrupted" || state(live) != "running" {
		t.Fatal("expired recovery adopted/released a live owner")
	}
	var incomplete, interrupted, audits int
	if e = account.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='not_started'),count(*) FILTER(WHERE state='interrupted') FROM workflow_node_traces WHERE run_id=$1`, expired).Scan(&incomplete, &interrupted); e != nil || incomplete != 3 || interrupted != 1 {
		t.Fatal("recovery fabricated node execution", e)
	}
	if e = service.RecoverExpired(ctx); e != nil {
		t.Fatal(e)
	}
	if e = account.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='workflow.run_interrupted'").Scan(&audits); e != nil || audits != 1 {
		t.Fatal("recovery replay duplicated audit", e)
	}
	_, e = account.DB.Exec(ctx, "UPDATE workflow_runs SET question='Changed immutable request' WHERE id=$1", expired)
	var server *pgconn.PgError
	if !errors.As(e, &server) || server.Code != "P0001" || server.Message != "WORKFLOW_RUN_IDENTITY_IMMUTABLE" {
		t.Fatal("durable run identity changed", e)
	}
}

func TestM13ActualLogicalRestoreCatalogEquivalence(t *testing.T) {
	account := fixture.NewM13(t)
	fixture.Bootstrap(t, account)
	ctx := t.Context()
	owned := fixture.OwnedPostgres(t, account.DB)
	var before, after string
	if e := account.DB.QueryRow(ctx, database.WorkflowCatalogSQL).Scan(&before); e != nil {
		t.Fatal(e)
	}
	archive := owned.DumpSchema(t, ctx)
	if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
		if e := os.WriteFile(filepath.Join(output, "catalog-restore.dump"), archive, 0600); e != nil {
			t.Fatal(e)
		}
	}
	restored := owned.RestoreFresh(t, ctx, account.DB, archive)
	if e := restored.QueryRow(ctx, database.WorkflowCatalogSQL).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
		for name, raw := range map[string]string{"catalog-original.json": before, "catalog-restored.json": after} {
			if e := os.WriteFile(filepath.Join(output, name), []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	t.Logf("CATALOG_SOURCE_SHA256=%x CATALOG_RESTORED_SHA256=%x", sha256.Sum256([]byte(before)), sha256.Sum256([]byte(after)))

	// The complete inventories are retained. Official18.6 dump/reparse moves six
	// CHECK and two index enum-array casts; production accepts only the two exact
	// reviewed hashes, preserving every other structural field.
	if before == after {
		t.Fatal("fixture did not exercise the declared dump reparse form")
	}
	for _, sql := range []string{"DROP INDEX workflow_one_active_turn_idx", "ALTER TABLE workflow_versions DISABLE TRIGGER workflow_publication_immutable", "ALTER TABLE workflow_runs ALTER COLUMN question DROP NOT NULL"} {
		tx, e := restored.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, sql); e != nil {
			tx.Rollback(ctx)
			t.Fatal(e)
		}
		if database.CheckAccountProfileWithin(ctx, transaction.PostgreSQLTransaction(tx), "workflows") == nil {
			tx.Rollback(ctx)
			t.Fatal("malformed restored catalog accepted")
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
	}

	if e := database.Check(ctx, restored, "workflows"); e != nil {
		t.Fatal(e)
	}
}
