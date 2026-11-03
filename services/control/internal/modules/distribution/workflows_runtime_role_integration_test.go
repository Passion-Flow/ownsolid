//go:build knowledge_gateway_integration

package distribution_test

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
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
)

func verifyWorkflowRuntimeRole(t *testing.T, ctx context.Context, c scenario, original *identityhttp.Handler, graph workflows.Graph, question string, document knowledge.Document) {
	t.Helper()
	operator := c.s.Identity.DB
	schema := operator.Config().ConnConfig.RuntimeParams["search_path"]
	role := "m13_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	password := security.Token()
	// Only an exact newly generated test role is changed; its secret is never logged.
	if _, e := operator.Exec(ctx, "CREATE ROLE "+quoted+" WITH LOGIN PASSWORD '"+password+"' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS"); e != nil {
		t.Fatal("isolated workflow runtime login role", e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := operator.Exec(cleanup, "DROP OWNED BY "+quoted+"; DROP ROLE "+quoted); e != nil {
			t.Error("owned workflow runtime role cleanup", e)
		}
	}()
	schemaName := pgx.Identifier{schema}.Sanitize()
	if _, e := operator.Exec(ctx, "GRANT USAGE ON SCHEMA "+schemaName+" TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schemaName+" TO "+quoted+"; REVOKE INSERT,UPDATE,DELETE ON "+schemaName+".schema_migrations FROM "+quoted); e != nil {
		t.Fatal("workflow runtime DML grants", e)
	}
	configuration := operator.Config().Copy()
	configuration.ConnConfig.User, configuration.ConnConfig.Password = role, password
	pool, e := pgxpool.NewWithConfig(ctx, configuration)
	if e != nil {
		t.Fatal("actual workflow runtime login", e)
	}
	defer pool.Close()
	var actual string
	var super, createDB, createRole, inherit, bypass, createSchema, createPublic bool
	e = pool.QueryRow(ctx, `SELECT current_user,r.rolsuper,r.rolcreatedb,r.rolcreaterole,r.rolinherit,r.rolbypassrls,has_schema_privilege(current_user,$1,'CREATE'),has_schema_privilege(current_user,'public','CREATE') FROM pg_roles r WHERE r.rolname=current_user`, schema).Scan(&actual, &super, &createDB, &createRole, &inherit, &bypass, &createSchema, &createPublic)
	if e != nil || actual != role || super || createDB || createRole || inherit || bypass || createSchema || createPublic {
		t.Fatal("workflow runtime privilege boundary", e)
	}
	for _, statement := range []string{"CREATE TABLE workflow_runtime_forbidden(id integer)", "ALTER TABLE workflow_runs ADD COLUMN runtime_forbidden integer", "TRUNCATE workflow_runs", "UPDATE schema_migrations SET checksum='forbidden' WHERE version=13"} {
		_, e := pool.Exec(ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(e, &denied) || denied.Code != "42501" {
			t.Fatal("workflow runtime DDL/migration operation was not denied")
		}
	}
	accounts := *c.s.Identity
	accounts.DB = pool
	mail := *accounts.Mail
	mail.DB = pool
	accounts.Mail = &mail
	// Compose every service from the restricted pool; no copied dependency may
	// retain an operator connection underneath the workflow or model authority path.
	handler := identityhttp.New(&accounts)
	handler.Knowledge.Service.Processor = original.Knowledge.Service.Processor
	s := handler.Workflows.Service
	if e = s.Start(ctx); e != nil {
		t.Fatal("DML workflow readiness", e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if e := s.Stop(cleanup); e != nil {
			t.Error("DML workflow shutdown", e)
		}
	}()
	w := c.p.Me.Workspace.ID
	draft, e := s.Create(ctx, c.p, workflows.CreateRequest{WorkspaceID: w, Name: "Restricted runtime actual workflow", Reason: "Synthetic DML-only actual model path"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("DML workflow create", e)
	}
	draft, e = s.Update(ctx, c.p, w, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "DML graph persistence"}, uuid.NewString())
	if e != nil {
		t.Fatal("DML workflow save", e)
	}
	version, e := s.Publish(ctx, c.p, w, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "DML immutable publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("DML workflow publication", e)
	}
	conversation, e := s.CreateConversation(ctx, c.p, w, draft.ID, workflows.ConversationRequest{VersionID: version.ID, Reason: "DML private conversation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("DML workflow conversation", e)
	}
	run, e := s.RunConversation(ctx, c.p, w, draft.ID, conversation.ID, workflows.RunRequest{Question: question, Reason: "DML actual encoder Rust CPU model"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("DML workflow durable admission", e)
	}
	operation, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for run.State == "pending" || run.State == "running" {
		select {
		case <-operation.Done():
			t.Fatal("DML workflow run deadline")
		case <-ticker.C:
		}
		run, e = s.GetRun(operation, c.p, w, draft.ID, run.ID)
		if e != nil {
			t.Fatal("DML workflow history", e)
		}
	}
	if run.State != "succeeded" || !run.ResultAvailable || run.Answer == nil || !strings.Contains(*run.Answer, "Lisbon") || !strings.Contains(*run.Answer, "[S1]") || len(run.Sources) != 1 || run.Sources[0].SourceID != document.VisibleSource.ID || len(run.Nodes) != 4 || run.Usage == nil || run.Usage.CompletionTokens < 1 {
		t.Fatal("DML actual model path", run.State, workflowTestCode(run.ErrorCode))
	}
	for _, trace := range run.Nodes {
		if trace.State != "succeeded" {
			t.Fatal("DML actual trace", trace.Kind, trace.State)
		}
	}
	var audits int
	if e = pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='workflow.run_succeeded' AND target_id=$1", run.ID).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("DML workflow atomic success audit", e, audits)
	}
}
