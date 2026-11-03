//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type cumulativeKnowledgeRecovery struct {
	service                          *knowledge.Service
	base                             knowledge.Base
	document                         knowledge.Document
	source                           knowledge.Source
	vector                           string
	buildingBase, buildingGeneration string
}

func knowledgeRecoverySHA(s string) string {
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:])
}

// Computation and persisted vectors are real. The separate knowledge native
// crash-after-claim profile covers a running lease; this matched four-store
// snapshot deliberately retains a real pending replacement and building index.
func prepareCumulativeKnowledgeRecovery(t *testing.T, g preparationScenario) *cumulativeKnowledgeRecovery {
	t.Helper()
	a := g.f.s.Identity
	if !a.Config.KnowledgeRuntime() {
		return nil
	}
	repo := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	policy := a.Config.Policy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 20
	a.Config.Runtime = &policy
	a.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(repo, "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: snapshotTestDirectory(t)}
	processor, e := knowledge.NewProcessor(a.Config.Knowledge, a.Config.Policy().Knowledge)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(processor.Close)
	s := &knowledge.Service{DB: a.DB, Identity: a, Workspaces: &workspaces.Service{DB: a.DB, Identity: a}, Processor: processor}
	ctx := t.Context()
	w := g.f.admin.Me.Workspace.ID
	b, e := s.CreateBase(ctx, g.f.admin, knowledge.CreateBase{WorkspaceID: w, Name: "Matched cumulative restore facts", Reason: "Real retained source with all four stores"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	text := "The project launch city is Lisbon.\r\n"
	d, e := s.Upload(ctx, g.f.admin, w, b.ID, knowledge.UploadDocument{Name: "matched-facts.txt", Content: text, SHA256: knowledgeRecoverySHA(text), Reason: "Actual offline encoder before matched backup"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if work, e := s.ProcessNext(ctx); e != nil || !work {
		t.Fatal("actual cumulative encoding", e)
	}
	d, e = s.GetDocument(ctx, g.f.admin, w, b.ID, d.ID)
	if e != nil || d.State != "ready" || d.VisibleSource == nil {
		t.Fatal("actual ready source", e)
	}
	source, e := s.GetSource(ctx, g.f.admin, w, b.ID, d.ID, d.VisibleSource.ID)
	if e != nil || source.Content != text {
		t.Fatal("actual source bytes", e)
	}
	var vector string
	if e = a.DB.QueryRow(ctx, "SELECT embedding::text FROM knowledge_chunks WHERE document_id=$1 ORDER BY ordinal LIMIT 1", d.ID).Scan(&vector); e != nil {
		t.Fatal(e)
	}
	// The canonical API refuses a rebuild on a base with pending ingestion.
	// Use an independently ready base so both real kinds can coexist in backup.
	rebuilding, e := s.CreateBase(ctx, g.f.admin, knowledge.CreateBase{WorkspaceID: w, Name: "Matched building index facts", Reason: "Independent real generation beside a pending replacement"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Upload(ctx, g.f.admin, w, rebuilding.ID, knowledge.UploadDocument{Name: "building-facts.txt", Content: text, SHA256: knowledgeRecoverySHA(text), Reason: "Actual independent generation source"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if work, e := s.ProcessNext(ctx); e != nil || !work {
		t.Fatal("actual independent source encoding", e)
	}
	rebuilding, e = s.GetBase(ctx, g.f.admin, w, rebuilding.ID)
	if e != nil {
		t.Fatal(e)
	}
	text = "The updated project launch city is Porto."
	if _, e = s.Replace(ctx, g.f.admin, w, b.ID, d.ID, knowledge.ReplaceSource{Content: text, SHA256: knowledgeRecoverySHA(text), ExpectedRevision: d.Revision, Reason: "Retain an admitted pending replacement in matched backup"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}

	rebuilding, e = s.Rebuild(ctx, g.f.admin, w, rebuilding.ID, knowledge.RevisionReason{ExpectedRevision: rebuilding.Revision, Reason: "Retain a complete building generation in matched backup"}, uuid.NewString(), uuid.NewString())
	if e != nil || rebuilding.BuildingGeneration == nil {
		t.Fatal("actual admitted independent generation", e)
	}
	return &cumulativeKnowledgeRecovery{service: s, base: b, document: d, source: source, vector: vector, buildingBase: rebuilding.ID, buildingGeneration: rebuilding.BuildingGeneration.ID}

}

func verifyCumulativeKnowledgeRecovery(t *testing.T, state *cumulativeKnowledgeRecovery, g preparationScenario, pool *pgxpool.Pool) {
	t.Helper()
	if state == nil {
		return
	}
	ctx := t.Context()
	a := *g.f.s.Identity
	a.DB = pool
	a.Storage = nil
	s := *state.service
	s.DB = pool
	s.Identity = &a
	s.Workspaces = &workspaces.Service{DB: pool, Identity: &a}
	if _, e := s.GetBase(ctx, g.f.admin, state.base.WorkspaceID, state.base.ID); e == nil {
		t.Fatal("old knowledge authority survived matched restore")
	}
	fresh := fixture.Login(t, &a, "admin@example.invalid", g.f.password, "admin")
	if e := s.VerifyStored(ctx); e != nil {
		t.Fatal("restored exact knowledge catalog", e)
	}
	b, e := s.GetBase(ctx, fresh, state.base.WorkspaceID, state.base.ID)
	if e != nil || b.BuildingGeneration != nil || b.ActiveGeneration.ID != state.base.ActiveGeneration.ID {
		t.Fatal("restored building index not fenced", e)
	}
	other, e := s.GetBase(ctx, fresh, b.WorkspaceID, state.buildingBase)
	if e != nil || other.BuildingGeneration != nil {
		t.Fatal("independent restored building base retained authority", e)
	}
	var generationState string
	if e = pool.QueryRow(ctx, "SELECT state FROM knowledge_generations WHERE id=$1 AND base_id=$2", state.buildingGeneration, state.buildingBase).Scan(&generationState); e != nil || generationState != "failed" {
		t.Fatal("restored building generation not failed", e)
	}
	d, e := s.GetDocument(ctx, fresh, b.WorkspaceID, b.ID, state.document.ID)
	if e != nil || d.State != "failed" || d.VisibleSource == nil || d.VisibleSource.ID != state.source.SourceID || d.CurrentAttempt.ErrorCode == nil || *d.CurrentAttempt.ErrorCode != "KNOWLEDGE_WORKER_INTERRUPTED" {
		t.Fatal("restored attempt/last-good fence", e)
	}
	source, e := s.GetSource(ctx, fresh, b.WorkspaceID, b.ID, d.ID, state.source.SourceID)
	if e != nil || source.Content != state.source.Content || source.SHA256 != state.source.SHA256 {
		t.Fatal("matched restored source bytes", e)
	}
	var vector string
	if e = pool.QueryRow(ctx, "SELECT embedding::text FROM knowledge_chunks WHERE document_id=$1 ORDER BY ordinal LIMIT 1", d.ID).Scan(&vector); e != nil || vector != state.vector {
		t.Fatal("matched restored vector bytes", e)
	}
	var live int
	if e = pool.QueryRow(ctx, "SELECT count(*) FROM knowledge_attempts WHERE state IN ('pending','running') OR lease_hash IS NOT NULL OR lease_expires_at IS NOT NULL").Scan(&live); e != nil || live != 0 {
		t.Fatal("old knowledge work retained live authority", e)
	}
	found, e := s.Search(ctx, fresh, b.WorkspaceID, b.ID, knowledge.SearchRequest{Query: "Which city does the project launch in?", TopK: 1, MinimumScore: 0.1}, uuid.NewString())
	if e != nil || len(found.Matches) != 1 || found.Matches[0].SourceID != source.SourceID {
		t.Fatal("matched restored real retrieval", e)
	}
	retry, e := s.Retry(ctx, fresh, b.WorkspaceID, b.ID, d.ID, knowledge.RevisionReason{ExpectedRevision: d.Revision, Reason: "Explicit fresh local attempt after matched four-store restore"}, uuid.NewString(), uuid.NewString())
	if e != nil || retry.CurrentAttempt.ID == d.CurrentAttempt.ID {
		t.Fatal("fresh restored retry", e)
	}
	if work, e := s.ProcessNext(ctx); e != nil || !work {
		t.Fatal("real restored retry encoding", e)
	}
	ready, e := s.GetDocument(ctx, fresh, b.WorkspaceID, b.ID, d.ID)
	if e != nil || ready.State != "ready" || ready.PendingSource != nil || ready.VisibleSource.ID == source.SourceID {
		t.Fatal("new replacement did not commit", e)
	}
	t.Log("Actual cumulative matched four-store+SQL+SSO-keyring restore retained exact source/vector bytes, fenced pending replacement/building generation/old session, retrieved last-good data and required a fresh local explicit real-encoder retry; separate native crash profile covers running leases")
}
