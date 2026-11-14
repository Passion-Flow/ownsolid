//go:build plugin_database_integration && knowledge_encoder_integration

package workflows_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
)

func TestM14ActualRestrictedRuntimeToolWorkflowAndFinalAuditRollback(t *testing.T) {
	original, admin, portal, _, private := toolFixture(t)
	ctx := context.Background()
	operator := original.Service.DB
	schema := operator.Config().ConnConfig.RuntimeParams["search_path"]
	role := "m14_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	password := security.Token()
	if _, e := operator.Exec(ctx, "CREATE ROLE "+quoted+" WITH LOGIN PASSWORD '"+password+"' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS"); e != nil {
		t.Fatal("isolated runtime role", e)
	}
	defer func() {
		operation, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := operator.Exec(operation, "DROP OWNED BY "+quoted+"; DROP ROLE "+quoted); e != nil {
			t.Error("exact owned runtime cleanup", e)
		}
	}()
	schemaName := pgx.Identifier{schema}.Sanitize()
	if _, e := operator.Exec(ctx, "GRANT USAGE ON SCHEMA "+schemaName+" TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schemaName+" TO "+quoted+"; REVOKE INSERT,UPDATE,DELETE ON "+schemaName+".schema_migrations FROM "+quoted); e != nil {
		t.Fatal(e)
	}
	configuration := operator.Config().Copy()
	configuration.ConnConfig.User, configuration.ConnConfig.Password = role, password
	pool, e := pgxpool.NewWithConfig(ctx, configuration)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var actual string
	var super, createDB, createRole, inherit, bypass, createSchema, createPublic bool
	e = pool.QueryRow(ctx, `SELECT current_user,r.rolsuper,r.rolcreatedb,r.rolcreaterole,r.rolinherit,r.rolbypassrls,has_schema_privilege(current_user,$1,'CREATE'),has_schema_privilege(current_user,'public','CREATE') FROM pg_roles r WHERE r.rolname=current_user`, schema).Scan(&actual, &super, &createDB, &createRole, &inherit, &bypass, &createSchema, &createPublic)
	if e != nil || actual != role || super || createDB || createRole || inherit || bypass || createSchema || createPublic {
		t.Fatal("actual runtime privilege boundary", e)
	}
	for _, statement := range []string{"CREATE TABLE plugin_forbidden(id integer)", "ALTER TABLE plugin_versions ADD COLUMN forbidden integer", "TRUNCATE plugin_grants", "UPDATE schema_migrations SET checksum='forbidden' WHERE version=14"} {
		_, e := pool.Exec(ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(e, &denied) || denied.Code != "42501" {
			t.Fatal("runtime DDL/migration was not denied")
		}
	}
	accounts := *original.Service
	accounts.DB = pool
	mail := *accounts.Mail
	mail.DB = pool
	accounts.Mail = &mail
	h := identityhttp.New(&accounts)
	h.Knowledge.Service.Processor = original.Knowledge.Service.Processor
	if e = h.Plugins.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		operation, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := h.Plugins.Service.Stop(operation); e != nil {
			t.Error(e)
		}
	}()
	s := h.Workflows.Service
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		operation, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := s.Stop(operation); e != nil {
			t.Error(e)
		}
	}()
	version, _ := installTool(t, h.Plugins.Service, admin, private, "1.0.0")
	workspace := portal.Me.Workspace.ID
	draft, e := s.Create(ctx, portal, workflows.CreateRequest{WorkspaceID: workspace, Name: "Actual DML-only signed tool", Reason: "Synthetic runtime acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Update(ctx, portal, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: toolGraph(version.ID), Enabled: true, ExpectedRevision: draft.Revision, Reason: "Actual DML-only graph"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	publication, e := s.Publish(ctx, portal, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual DML-only publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	conversation, e := s.CreateConversation(ctx, portal, workspace, draft.ID, workflows.ConversationRequest{VersionID: publication.ID, Reason: "Actual DML-only conversation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	request := workflows.RunRequest{Question: "List my current models", Reason: "Actual DML-only tool result"}
	run, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	expectToolSuccess(t, waitTool(t, s, portal, draft.ID, run.ID))
	verifyPortalRestrictedRuntime(t, h, portal, draft.ID, run.ID, "succeeded")
	// The operator installs only an explicit test audit failure. All application
	// admission, tool SQL and terminal failure continue through the restricted pool.
	if _, e = operator.Exec(ctx, `CREATE FUNCTION plugin_workflow_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='workflow.run_succeeded' THEN RAISE EXCEPTION 'SYNTHETIC_PLUGIN_FINAL_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$;CREATE TRIGGER plugin_workflow_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION plugin_workflow_test_audit()`); e != nil {
		t.Fatal(e)
	}
	var successesBefore, successesAfter int
	if e = operator.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='workflow.run_succeeded'").Scan(&successesBefore); e != nil {
		t.Fatal(e)
	}
	failed, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	failed = waitTool(t, s, portal, draft.ID, failed.ID)
	if failed.State != "failed" || failed.ErrorCode == nil || *failed.ErrorCode != "STORAGE_UNAVAILABLE" || failed.ResultAvailable || failed.ToolResult != nil || failed.Answer != nil || failed.Usage != nil {
		t.Fatal("failed final audit committed tool result", failed.State, failed.ErrorCode)
	}
	verifyPortalRestrictedRuntime(t, h, portal, draft.ID, failed.ID, "failed")
	var resultNull, proofNull bool
	if e = operator.QueryRow(ctx, "SELECT tool_result IS NULL,tool_proof IS NULL FROM workflow_runs WHERE id=$1", failed.ID).Scan(&resultNull, &proofNull); e != nil || !resultNull || !proofNull {
		t.Fatal("failed tool result/proof persisted", e)
	}
	if e = operator.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='workflow.run_succeeded'").Scan(&successesAfter); e != nil || successesBefore != successesAfter {
		t.Fatal("phantom success audit", e)
	}
	if _, e = operator.Exec(ctx, "DROP TRIGGER plugin_workflow_test_audit ON audit_events;DROP FUNCTION plugin_workflow_test_audit()"); e != nil {
		t.Fatal(e)
	}
}
