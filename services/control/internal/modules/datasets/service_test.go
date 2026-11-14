package datasets_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func setup(t *testing.T) (*datasets.Service, identity.Auth, identity.Auth) {
	t.Helper()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	root := filepath.Join(t.TempDir(), "store")
	os.Mkdir(root, 0700)
	if e := datasets.InitializeStore(root, admin.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, admin.Me.Workspace.TenantID, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	return &datasets.Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}, Store: store}, admin, portal
}
func createInput(workspace string) datasets.CreateRequest {
	return datasets.CreateRequest{WorkspaceID: workspace, Name: "Synthetic corpus", Origin: "Synthetic local fixture", License: "CC0 synthetic fixture", RightsAsserted: true, Reason: "Synthetic lifecycle acceptance"}
}
func TestDatasetCreationIntentScopeDraftCountsAndAuditAtomicity(t *testing.T) {
	s, a, p := setup(t)
	ctx := context.Background()
	input := createInput(a.Me.Workspace.ID)
	key := uuid.NewString()
	first, e := s.Create(ctx, p, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Create(ctx, p, input, key, uuid.NewString())
	if e != nil || first.ID != again.ID {
		t.Fatal("creation intent", e)
	}
	changed := input
	changed.License = "Changed"
	if _, e = s.Create(ctx, p, changed, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed replay %v", e)
	}
	other, e := s.Workspaces.Create(ctx, a, "Other dataset workspace", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, a, other.ID, first.ID); e == nil || e.Error() != "DATASET_NOT_FOUND" {
		t.Fatalf("scope %v", e)
	}
	raw := []byte("{\"source_id\":\"a\",\"prompt\":\"p\",\"completion\":\"c\"}\n")
	sum := sha256.Sum256(raw)
	request := datasets.DraftRequest{SourceName: "fixture.jsonl", ExpectedBytes: int64(len(raw)), SourceSHA256: hex.EncodeToString(sum[:]), Reason: "Synthetic upload"}
	draftKey := uuid.NewString()
	draft, e := s.CreateDraft(ctx, p, input.WorkspaceID, first.ID, request, draftKey, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if draft.State != "uploading" || draft.TotalRows != nil || draft.InvalidRows != nil || draft.ValidationCompletedAt != nil {
		t.Fatal("unvalidated rows were presented as actual zero")
	}
	replay, e := s.CreateDraft(ctx, p, input.WorkspaceID, first.ID, request, draftKey, uuid.NewString())
	if e != nil || replay.ID != draft.ID {
		t.Fatal("draft intent", e)
	}
	if _, e = s.GetDraft(ctx, p, other.ID, first.ID, draft.ID); e == nil {
		t.Fatal("foreign draft scope")
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_dataset_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic audit failure'; END$$;CREATE TRIGGER reject_dataset_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_dataset_audit()`); e != nil {
		t.Fatal(e)
	}
	input.Name = "Must rollback"
	if _, e = s.Create(ctx, p, input, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("audit failure ignored")
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM datasets WHERE name='Must rollback'").Scan(&count); e != nil || count != 0 {
		t.Fatal("audit rollback", e)
	}
}
func TestDatasetDraftCurrentRoleAndAccountRechecked(t *testing.T) {
	s, a, _ := setup(t)
	ctx := context.Background()
	password := fixture.Password()
	invite, e := s.Identity.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "dataset-member@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Identity.Register(ctx, fixture.InvitationToken(t, s.Identity, invite.ID), "Dataset member", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	member := fixture.Login(t, s.Identity, "dataset-member@example.invalid", password, "portal")
	input := createInput(a.Me.Workspace.ID)
	data, e := s.Create(ctx, member, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", input.WorkspaceID, member.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	request := datasets.DraftRequest{SourceName: "fixture.jsonl", ExpectedBytes: 1, SourceSHA256: hex.EncodeToString(make([]byte, 32)), Reason: "Must not write"}
	if _, e = s.CreateDraft(ctx, member, input.WorkspaceID, data.ID, request, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatalf("stale member role %v", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", member.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, member, input.WorkspaceID, data.ID); e == nil {
		t.Fatal("stale account epoch accepted")
	}
}
