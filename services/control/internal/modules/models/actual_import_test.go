//go:build model_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestActualModelImportPublicationAuditRollbackAndFreshWorkerRecovery(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	service := &models.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}, RootDirectory: root}
	service.Referenced = (&deployments.Service{Nodes: &nodes.Service{DB: s.DB}}).HasModelReferences
	archive := filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/models/qwen3-0.6b", wire.ApprovedModel().Revision, "qwen3-0.6b.tar")
	file, e := os.Open(archive)
	if e != nil {
		t.Fatal("actual frozen public package required", e)
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		t.Fatal(e)
	}
	input := models.ImportRequest{WorkspaceID: admin.Me.Workspace.ID, Name: "Actual approved offline model", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: info.Size(), LicenseAcknowledged: true, Reason: "Actual isolated import evidence"}
	value, e := service.StartImport(ctx, admin, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	buffer := make([]byte, models.ChunkBytes)
	for offset := int64(0); offset < info.Size(); {
		n, e := io.ReadFull(file, buffer)
		if e != nil && e != io.ErrUnexpectedEOF {
			t.Fatal(e)
		}
		hash := sha256.Sum256(buffer[:n])
		if _, e = service.UploadChunk(ctx, admin, value.ID, offset, int64(n), hex.EncodeToString(hash[:]), bytes.NewReader(buffer[:n])); e != nil {
			t.Fatal(e)
		}
		offset += int64(n)
	}
	if _, e = service.QueueImport(ctx, admin, value.ID, "Actual immutable publication", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_model_ready() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.import_ready' THEN RAISE EXCEPTION 'synthetic publication audit failure';END IF;RETURN NEW;END$$;CREATE TRIGGER reject_model_ready BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_model_ready()`); e != nil {
		t.Fatal(e)
	}
	if _, e = service.ProcessNext(ctx); e == nil {
		t.Fatal("publication audit rollback was ignored")
	}
	if models, e := service.List(ctx, admin, input.WorkspaceID); e != nil || len(models) != 0 {
		t.Fatal("model metadata survived audit rollback", e)
	}
	if _, e = os.Stat(filepath.Join(root, "objects", value.ID, "model.safetensors")); e != nil {
		t.Fatal("recoverable verified filesystem publication was lost", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_model_ready ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = service.QueueImport(ctx, admin, value.ID, "Retry verified publication", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	fresh := *service
	if _, e = fresh.ProcessNext(ctx); e != nil {
		t.Fatal("fresh worker did not reconcile actual publication", e)
	}
	result, e := fresh.GetImport(ctx, admin, value.ID)
	if e != nil || result.State != "ready" || result.ModelID == nil {
		t.Fatal("real import never became ready", e)
	}
	list, e := fresh.List(ctx, admin, input.WorkspaceID)
	if e != nil || len(list) != 1 || list[0].Kind != "approved_base" || list[0].CatalogDigest != wire.ModelCatalogDigest() {
		t.Fatal("immutable model metadata absent", e)
	}
	var catalog wire.ModelCatalog
	if e = json.Unmarshal(list[0].Manifest, &catalog); e != nil || catalog.Revision != wire.ApprovedModel().Revision {
		t.Fatal("exact approved base catalog revision absent from typed model manifest", e)
	}
	backupParent := t.TempDir()
	os.Chmod(backupParent, 0700)
	backup := filepath.Join(backupParent, "model-backup")
	if e = fresh.ExportFiles(ctx, backup); e != nil {
		t.Fatal("actual immutable file snapshot", e)
	}
	restored := fresh
	restored.RootDirectory = t.TempDir()
	os.Chmod(restored.RootDirectory, 0700)
	if e = restored.ImportFiles(ctx, backup); e != nil {
		t.Fatal("actual model-file snapshot restore", e)
	}
	if e = restored.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("restored model inventory/file integrity", e)
	}
	if e = restored.ImportFiles(ctx, backup); e == nil || e.Error() != "MODEL_STORE_NOT_EMPTY" {
		t.Fatal("restore overwrote existing data", e)
	}
	if retired, e := fresh.Retire(ctx, admin, value.ID, "Retire actual unreferenced model while preserving recovery assets", uuid.NewString()); e != nil || retired.State != "deleted" {
		t.Fatal("actual model retirement", e)
	}
	if e = modelartifact.VerifyDirectory(ctx, filepath.Join(root, "objects", value.ID)); e != nil {
		t.Fatal("retirement modified immutable recovery files", e)
	}
	if versions, e := fresh.List(ctx, admin, input.WorkspaceID); e != nil || len(versions) != 0 {
		t.Fatal("retired model still admitted", e)
	}
	t.Log("Actual official 1.52 GB model uploaded in bounded resumable chunks, verified, published, audit-rolled-back and reconciled by a fresh worker; no GPU inference claim.")
}
