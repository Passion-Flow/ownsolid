//go:build workflow_database_integration

package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"testing"
)

func TestM13ActualDraftPersistenceRevisionIdempotencyAuditAndSchemaConstraints(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	var account *identity.Service
	profiles := []string{}
	migrationVersion := 13
	downPath := "postgresql/000013_workflows.down.sql"
	if os.Getenv("PROJECT_ONE_TEST_APPLICATION_PROFILE") == "workflows" {
		// The historical fixture is retained only for its original unselected profile.
		account = fixture.NewM13(t)
		profiles = []string{"workflows"}
		migrationVersion = 19
		downPath = "account/000019_workflows.down.sql"
	} else {
		account = fixture.NewAtVersion(t, 13)
	}
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	account.Config.Workflows.Enabled = true
	service := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	key := uuid.NewString()
	input := CreateRequest{WorkspaceID: workspace, Name: "Caf\u00e9 workflow", Reason: "Actual isolated workflow draft acceptance"}
	created, e := service.Create(ctx, a, input, key, uuid.NewString())
	if e != nil || created.Revision != 1 || !created.CanEdit || len(created.DraftGraph) == 0 {
		t.Fatal("actual persisted draft", e)
	}
	replay, e := service.Create(ctx, a, input, key, uuid.NewString())
	if e != nil || replay.ID != created.ID {
		t.Fatal("idempotent draft replay", e)
	}
	other := input
	other.Name = "Different request"
	if _, e = service.Create(ctx, a, other, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed request reused intent", e)
	}
	if _, e = service.Create(ctx, a, input, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "WORKFLOW_NAME_EXISTS" {
		t.Fatal("normalized duplicate name", e)
	}
	replacement := *service
	restored, e := replacement.Get(ctx, a, workspace, created.ID)
	if e != nil || string(restored.DraftGraph) != string(created.DraftGraph) {
		t.Fatal("new service lost durable draft", e)
	}
	validation, e := service.Validate(ctx, a, workspace, created.ID, RevisionReason{ExpectedRevision: 1, Reason: "Actual missing graph inputs"})
	if e != nil || validation.Valid {
		t.Fatal("incomplete draft became executable", e)
	}
	if _, e = service.Publish(ctx, a, workspace, created.ID, RevisionReason{ExpectedRevision: 1, Reason: "Actual incomplete publication rejection"}, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "WORKFLOW_GRAPH_INVALID" {
		t.Fatal("missing inputs published", e)
	}
	var graph Graph
	if json.Unmarshal(created.DraftGraph, &graph) != nil {
		t.Fatal("durable graph JSON")
	}
	graph.Nodes[0].Position.X = 555
	update := UpdateRequest{Name: created.Name, Graph: graph, Enabled: false, ExpectedRevision: created.Revision, Reason: "Actual persisted edit and disable"}
	changed, e := service.Update(ctx, a, workspace, created.ID, update, uuid.NewString())
	if e != nil || changed.Revision != 2 || changed.Enabled {
		t.Fatal("actual revision-fenced update", e)
	}
	if _, e = service.Update(ctx, a, workspace, created.ID, update, uuid.NewString()); e == nil || e.Error() != "WORKFLOW_DRAFT_CHANGED" {
		t.Fatal("stale writer changed draft", e)
	}
	var tenant string
	if e = account.DB.QueryRow(ctx, "SELECT tenant_id::text FROM workspaces WHERE id=$1", workspace).Scan(&tenant); e != nil {
		t.Fatal(e)
	}
	// A direct synthetic version row verifies only SQL immutability/FK mechanics;
	// it is not product publication or authorized knowledge/model execution proof.
	version := uuid.NewString()
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES($1,$2,$3,$4,1,2,$5,$6,$7)`, version, tenant, workspace, created.ID, changed.DraftGraph, changed.DraftSHA256, a.Me.User.ID); e != nil {
		t.Fatal("SQL publication constraint fixture", e)
	}
	_, e = account.DB.Exec(ctx, "UPDATE workflow_versions SET version_number=2 WHERE id=$1", version)
	var databaseError *pgconn.PgError
	if !errors.As(e, &databaseError) || databaseError.Code != "P0001" || databaseError.Message != "WORKFLOW_VERSION_IMMUTABLE" {
		t.Fatal("published graph changed", e)
	}
	_, e = account.DB.Exec(ctx, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES($1,$2,$3,$4,2,2,$5,$6,$7)`, uuid.NewString(), uuid.NewString(), workspace, created.ID, changed.DraftGraph, changed.DraftSHA256, a.Me.User.ID)
	if !errors.As(e, &databaseError) || databaseError.Code != "23503" {
		t.Fatal("cross-tenant publication scope accepted", e)
	}
	if _, e = account.DB.Exec(ctx, `CREATE FUNCTION workflow_test_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'workflow.%' THEN RAISE EXCEPTION 'SYNTHETIC_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$; CREATE TRIGGER workflow_test_reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION workflow_test_reject_audit()`); e != nil {
		t.Fatal(e)
	}
	rejected := input
	rejected.Name = "Audit rollback must not persist"
	if _, e = service.Create(ctx, a, rejected, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("audit failure accepted")
	}
	var count int
	if e = account.DB.QueryRow(ctx, "SELECT count(*) FROM workflows").Scan(&count); e != nil || count != 1 {
		t.Fatal("failed audit committed workflow", e, count)
	}

	// Roll back only this freshly owned schema's M13 tables, preserve real identity
	// rows, and actually migrate that populated schema12 back to13.
	if _, e = account.DB.Exec(ctx, "DROP TRIGGER workflow_test_reject_audit ON audit_events; DROP FUNCTION workflow_test_reject_audit()"); e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile(downPath)
	if e != nil {
		t.Fatal(e)
	}
	if migrationVersion == 19 {
		tx, e := account.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(down)); e == nil {
			tx.Rollback(ctx)
			t.Fatal("populated workflow rollback allowed")
		}
		tx.Rollback(ctx)
		if e = database.Check(ctx, account.DB, profiles...); e != nil {
			t.Fatal("populated refusal changed schema", e)
		}
		current, e := service.Get(ctx, a, workspace, created.ID)
		if e != nil || current.ID != created.ID {
			t.Fatal("rollback refusal lost retained publication", e)
		}
		return // Empty forward rollback/reapply is executed in its own fresh fixture below.
	}
	rollback, e := account.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rollback.Exec(ctx, string(down)); e != nil {
		rollback.Rollback(ctx)
		t.Fatal("actual owned M13 rollback", e)
	}
	if _, e = rollback.Exec(ctx, "DELETE FROM schema_migrations WHERE version=$1", migrationVersion); e != nil {
		rollback.Rollback(ctx)
		t.Fatal(e)
	}
	if e = rollback.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, account.DB, profiles...); e == nil {
		t.Fatal("schema12 was accepted as current13")
	}
	if e = database.Migrate(ctx, account.DB, profiles...); e != nil {
		t.Fatal("actual populated schema12 upgrade", e)
	}
	if e = database.Check(ctx, account.DB, profiles...); e != nil {
		t.Fatal("restored migration identity", e)
	}
	a = fixture.Login(t, account, "admin@example.invalid", password, "portal")
	recovered, e := service.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Recovered after schema rollback", Reason: "Actual synthetic rollback and upgrade"}, uuid.NewString(), uuid.NewString())
	if e != nil || recovered.ID == created.ID {
		t.Fatal("actual new schema13 draft after preserved identity login", e)
	}
}

