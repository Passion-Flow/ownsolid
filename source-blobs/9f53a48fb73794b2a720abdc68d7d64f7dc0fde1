//go:build knowledge_database_integration && knowledge_encoder_integration

package knowledge

import (
	"context"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func serviceFixture(t *testing.T) (*Service, identity.Auth, identity.Auth) {
	s, a, p, _ := serviceFixtureWithPassword(t)
	return s, a, p
}
func serviceFixtureWithPassword(t *testing.T) (*Service, identity.Auth, identity.Auth, string) {
	t.Helper()
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	a := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	p := fixture.Login(t, accounts, "admin@example.invalid", password, "portal")
	processor, configuration := actualProcessor(t)
	accounts.Config.Knowledge = configuration
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 20
	accounts.Config.Runtime = &policy
	return &Service{DB: accounts.DB, Identity: accounts, Workspaces: &workspaces.Service{DB: accounts.DB, Identity: accounts}, Processor: processor}, a, p, password
}
func requireCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("wanted %s; observed %v", code, e)
	}
}
func createFacts(t *testing.T, s *Service, a identity.Auth, workspace string) Base {
	t.Helper()
	b, e := s.CreateBase(context.Background(), a, CreateBase{WorkspaceID: workspace, Name: "Synthetic facts", Reason: "Synthetic source/version acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func uploadFacts(t *testing.T, s *Service, a identity.Auth, workspace, base string) Document {
	t.Helper()
	text := "  Caf\u00e9 😀 \u6c49\u5b57\r\nThe project launch city is Lisbon.\r\n"
	d, e := s.Upload(context.Background(), a, workspace, base, UploadDocument{Name: "facts.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Synthetic exact source upload"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestM12ActualBaseDocumentAdmissionSourceReplayRetryDeleteAndScope(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	input := CreateBase{WorkspaceID: workspace, Name: "Synthetic exact facts", Reason: "Synthetic base creation"}
	key := uuid.NewString()
	base, e := s.CreateBase(ctx, p, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.CreateBase(ctx, p, input, key, uuid.NewString())
	if e != nil || again.ID != base.ID || again.ActiveGeneration.ID != base.ActiveGeneration.ID {
		t.Fatal("base replay", e)
	}
	input.Reason = "Changed intent"
	_, e = s.CreateBase(ctx, p, input, key, uuid.NewString())
	requireCode(t, e, "IDEMPOTENCY_CONFLICT")
	text := "  Caf\u00e9 😀 \u6c49\u5b57\r\nThe project launch city is Lisbon.\r\n"
	upload := UploadDocument{Name: "facts.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Synthetic source admission"}
	uploadKey := uuid.NewString()
	document, e := s.Upload(ctx, p, workspace, base.ID, upload, uploadKey, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if document.State != "pending" || document.VisibleSource != nil || document.PendingSource == nil || document.CurrentAttempt == nil || document.CurrentAttempt.State != "pending" {
		t.Fatal("unprocessed source must remain pending")
	}
	againDoc, e := s.Upload(ctx, p, workspace, base.ID, upload, uploadKey, uuid.NewString())
	if e != nil || againDoc.ID != document.ID || againDoc.CurrentAttempt.ID != document.CurrentAttempt.ID {
		t.Fatal("source replay", e)
	}
	source, e := s.GetSource(ctx, p, workspace, base.ID, document.ID, document.PendingSource.ID)
	if e != nil || source.Content != text || source.SHA256 != upload.SHA256 || source.SizeBytes != len(text) || source.SourceVersion != 1 {
		t.Fatal("actual immutable source roundtrip", e)
	}
	changed := upload
	changed.Content += "changed"
	changed.SHA256 = hashBytes([]byte(changed.Content))
	_, e = s.Upload(ctx, p, workspace, base.ID, changed, uploadKey, uuid.NewString())
	requireCode(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = s.Replace(ctx, p, workspace, base.ID, document.ID, ReplaceSource{Content: text, SHA256: upload.SHA256, ExpectedRevision: document.Revision, Reason: "Cannot replace running input"}, uuid.NewString(), uuid.NewString())
	requireCode(t, e, "KNOWLEDGE_BUSY")
	_, e = s.Retry(ctx, p, workspace, base.ID, document.ID, RevisionReason{ExpectedRevision: document.Revision, Reason: "Cannot retry pending"}, uuid.NewString(), uuid.NewString())
	requireCode(t, e, "KNOWLEDGE_RETRY_NOT_ALLOWED")
	// Explicit failed-worker database fixture only. No successful worker/vector output is fabricated.
	if _, e = s.DB.Exec(ctx, `UPDATE knowledge_attempts SET state='failed',error_code='KNOWLEDGE_WORKER_FAILED',finished_at=clock_timestamp() WHERE id=$1;`, document.CurrentAttempt.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE knowledge_documents SET state='failed' WHERE id=$1`, document.ID); e != nil {
		t.Fatal(e)
	}
	retryKey := uuid.NewString()
	retry := RevisionReason{ExpectedRevision: document.Revision, Reason: "Explicit synthetic retry"}
	retried, e := s.Retry(ctx, p, workspace, base.ID, document.ID, retry, retryKey, uuid.NewString())
	if e != nil || retried.CurrentAttempt.AttemptNumber != 2 || retried.CurrentAttempt.ID == document.CurrentAttempt.ID {
		t.Fatal("new explicit retry attempt", e)
	}
	replay, e := s.Retry(ctx, p, workspace, base.ID, document.ID, retry, retryKey, uuid.NewString())
	if e != nil || replay.CurrentAttempt.ID != retried.CurrentAttempt.ID {
		t.Fatal("retry replay duplicated attempt", e)
	}
	_, e = s.Delete(ctx, p, workspace, base.ID, document.ID, RevisionReason{ExpectedRevision: document.Revision, Reason: "Stale deletion"}, uuid.NewString())
	requireCode(t, e, "KNOWLEDGE_REVISION_CONFLICT")
	deleted, e := s.Delete(ctx, p, workspace, base.ID, document.ID, RevisionReason{ExpectedRevision: retried.Revision, Reason: "Synthetic deletion"}, uuid.NewString())
	if e != nil || deleted.State != "deleted" || deleted.VisibleSource != nil || deleted.PendingSource != nil || deleted.CurrentAttempt.State != "cancelled" {
		t.Fatal("immediate deletion/cancel", e)
	}
	_, e = s.GetSource(ctx, p, workspace, base.ID, document.ID, document.PendingSource.ID)
	requireCode(t, e, "KNOWLEDGE_DOCUMENT_NOT_FOUND")
	page, e := s.DocumentPage(ctx, p, workspace, base.ID, url.Values{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("deleted source remains listed", e)
	}
	other, e := s.Workspaces.Create(ctx, a, "Unshared knowledge scope", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.GetBase(ctx, a, other.ID, base.ID)
	requireCode(t, e, "KNOWLEDGE_BASE_NOT_FOUND")
	var attempts, chunks int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_attempts WHERE document_id=$1", document.ID).Scan(&attempts); e != nil || attempts != 2 {
		t.Fatal("attempt duplication", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_chunks WHERE document_id=$1", document.ID).Scan(&chunks); e != nil || chunks != 0 {
		t.Fatal("metadata test fabricated vectors", e)
	}
}
func TestM12ActualCurrentWorkspaceViewerMemberDowngradeAndRemoval(t *testing.T) {
	s, a, _ := serviceFixture(t)
	ctx := context.Background()
	password, email := fixture.Password(), "knowledge-member@example.invalid"
	invite, e := s.Identity.Invite(ctx, a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Identity.Register(ctx, fixture.InvitationToken(t, s.Identity, invite.ID), "Synthetic knowledge member", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	user := fixture.Login(t, s.Identity, email, password, "portal")
	w, e := s.Workspaces.Create(ctx, a, "Scoped knowledge roles", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	member, e := s.Workspaces.AddMember(ctx, a, w.ID, email, "viewer", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	base := createFacts(t, s, a, w.ID)
	if _, e = s.GetBase(ctx, user, w.ID, base.ID); e != nil {
		t.Fatal("viewer read", e)
	}
	_, e = s.CreateBase(ctx, user, CreateBase{WorkspaceID: w.ID, Name: "Denied viewer base", Reason: "Synthetic viewer refusal"}, uuid.NewString(), uuid.NewString())
	requireCode(t, e, "ACCESS_DENIED")
	member, e = s.Workspaces.ChangeMember(ctx, a, w.ID, user.Me.User.ID, "member", member.Version, "Synthetic current member grant", uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	document := uploadFacts(t, s, user, w.ID, base.ID)
	member, e = s.Workspaces.ChangeMember(ctx, a, w.ID, user.Me.User.ID, "viewer", member.Version, "Synthetic live downgrade", uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Delete(ctx, user, w.ID, base.ID, document.ID, RevisionReason{ExpectedRevision: document.Revision, Reason: "Denied downgraded deletion"}, uuid.NewString())
	requireCode(t, e, "ACCESS_DENIED")
	if _, e = s.GetDocument(ctx, user, w.ID, base.ID, document.ID); e != nil {
		t.Fatal("current viewer read", e)
	}
	if _, e = s.Workspaces.ChangeMember(ctx, a, w.ID, user.Me.User.ID, "viewer", member.Version, "Synthetic live removal", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	_, e = s.GetBase(ctx, user, w.ID, base.ID)
	requireCode(t, e, "RESOURCE_NOT_FOUND")
}
func TestM12ActualAuditRollbackAndDisabledBasePendingCancellation(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx := context.Background()
	w := a.Me.Workspace.ID
	base := createFacts(t, s, p, w)
	if _, e := s.DB.Exec(ctx, `CREATE FUNCTION synthetic_knowledge_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'knowledge.%' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER synthetic_knowledge_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION synthetic_knowledge_audit_failure()`); e != nil {
		t.Fatal(e)
	}
	key := uuid.NewString()
	text := "Synthetic audited source."
	input := UploadDocument{Name: "audited.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Synthetic audit rollback"}
	if _, e := s.Upload(ctx, p, w, base.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("audit failure returned success")
	}
	for _, table := range []string{"knowledge_documents", "knowledge_source_versions", "knowledge_attempts"} {
		var count int
		if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE base_id=$1", base.ID).Scan(&count); e != nil || count != 0 {
			t.Fatal("audit left partial "+table, e)
		}
	}
	if _, e := s.DB.Exec(ctx, "DROP TRIGGER synthetic_knowledge_audit_failure ON audit_events"); e != nil {
		t.Fatal(e)
	}
	document, e := s.Upload(ctx, p, w, base.ID, input, key, uuid.NewString())
	if e != nil {
		t.Fatal("same-key post-rollback recovery", e)
	}
	disabled, e := s.Update(ctx, a, w, base.ID, UpdateBase{Name: base.Name, Enabled: false, ExpectedRevision: base.Revision, Reason: "Synthetic pending cancellation"}, uuid.NewString())
	if e != nil || disabled.Enabled {
		t.Fatal(e)
	}
	current, e := s.GetDocument(ctx, p, w, base.ID, document.ID)
	if e != nil || current.State != "failed" || current.CurrentAttempt.State != "cancelled" {
		t.Fatal("disable failed to cancel pending job", e)
	}
	_, e = s.GetSource(ctx, p, w, base.ID, document.ID, current.PendingSource.ID)
	requireCode(t, e, "KNOWLEDGE_BASE_DISABLED")
	restored, e := s.Update(ctx, a, w, base.ID, UpdateBase{Name: base.Name, Enabled: true, ExpectedRevision: disabled.Revision, Reason: "Synthetic explicit recovery"}, uuid.NewString())
	if e != nil || !restored.Enabled {
		t.Fatal(e)
	}
	if _, e = s.Retry(ctx, p, w, base.ID, document.ID, RevisionReason{ExpectedRevision: current.Revision, Reason: "Retry cancelled source after enable"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("explicit post-enable recovery", e)
	}
}
