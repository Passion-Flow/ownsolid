package models_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestPrivateSnapshotFailsClosedOnChangedMetadataExistingDataAndPendingImports(t *testing.T) {
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	s := &models.Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}, RootDirectory: root}
	parent := t.TempDir()
	os.Chmod(parent, 0700)
	snapshot := filepath.Join(parent, "new-backup")
	if e := s.ExportFiles(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e := s.ExportFiles(ctx, snapshot); e == nil || e.Error() != "MODEL_SNAPSHOT_DESTINATION_EXISTS" {
		t.Fatal("snapshot overwritten", e)
	}
	destination := *s
	destination.RootDirectory = t.TempDir()
	os.Chmod(destination.RootDirectory, 0700)
	if e := destination.ImportFiles(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e := destination.ImportFiles(ctx, snapshot); e == nil || e.Error() != "MODEL_STORE_NOT_EMPTY" {
		t.Fatal("populated store adopted", e)
	}
	original, e := os.ReadFile(filepath.Join(snapshot, "model-files.json"))
	if e != nil {
		t.Fatal(e)
	}
	var manifest models.FileSnapshot
	json.Unmarshal(original, &manifest)
	manifest.TenantID = uuid.NewString()
	changed, _ := json.Marshal(manifest)
	if e = os.WriteFile(filepath.Join(snapshot, "model-files.json"), changed, 0600); e != nil {
		t.Fatal(e)
	}
	mismatch := *s
	mismatch.RootDirectory = t.TempDir()
	os.Chmod(mismatch.RootDirectory, 0700)
	if e = mismatch.ImportFiles(ctx, snapshot); e == nil || e.Error() != "MODEL_SNAPSHOT_DATABASE_MISMATCH" {
		t.Fatal("wrong database snapshot accepted", e)
	}
	entries, e := os.ReadDir(mismatch.RootDirectory)
	if e != nil || len(entries) != 0 {
		t.Fatal("invalid backup changed target", e)
	}
	if _, e = s.StartImport(ctx, a, models.ImportRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Unfinished snapshot fixture", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: wire.ModelBytes() + 20000, LicenseAcknowledged: true, Reason: "Synthetic snapshot quiescence"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e = s.ExportFiles(ctx, filepath.Join(t.TempDir(), "refused-backup")); e == nil || e.Error() != "MODEL_IMPORT_BUSY" {
		t.Fatal("unfinished import omitted from successful backup", e)
	}
}
func TestRestoredReadyModelWithoutFilesCannotPassStartupIntegrity(t *testing.T) {
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	s := &models.Service{DB: account.DB}
	if e := s.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("legacy empty model state rejected", e)
	}
	manifest, _ := json.Marshal(wire.ApprovedModel())
	if _, e := account.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Missing restored file fixture',$4,$5,$6,'ready',$7)", uuid.NewString(), a.Me.Workspace.TenantID, a.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyStoredVersions(ctx); e == nil {
		t.Fatal("metadata-only model passed startup integrity")
	}
	s.RootDirectory = t.TempDir()
	os.Chmod(s.RootDirectory, 0700)
	if e := s.VerifyStoredVersions(ctx); e == nil {
		t.Fatal("empty file store passed restored readiness")
	}
}

func TestOperatorFileSnapshotAuditContainsInventoryDigestAndRequiresReason(t *testing.T) {
	account := fixture.New(t)
	fixture.Bootstrap(t, account)
	ctx := context.Background()
	s := &models.Service{DB: account.DB}
	if e := s.AuditFileOperation(ctx, "export", ""); e == nil {
		t.Fatal("missing operator reason accepted")
	}
	if e := s.AuditFileOperation(ctx, "unapproved", "Synthetic audit"); e == nil {
		t.Fatal("unknown operation accepted")
	}
	if e := s.AuditFileOperation(ctx, "export", "Synthetic isolated operator inventory"); e != nil {
		t.Fatal(e)
	}
	var audience, hash string
	var count int
	if e := account.DB.QueryRow(ctx, "SELECT audience,metadata->>'inventory_sha256',(metadata->>'model_versions')::integer FROM audit_events WHERE action='model.files_exported'").Scan(&audience, &hash, &count); e != nil || audience != "operator" || len(hash) != 64 || count != 0 {
		t.Fatal("missing bounded operator audit", e)
	}
}
