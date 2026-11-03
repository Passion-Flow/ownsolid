//go:build knowledge_database_integration && knowledge_encoder_integration

package knowledge

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
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func TestM12ActualRuntimeLoginRoleHasDMLOnlyAndRunsRealKnowledgePath(t *testing.T) {
	original, a, p := serviceFixture(t)
	ctx := context.Background()
	schema := original.DB.Config().ConnConfig.RuntimeParams["search_path"]
	role := "m12_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	password := security.Token()
	// Identifier and literal are generated test-only values; no secret reaches argv,
	// a source file, log, response, or an existing role. This role owns no objects.
	if _, e := original.DB.Exec(ctx, "CREATE ROLE "+quoted+" WITH LOGIN PASSWORD '"+password+"' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS"); e != nil {
		t.Fatal("isolated runtime login role", e)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := original.DB.Exec(cleanup, "DROP OWNED BY "+quoted+"; DROP ROLE "+quoted); e != nil {
			t.Error("owned runtime role cleanup", e)
		}
	})
	schemaName := pgx.Identifier{schema}.Sanitize()
	if _, e := original.DB.Exec(ctx, "GRANT USAGE ON SCHEMA "+schemaName+" TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schemaName+" TO "+quoted+"; REVOKE INSERT,UPDATE,DELETE ON "+schemaName+".schema_migrations FROM "+quoted); e != nil {
		t.Fatal("runtime DML grants", e)
	}
	configuration := original.DB.Config().Copy()
	configuration.ConnConfig.User = role
	configuration.ConnConfig.Password = password
	runtimePool, e := pgxpool.NewWithConfig(ctx, configuration)
	if e != nil {
		t.Fatal("actual runtime login", e)
	}
	t.Cleanup(runtimePool.Close)
	var actual string
	var super, createDB, createRole, bypass, createSchema, createPublic bool
	e = runtimePool.QueryRow(ctx, `SELECT current_user,r.rolsuper,r.rolcreatedb,r.rolcreaterole,r.rolbypassrls,has_schema_privilege(current_user,$1,'CREATE'),has_schema_privilege(current_user,'public','CREATE') FROM pg_roles r WHERE r.rolname=current_user`, schema).Scan(&actual, &super, &createDB, &createRole, &bypass, &createSchema, &createPublic)
	if e != nil || actual != role || super || createDB || createRole || bypass || createSchema || createPublic {
		t.Fatal("runtime privilege boundary", e)
	}
	for _, statement := range []string{"CREATE TABLE runtime_forbidden(id integer)", "ALTER TABLE knowledge_documents ADD COLUMN runtime_forbidden integer", "TRUNCATE knowledge_chunks", "UPDATE schema_migrations SET checksum='forbidden' WHERE version=12"} {
		_, e := runtimePool.Exec(ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(e, &denied) || denied.Code != "42501" {
			t.Fatal("runtime DDL/migration operation was not denied")
		}
	}
	accounts := *original.Identity
	accounts.DB = runtimePool
	workspace := &workspaces.Service{DB: runtimePool, Identity: &accounts}
	service := &Service{DB: runtimePool, Identity: &accounts, Workspaces: workspace, Processor: original.Processor}
	if e = service.VerifyStored(ctx); e != nil {
		t.Fatal("DML runtime vector readiness", e)
	}
	base := createFacts(t, service, p, a.Me.Workspace.ID)
	document := uploadFacts(t, service, p, a.Me.Workspace.ID, base.ID)
	processOne(t, service)
	ready, e := service.GetDocument(ctx, p, a.Me.Workspace.ID, base.ID, document.ID)
	if e != nil || ready.VisibleSource == nil {
		t.Fatal("DML-only actual ingestion", e)
	}
	found, e := service.Search(ctx, p, a.Me.Workspace.ID, base.ID, SearchRequest{Query: "Which city does the project launch in?", TopK: 1, MinimumScore: 0.1}, uuid.NewString())
	if e != nil || len(found.Matches) != 1 || !strings.Contains(found.Matches[0].Text, "Lisbon") {
		t.Fatal("DML-only actual vector search", e)
	}
	if _, e = service.Delete(ctx, p, a.Me.Workspace.ID, base.ID, ready.ID, RevisionReason{ExpectedRevision: ready.Revision, Reason: "Actual runtime role deletion"}, uuid.NewString()); e != nil {
		t.Fatal("DML-only deletion/audit", e)
	}
}

