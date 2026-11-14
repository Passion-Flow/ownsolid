//go:build dataset_processor_integration && mongodb_integration

package datasets

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// This gate consumes an explicitly prepared read-only synthetic MongoDB fixture.
// It never creates a MongoDB instance, user, database or collection.
func mongoIntegrationSource(t *testing.T) config.MongoDBSource {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_MONGODB_TEST_SOURCE_FILE")
	if path == "" {
		t.Fatal("explicit synthetic MongoDB source configuration is required")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("fixture configuration unavailable")
	}
	var source config.MongoDBSource
	if wire.DecodeStrict(raw, &source) != nil {
		t.Fatal("fixture source configuration invalid")
	}
	secret, e := config.MongoDBSecret(source.URIFile)
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(strings.TrimSpace(string(secret)))
	if e != nil || u.Scheme != "mongodb" || u.Hostname() != "127.0.0.1" || !strings.HasSuffix(source.Database, "_test") || !strings.HasPrefix(source.Collection, "m21_") {
		t.Fatal("isolated loopback synthetic test source required")
	}
	source.Fields = []string{"question", "answer"}
	return source
}
func TestMongoDBActualSourceToImmutableVersionAndReplay(t *testing.T) {
	source := mongoIntegrationSource(t)
	s, a, portal := workerScenario(t)
	assertMongoDBSourceToImmutableVersion(t, source, s, a, portal)
}