func TestM13ActualEmptyForwardRollbackAndReapplyPreservesM12(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.NewM13(t)
	fixture.Bootstrap(t, account)
	ctx := t.Context()
	var before string
	const original = `SELECT jsonb_build_object('users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM users u),'passwords',(SELECT jsonb_agg(to_jsonb(p) ORDER BY user_id) FROM password_credentials p),'installation',(SELECT to_jsonb(i) FROM installation_state i WHERE id=1),'ledger',(SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m WHERE version<=18))::text`
	if e := account.DB.QueryRow(ctx, original).Scan(&before); e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile("account/000019_workflows.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := account.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		tx.Rollback(ctx)
		t.Fatal("actual empty forward down", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=19"); e != nil {
		tx.Rollback(ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, account.DB, "knowledge"); e != nil {
		t.Fatal("M12 prefix after empty workflow rollback", e)
	}
	if e = database.Migrate(ctx, account.DB, "workflows"); e != nil {
		t.Fatal("forward reapply", e)
	}
	var after string
	if e = account.DB.QueryRow(ctx, original).Scan(&after); e != nil || before != after {
		t.Fatal("empty workflow rollback changed M12 identity/credentials/checksums", e)
	}
	if e = database.Check(ctx, account.DB, "workflows"); e != nil {
		t.Fatal(e)
	}
}