func TestM12ActualPhysicalRetentionPurgeAuditRollbackAndActiveIndexPreservation(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx := context.Background()
	w := a.Me.Workspace.ID
	base := createFacts(t, s, p, w)
	deleted := uploadFacts(t, s, p, w, base.ID)
	processOne(t, s)
	text := "The active project launch city is Lisbon."
	active, e := s.Upload(ctx, p, w, base.ID, UploadDocument{Name: "active.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Actual protected active source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	deleted, e = s.GetDocument(ctx, p, w, base.ID, deleted.ID)
	if e != nil {
		t.Fatal("read processed source before retention", e)
	}
	if deleted.State != "ready" || deleted.CurrentAttempt == nil || deleted.CurrentAttempt.ChunkCount != 1 {
		t.Fatalf("single actual source chunk required before retention: state=%s attempt=%+v", deleted.State, deleted.CurrentAttempt)
	}
	if _, e = s.Delete(ctx, p, w, base.ID, deleted.ID, RevisionReason{ExpectedRevision: deleted.Revision, Reason: "Actual immediate visibility removal before retention"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	policy := s.Identity.Config.Policy()
	policy.Knowledge.RetentionDays = 1
	policy.Knowledge.CleanupBatch = 1
	s.Identity.Config.Runtime = &policy
	before, e := s.Cleanup(ctx)
	if e != nil || before.Chunks+before.Attempts+before.Sources+before.Documents != 0 {
		t.Fatal("recent tombstone purged before retention window", e)
	}
	// Explicit operator-only historical-clock fixture in this new isolated schema.
	// Source bytes/vectors and the actual delete are real; this does not claim a
	// two-day soak. The immutable trigger is re-enabled in the same transaction.
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `ALTER TABLE knowledge_source_versions DISABLE TRIGGER knowledge_source_immutable`); e != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE knowledge_source_versions SET created_at=clock_timestamp()-interval '3 days',superseded_at=clock_timestamp()-interval '2 days' WHERE document_id=$1`, deleted.ID); e != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `ALTER TABLE knowledge_source_versions ENABLE TRIGGER knowledge_source_immutable`); e != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE knowledge_documents SET deleted_at=clock_timestamp()-interval '2 days' WHERE id=$1`, deleted.ID); e != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	var trigger string
	if e = s.DB.QueryRow(ctx, "SELECT tgenabled::text FROM pg_trigger WHERE tgrelid='knowledge_source_versions'::regclass AND tgname='knowledge_source_immutable'").Scan(&trigger); e != nil || trigger != "O" {
		t.Fatal("historical fixture left immutability disabled", e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION refuse_physical_retention_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='knowledge.retention_completed' THEN RAISE EXCEPTION 'synthetic purge audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER refuse_physical_retention_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION refuse_physical_retention_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Cleanup(ctx); e == nil {
		t.Fatal("physical purge committed without its audit")
	}
	var sources, chunks, attempts, documents int
	counts := func() {
		t.Helper()
		if e := s.DB.QueryRow(ctx, `SELECT(SELECT count(*) FROM knowledge_source_versions WHERE document_id=$1),(SELECT count(*) FROM knowledge_chunks WHERE document_id=$1),(SELECT count(*) FROM knowledge_attempts WHERE document_id=$1),(SELECT count(*) FROM knowledge_documents WHERE id=$1)`, deleted.ID).Scan(&sources, &chunks, &attempts, &documents); e != nil {
			t.Fatal(e)
		}
	}
	counts()
	if sources != 1 || chunks != 1 || attempts != 1 || documents != 1 {
		t.Fatal("failed purge did not roll back source/vector/attempt/tombstone together")
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER refuse_physical_retention_audit ON audit_events; DROP FUNCTION refuse_physical_retention_audit()"); e != nil {
		t.Fatal(e)
	}
	purged, e := s.Cleanup(ctx)
	if e != nil || purged.Chunks != 1 || purged.Attempts != 1 || purged.Sources != 1 || purged.Documents != 1 {
		t.Fatal("actual bounded physical purge", e)
	}
	counts()
	if sources+chunks+attempts+documents != 0 {
		t.Fatal("unreachable original bytes or vectors survived eligible purge")
	}
	protected, e := s.GetDocument(ctx, p, w, base.ID, active.ID)
	if e != nil || protected.VisibleSource == nil {
		t.Fatal("active source purged", e)
	}
	original, e := s.GetSource(ctx, p, w, base.ID, active.ID, protected.VisibleSource.ID)
	if e != nil || original.Content != text || original.SHA256 != hashBytes([]byte(text)) {
		t.Fatal("active original changed", e)
	}
	current, e := s.GetBase(ctx, p, w, base.ID)
	if e != nil || current.ActiveGeneration.ID != base.ActiveGeneration.ID || current.DocumentCount != 1 || current.ReadyDocuments != 1 {
		t.Fatal("active generation changed during retention", e)
	}
	again, e := s.Cleanup(ctx)
	if e != nil || again.Chunks+again.Attempts+again.Sources+again.Documents != 0 {
		t.Fatal("repeated purge mutated retained active data", e)
	}
	var audits int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='knowledge.retention_completed'").Scan(&audits); e != nil || audits != 1 {
		t.Fatal("purge audit duplicate/missing", e)
	}
}