func assertMongoDBSourceToImmutableVersion(t *testing.T, source config.MongoDBSource, s *Service, a, portal identity.Auth) {
	t.Helper()
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	source.TenantID = a.Me.Workspace.TenantID
	source.WorkspaceID = workspace
	p := config.DefaultMongoDBConfiguration()
	p.Enabled = true
	p.BatchSize = 2
	p.Sources = []config.MongoDBSource{source}
	s.Identity.Config.MongoDB = p
	original, e := mongodatasets.Read(ctx, source, p, mongodatasets.Mapping{Prompt: "question", Completion: "answer", Conversion: "string"}, s.policy().Limits, false)
	if e != nil {
		t.Fatal(e)
	}
	if original.Rows != 8 {
		t.Fatal("expected exact eight-row synthetic source")
	}
	lines := strings.Split(strings.TrimSpace(string(original.Data)), "\n")
	for i, line := range lines {
		r, e := wire.DecodeRecord([]byte(line), s.policy().Limits)
		if e != nil || r.Prompt != fmt.Sprintf("M21 synthetic prompt %d", i+1) || r.Completion != fmt.Sprintf("M21 synthetic answer %d", i+1) {
			t.Fatal("source is not the declared synthetic fixture")
		}
	}
	dataset, e := s.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "M21 " + uuid.NewString(), Origin: "Synthetic read-only MongoDB integration fixture", License: "Synthetic CC0", RightsAsserted: true, Reason: "Verify actual source import"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	input := MongoDBRequest{SourceID: source.ID, PromptField: "question", CompletionField: "answer", Conversion: "string", Reason: "Import synthetic MongoDB fixture"}
	key := uuid.NewString()
	checked, e := s.MongoDBPreflight(ctx, a, workspace, dataset.ID, input)
	if e != nil || checked.Rows != 1 || len(checked.Data) != 0 {
		t.Fatal("preflight did not execute bounded source read", e)
	}
	receipt, e := s.MongoDBImport(ctx, a, workspace, dataset.ID, input, key, uuid.NewString())
	if e != nil || receipt.State != "imported" || receipt.DraftID == nil || *receipt.Rows != 8 {
		t.Fatal("actual import failed", e)
	}
	replay, e := s.MongoDBImport(ctx, a, workspace, dataset.ID, input, key, uuid.NewString())
	if e != nil || replay.ID != receipt.ID || *replay.DraftID != *receipt.DraftID {
		t.Fatal("replay created another draft", e)
	}
	conflict := input
	conflict.Reason = "Different intent"
	if _, e = s.MongoDBImport(ctx, a, workspace, dataset.ID, conflict, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed intent replay admitted", e)
	}
	if _, e = s.MongoDBGetImport(ctx, portal, uuid.NewString(), dataset.ID, receipt.ID); e == nil {
		t.Fatal("cross-Workspace receipt exposed")
	}
	// Simulate an uncertain attachment acknowledgement while retaining the exact durable export.
	if _, e = s.DB.Exec(ctx, "UPDATE mongodb_dataset_imports SET state='failed',draft_id=NULL,lease_expires_at=NULL,error_code='STORAGE_UNAVAILABLE' WHERE id=$1", receipt.ID); e != nil {
		t.Fatal(e)
	}
	recovered, e := s.MongoDBImport(ctx, a, workspace, dataset.ID, input, key, uuid.NewString())
	if e != nil || recovered.ID != receipt.ID || *recovered.DraftID != *receipt.DraftID {
		t.Fatal("durable checkpoint did not recover the same draft", e)
	}
	process(t, s)
	draft, e := s.GetDraft(ctx, a, workspace, dataset.ID, *receipt.DraftID)
	if e != nil || draft.State != "editing" || draft.InvalidRows == nil || *draft.InvalidRows != 0 {
		t.Fatal("native validation failed", e)
	}
	publication, e := s.Publish(ctx, a, workspace, dataset.ID, draft.ID, PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish MongoDB source fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	job, e := s.GetJob(ctx, a, workspace, dataset.ID, publication.ID)
	if e != nil || job.VersionID == nil {
		t.Fatal("native publication failed", e)
	}
	version, e := s.GetVersion(ctx, a, workspace, dataset.ID, *job.VersionID)
	if e != nil || version.Manifest.UniqueRows != 8 {
		t.Fatal("immutable version rows differ", e)
	}
	if e = s.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
	// Portal uses a delegated membership authority, unlike the Admin branch.
	// Listing must close its SQL result stream before per-receipt authority SQL.
	portalKey := uuid.NewString()
	portalReceipt, e := s.MongoDBImport(ctx, portal, workspace, dataset.ID, input, portalKey, uuid.NewString())
	if e != nil || portalReceipt.DraftID == nil {
		t.Fatal("actual Portal import", e)
	}
	history, e := s.MongoDBImports(ctx, portal, workspace, dataset.ID)
	found := false
	for _, v := range history {
		if v.ID == portalReceipt.ID {
			found = true
		}
	}
	if e != nil || !found {
		t.Fatal("Portal history lost current receipt", e)
	}
	changedGrant := source
	changedGrant.Collection = "m21_changed_grant"
	s.Identity.Config.MongoDB.Sources = []config.MongoDBSource{changedGrant}
	if _, e = s.MongoDBGetImport(ctx, portal, workspace, dataset.ID, portalReceipt.ID); e == nil || e.Error() != "MONGODB_SOURCE_CHANGED" {
		t.Fatal("changed grant exposed old receipt", e)
	}
	history, e = s.MongoDBImports(ctx, portal, workspace, dataset.ID)
	if e != nil || len(history) != 0 {
		t.Fatal("changed grant exposed old history", e)
	}
	if _, e = s.MongoDBImport(ctx, portal, workspace, dataset.ID, input, portalKey, uuid.NewString()); e == nil || e.Error() != "MONGODB_SOURCE_CHANGED" {
		t.Fatal("changed grant replay adopted old checkpoint", e)
	}
	s.Identity.Config.MongoDB.Sources = []config.MongoDBSource{source}
	if e = s.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
	s.Identity.Config.MongoDB.Sources = nil
	if _, e = s.MongoDBGetImport(ctx, a, workspace, dataset.ID, receipt.ID); e == nil {
		t.Fatal("removed source grant still readable")
	}
}
