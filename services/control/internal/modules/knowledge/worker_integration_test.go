//go:build knowledge_database_integration && knowledge_encoder_integration

package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func processOne(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	worked, e := s.ProcessNext(ctx)
	if e != nil || !worked {
		t.Fatal("actual pending encoder processing", e)
	}
}
func TestM12ActualCPUIngestionVectorSearchLastGoodReplacementRetryAndDelete(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	workspace := a.Me.Workspace.ID
	base := createFacts(t, s, p, workspace)
	query := SearchRequest{Query: "In which city does the project launch?", TopK: 5, MinimumScore: 0.1}
	empty, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || len(empty.Matches) != 0 {
		t.Fatal("unindexed base must have real empty retrieval", e)
	}
	text := "The project launch city is Lisbon. The cabinet code is TANGERINE-42.\r\n"
	input := UploadDocument{Name: "launch.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Actual offline CPU ingestion"}
	key := uuid.NewString()
	document, e := s.Upload(ctx, p, workspace, base.ID, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	ocean := "Ocean waves erode coastal rocks. The sea is blue."
	if _, e = s.Upload(ctx, p, workspace, base.ID, UploadDocument{Name: "ocean.txt", Content: ocean, SHA256: hashBytes([]byte(ocean)), Reason: "Actual distractor document"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	processOne(t, s)
	ready, e := s.GetDocument(ctx, p, workspace, base.ID, document.ID)
	if e != nil || ready.State != "ready" || ready.PendingSource != nil || ready.VisibleSource == nil || ready.CurrentAttempt.ChunkCount < 1 {
		t.Fatal("real CPU vectors did not activate", e)
	}
	replay, e := s.Upload(ctx, p, workspace, base.ID, input, key, uuid.NewString())
	if e != nil || replay.CurrentAttempt.ID != ready.CurrentAttempt.ID {
		t.Fatal("ready replay duplicated ingestion", e)
	}
	found, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || len(found.Matches) == 0 || found.Matches[0].DocumentID != document.ID || found.Matches[0].SourceID != ready.VisibleSource.ID || !strings.Contains(found.Matches[0].Text, "Lisbon") {
		t.Fatal("real semantic retrieval/source", e)
	}
	for _, match := range found.Matches {
		source, e := s.GetSource(ctx, p, workspace, base.ID, match.DocumentID, match.SourceID)
		if e != nil || source.SHA256 != match.SourceSHA256 || source.Content[match.StartByte:match.EndByte] != match.Text {
			t.Fatal("exact source citation offsets", e)
		}
	}
	original := ready.VisibleSource.ID
	updatedText := "The project launch city is Porto. The cabinet code is TANGERINE-42.\r\n"
	replacement, e := s.Replace(ctx, p, workspace, base.ID, document.ID, ReplaceSource{Content: updatedText, SHA256: hashBytes([]byte(updatedText)), ExpectedRevision: ready.Revision, Reason: "Actual immutable replacement"}, uuid.NewString(), uuid.NewString())
	if e != nil || replacement.VisibleSource.ID != original || replacement.PendingSource.SourceVersion != 2 {
		t.Fatal("replacement discarded last good source", e)
	}
	during, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || len(during.Matches) == 0 || during.Matches[0].SourceID != original {
		t.Fatal("pending replacement removed visible source", e)
	}
	processOne(t, s)
	changed, e := s.GetDocument(ctx, p, workspace, base.ID, document.ID)
	if e != nil || changed.VisibleSource.SourceVersion != 2 || changed.VisibleSource.ID == original {
		t.Fatal("replacement activation", e)
	}
	originalProcessor := s.Processor
	s.Processor = faultProcessor(t, "import sys;sys.stderr.write('{\"error\":\"KNOWLEDGE_WORKER_FAILED\"}');sys.exit(1)", nil)
	failedText := "The project launch city is Zurich. The cabinet code is TANGERINE-42.\r\n"
	if _, e = s.Replace(ctx, p, workspace, base.ID, document.ID, ReplaceSource{Content: failedText, SHA256: hashBytes([]byte(failedText)), ExpectedRevision: changed.Revision, Reason: "Explicit failed-worker replacement fixture"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	failed, e := s.GetDocument(ctx, p, workspace, base.ID, document.ID)
	if e != nil || failed.State != "failed" || failed.VisibleSource.ID != changed.VisibleSource.ID || failed.CurrentAttempt.ErrorCode == nil || *failed.CurrentAttempt.ErrorCode != "KNOWLEDGE_WORKER_FAILED" {
		t.Fatal("failed replacement damaged last good content", e)
	}
	s.Processor = originalProcessor
	retained, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || len(retained.Matches) == 0 || !strings.Contains(retained.Matches[0].Text, "Porto") {
		t.Fatal("last good vectors lost", e)
	}
	if _, e = s.Retry(ctx, p, workspace, base.ID, document.ID, RevisionReason{ExpectedRevision: failed.Revision, Reason: "Actual restored encoder retry"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	recovered, e := s.GetDocument(ctx, p, workspace, base.ID, document.ID)
	if e != nil || recovered.State != "ready" || recovered.VisibleSource.SourceVersion != 3 || recovered.CurrentAttempt.AttemptNumber != 2 {
		t.Fatal("real explicit retry recovery", e)
	}
	recoveredSearch, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil || len(recoveredSearch.Matches) == 0 || !strings.Contains(recoveredSearch.Matches[0].Text, "Zurich") {
		t.Fatal("recovered source not retrieved", e)
	}
	if _, e = s.Delete(ctx, p, workspace, base.ID, document.ID, RevisionReason{ExpectedRevision: recovered.Revision, Reason: "Actual deletion visibility"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	after, e := s.Search(ctx, p, workspace, base.ID, query, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	for _, match := range after.Matches {
		if match.DocumentID == document.ID {
			t.Fatal("deleted document still retrieved")
		}
	}
	var attempts, vectors int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_attempts WHERE document_id=$1", document.ID).Scan(&attempts); e != nil || attempts != 4 {
		t.Fatal("unexpected attempts", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_chunks WHERE document_id=$1", document.ID).Scan(&vectors); e != nil || vectors != 3 {
		t.Fatal("duplicate/failed vectors committed", e)
	}
	var reasons string
	if e = s.DB.QueryRow(ctx, "SELECT coalesce(string_agg(reason,' '),'') FROM audit_events WHERE action LIKE 'knowledge.%'").Scan(&reasons); e != nil || strings.Contains(reasons, text) || strings.Contains(reasons, query.Query) {
		t.Fatal("source/question leaked into audit", e)
	}
}
func TestM12ActualDeleteDuringClaimedWorkerFencesOutputAndStopsChild(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := createFacts(t, s, p, a.Me.Workspace.ID)
	document := uploadFacts(t, s, p, a.Me.Workspace.ID, base.ID)
	barrier := filepath.Join(t.TempDir(), "claimed.marker")
	release := filepath.Join(t.TempDir(), "release.marker")
	// Explicit successful-output double only for a controlled deletion interleaving.
	// The independent preceding test uses actual CPU embeddings through PostgreSQL.
	program := "import sys,json,time,hashlib,os\nr=json.load(sys.stdin)\nopen(" + strconvQuote(barrier) + ",'x').close()\nwhile not os.path.exists(" + strconvQuote(release) + "): time.sleep(0.01)\nt=r['text'].encode()\njson.dump({'schema_version':1,'encoder_catalog_sha256':r['encoder_catalog_sha256'],'dimension':384,'source_sha256':hashlib.sha256(t).hexdigest(),'chunks':[{'ordinal':0,'start_byte':0,'end_byte':len(t),'token_count':1,'embedding':[1.0]+[0.0]*383}]},sys.stdout)\n"
	s.Processor = faultProcessor(t, program, nil)
	done := make(chan error, 1)
	go func() { _, e := s.ProcessNext(ctx); done <- e }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(barrier); e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, e := os.Stat(barrier); e != nil {
		t.Fatal("actual child did not reach controlled IO boundary")
	}
	current, e := s.GetDocument(ctx, p, a.Me.Workspace.ID, base.ID, document.ID)
	if e != nil || current.State != "running" {
		t.Fatal(e)
	}
	if _, e = s.Delete(ctx, p, a.Me.Workspace.ID, base.ID, document.ID, RevisionReason{ExpectedRevision: current.Revision, Reason: "Delete during claimed CPU work"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(release, []byte("release"), 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("claimed child did not stop")
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_chunks WHERE document_id=$1", document.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("cancelled lease committed vectors", e)
	}
	var state string
	if e = s.DB.QueryRow(ctx, "SELECT state FROM knowledge_attempts WHERE id=$1", document.CurrentAttempt.ID).Scan(&state); e != nil || state != "cancelled" {
		t.Fatal("deleted job changed terminal state", e)
	}
}

func TestM12ActualQueryAdmissionIsPersistentAndBeforeCPU(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx := context.Background()
	w := a.Me.Workspace.ID
	b := createFacts(t, s, p, w)
	uploadFacts(t, s, p, w, b.ID)
	processOne(t, s)
	policy := s.Identity.Config.Policy()
	policy.Knowledge.QueryCount = 1
	policy.Knowledge.QueryWindowSeconds = 120
	s.Identity.Config.Runtime = &policy
	input := SearchRequest{Query: "Where is the project launch city?", TopK: 1, MinimumScore: 0.1}
	if _, e := s.Search(ctx, p, w, b.ID, input, uuid.NewString()); e != nil {
		t.Fatal("first actual query", e)
	}
	// Close the processor after current availability has been tested separately is
	// not needed: the rate refusal is observed directly and no scratch is admitted.
	_, e := s.Search(ctx, p, w, b.ID, input, uuid.NewString())
	requireCode(t, e, "RATE_LIMITED")
	var rate *identity.RateError
	if !errors.As(e, &rate) || rate.RetrySeconds() < 1 || rate.RetrySeconds() > 121 {
		t.Fatal("bounded measured rate retry lost", e)
	}
	entries, e := os.ReadDir(s.Processor.configuration.ScratchRoot)
	if e != nil || len(entries) != 0 {
		t.Fatal("rate refusal admitted scratch", e)
	}
}
