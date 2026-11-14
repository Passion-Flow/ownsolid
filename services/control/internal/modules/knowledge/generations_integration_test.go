//go:build knowledge_database_integration && knowledge_encoder_integration

package knowledge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestM12ActualGenerationRebuildAtomicSwapFailureRollbackAndNoMixedVectors(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	workspace := a.Me.Workspace.ID
	base := createFacts(t, s, p, workspace)
	first := uploadFacts(t, s, p, workspace, base.ID)
	secondText := "The backup project launch city is Porto. All source references remain exact."
	if _, e := s.Upload(ctx, p, workspace, base.ID, UploadDocument{Name: "backup.txt", Content: secondText, SHA256: hashBytes([]byte(secondText)), Reason: "Actual second rebuild source"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	processOne(t, s)
	before, e := s.GetBase(ctx, p, workspace, base.ID)
	if e != nil || before.ReadyDocuments != 2 {
		t.Fatal(e)
	}
	policy := s.Identity.Config.Policy()
	policy.Knowledge.ChunkTokens = 32
	policy.Knowledge.OverlapTokens = 8
	s.Identity.Config.Runtime = &policy
	// The test applies the same validated settings a fresh configured process receives;
	// it does not claim a production configuration hot-reload feature.
	key := uuid.NewString()
	input := RevisionReason{ExpectedRevision: before.Revision, Reason: "Actual complete generation rebuild"}
	started, e := s.Rebuild(ctx, p, workspace, base.ID, input, key, uuid.NewString())
	if e != nil || started.BuildingGeneration == nil || started.ActiveGeneration.ID != before.ActiveGeneration.ID || started.BuildingGeneration.ExpectedDocuments != 2 {
		t.Fatal("frozen rebuild admission", e)
	}
	processOne(t, s)
	partial, e := s.GetBase(ctx, p, workspace, base.ID)
	if e != nil || partial.BuildingGeneration == nil || partial.ActiveGeneration.ID != before.ActiveGeneration.ID || partial.BuildingGeneration.CompletedDocuments != 1 {
		t.Fatal("partial generation activated", e)
	}
	query := SearchRequest{Query: "Which city does the project launch in?", TopK: 5, MinimumScore: 0.1}
	during, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || during.GenerationID != before.ActiveGeneration.ID {
		t.Fatal("mixed building generation retrieval", e)
	}
	processOne(t, s)
	completed, e := s.GetBase(ctx, p, workspace, base.ID)
	if e != nil || completed.BuildingGeneration != nil || completed.ActiveGeneration.ID == before.ActiveGeneration.ID || completed.ActiveGeneration.ChunkTokens != 32 || completed.ReadyDocuments != 2 {
		t.Fatal("complete atomic generation activation", e)
	}
	replay, e := s.Rebuild(ctx, p, workspace, base.ID, input, key, uuid.NewString())
	if e != nil || replay.ActiveGeneration.ID != completed.ActiveGeneration.ID || replay.BuildingGeneration != nil {
		t.Fatal("completed rebuild replay duplicated work", e)
	}
	current, e := s.GetDocument(ctx, p, workspace, base.ID, first.ID)
	if e != nil || current.State != "ready" || current.CurrentAttempt.Kind != "rebuild" {
		t.Fatal("rebuild document progress", e)
	}
	failure, e := s.Rebuild(ctx, p, workspace, base.ID, RevisionReason{ExpectedRevision: completed.Revision, Reason: "Controlled failed generation after real partial output"}, uuid.NewString(), uuid.NewString())
	if e != nil || failure.BuildingGeneration == nil {
		t.Fatal(e)
	}
	failedID := failure.BuildingGeneration.ID
	processOne(t, s)
	original := s.Processor
	s.Processor = faultProcessor(t, "import sys;sys.stderr.write('{\"error\":\"KNOWLEDGE_WORKER_FAILED\"}');sys.exit(1)", nil)
	processOne(t, s)
	s.Processor = original
	retained, e := s.GetBase(ctx, p, workspace, base.ID)
	if e != nil || retained.BuildingGeneration != nil || retained.ActiveGeneration.ID != completed.ActiveGeneration.ID {
		t.Fatal("failed rebuild replaced last complete generation", e)
	}
	found, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || found.GenerationID != completed.ActiveGeneration.ID || len(found.Matches) == 0 {
		t.Fatal("failed generation leaked into search", e)
	}
	var state string
	var staged int
	if e = s.DB.QueryRow(ctx, "SELECT state FROM knowledge_generations WHERE id=$1", failedID).Scan(&state); e != nil || state != "failed" {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_chunks WHERE generation_id=$1", failedID).Scan(&staged); e != nil || staged < 1 {
		t.Fatal("controlled failure did not retain actual staged vectors", e)
	}
}
func TestM12ActualExpiredClaimRequiresNewExplicitAttemptAndReadyIsNotDuplicated(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx := context.Background()
	base := createFacts(t, s, p, a.Me.Workspace.ID)
	document := uploadFacts(t, s, p, a.Me.Workspace.ID, base.ID)
	candidate, e := s.nextCandidate(ctx)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.claim(ctx, candidate)
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	// Explicit crash-after-claim fixture; actual source/lease state is durable in PostgreSQL.
	if _, e = s.DB.Exec(ctx, "UPDATE knowledge_attempts SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", claim.id); e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	expired, e := s.GetDocument(ctx, p, a.Me.Workspace.ID, base.ID, document.ID)
	if e != nil || expired.State != "failed" || expired.CurrentAttempt.ErrorCode == nil || *expired.CurrentAttempt.ErrorCode != "KNOWLEDGE_WORKER_INTERRUPTED" {
		t.Fatal("expired lease not fenced", e)
	}
	if _, e = s.Retry(ctx, p, a.Me.Workspace.ID, base.ID, document.ID, RevisionReason{ExpectedRevision: expired.Revision, Reason: "Explicit new attempt after interrupted claim"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	ready, e := s.GetDocument(ctx, p, a.Me.Workspace.ID, base.ID, document.ID)
	if e != nil || ready.State != "ready" || ready.CurrentAttempt.AttemptNumber != 2 {
		t.Fatal("expired lease explicit retry", e)
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_chunks WHERE document_id=$1", document.ID).Scan(&count); e != nil || count != ready.CurrentAttempt.ChunkCount {
		t.Fatal("interrupted attempt duplicated vectors", e)
	}
}
func TestM12ActualBoundedRetentionPreservesActiveSourceAndAuditFailureRollsBack(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx := context.Background()
	w := a.Me.Workspace.ID
	base := createFacts(t, s, p, w)
	document := uploadFacts(t, s, p, w, base.ID)
	processOne(t, s)
	ready, e := s.GetDocument(ctx, p, w, base.ID, document.ID)
	if e != nil {
		t.Fatal(e)
	}
	policy := s.Identity.Config.Policy()
	policy.Knowledge.RetentionDays = 1
	policy.Knowledge.CleanupBatch = 1
	s.Identity.Config.Runtime = &policy
	var tenant string
	if e = s.DB.QueryRow(ctx, "SELECT tenant_id::text FROM workspaces WHERE id=$1", w).Scan(&tenant); e != nil {
		t.Fatal(e)
	}
	// Old unreferenced source versions are explicit retention fixtures. Their bytes/hash,
	// immutability and real database delete/audit transactions remain enforced.
	for version := 2; version <= 4; version++ {
		id := uuid.NewString()
		text := []byte("Old unreachable synthetic source.")
		if _, e = s.DB.Exec(ctx, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()-interval '3 days')`, id, tenant, w, base.ID, document.ID, version, text, hashBytes(text), a.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.Exec(ctx, "UPDATE knowledge_source_versions SET superseded_at=clock_timestamp()-interval '2 days' WHERE id=$1", id); e != nil {
			t.Fatal("one-time safe source retirement", e)
		}
		_, e = s.DB.Exec(ctx, "UPDATE knowledge_source_versions SET superseded_at=clock_timestamp() WHERE id=$1", id)
		sqlCode(t, e, "23514")
	}
	result, e := s.Cleanup(ctx)
	if e != nil || result.Sources != 1 || result.Chunks != 0 {
		t.Fatal("bounded source cleanup", e)
	}
	var before int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_source_versions WHERE document_id=$1", document.ID).Scan(&before); e != nil || before != 3 {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION synthetic_retention_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='knowledge.retention_completed' THEN RAISE EXCEPTION 'synthetic retention audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER synthetic_retention_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION synthetic_retention_audit_failure()`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Cleanup(ctx); e == nil {
		t.Fatal("retention audit failure returned success")
	}
	var after int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_source_versions WHERE document_id=$1", document.ID).Scan(&after); e != nil || after != before {
		t.Fatal("retention audit rollback lost source", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER synthetic_retention_audit_failure ON audit_events"); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 3; n++ {
		result, e = s.Cleanup(ctx)
		if e != nil || result.Sources > 1 || result.Chunks > 1 || result.Attempts > 1 || result.Documents > 1 || result.Generations > 1 || result.Intents > 1 {
			t.Fatal("retention exceeded explicit row budget", e)
		}
	}
	source, e := s.GetSource(ctx, p, w, base.ID, document.ID, ready.VisibleSource.ID)
	if e != nil || source.SHA256 != ready.VisibleSource.SHA256 {
		t.Fatal("retention deleted active source", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_source_versions WHERE document_id=$1", document.ID).Scan(&after); e != nil || after != 1 {
		t.Fatal("retention did not finish old sources", e)
	}
	// A disabled configuration must remain parseable without opening old model paths.
	disabled := config.KnowledgeConfiguration{}
	if e = disabled.Validate(); e != nil {
		t.Fatal(e)
	}
}
