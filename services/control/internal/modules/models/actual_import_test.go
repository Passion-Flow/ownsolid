//go:build model_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

//go:embed testdata/smollm2-model-catalog.json
var alternateModelManifest []byte

func TestActualModelImportPublicationAuditRollbackAndFreshWorkerRecovery(t *testing.T) {
	s := fixture.NewWithSetup(t, fixture.SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30})
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
	var alternateChunk []byte
	if path := os.Getenv("OWNSOLID_TEST_ALTERNATE_MODEL_ARCHIVE"); path != "" {
		alternateChunk = readAlternateModelChunk(t, path)
		var alternate wire.ModelCatalog
		if e = json.Unmarshal(alternateModelManifest, &alternate); e != nil {
			t.Fatal(e)
		}
		canonical, e := json.Marshal(alternate)
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(canonical)
		unavailable := input
		unavailable.CatalogDigest = hex.EncodeToString(digest[:])
		// Use an otherwise admissible size so the rejection proves digest admission.
		if _, e = service.StartImport(ctx, admin, unavailable, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatalf("unconsumable SmolLM2 digest was admitted: %v", e)
		}
	}
	value, e := service.StartImport(ctx, admin, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("Uploading %d bytes from the pinned local archive", info.Size())
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
		if offset == 0 && alternateChunk != nil {
			resumed := *service
			service = &resumed
			different := sha256.Sum256(alternateChunk)
			if _, e = service.UploadChunk(ctx, admin, value.ID, 0, int64(n), hex.EncodeToString(different[:]), bytes.NewReader(alternateChunk)); e == nil || e.Error() != "MODEL_CHUNK_CONFLICT" {
				t.Fatalf("alternate archive replay was accepted: %v", e)
			}
			replay, e := service.UploadChunk(ctx, admin, value.ID, 0, int64(n), hex.EncodeToString(hash[:]), bytes.NewReader(buffer[:n]))
			if e != nil || replay.UploadedBytes != int64(n) || replay.CatalogDigest != input.CatalogDigest {
				t.Fatalf("original chunk could not resume with its durable digest and offset: %v", e)
			}
			t.Log("Resumed Qwen import refused the SmolLM2 chunk and accepted the original replay")
		}
		offset += int64(n)
		if offset <= 3*models.ChunkBytes || offset%(32*models.ChunkBytes) == 0 || offset == info.Size() {
			t.Logf("Committed upload offset %d of %d bytes", offset, info.Size())
		}
	}
	t.Log("Verifying model and injecting publication audit failure")
	if _, e = service.QueueImport(ctx, admin, value.ID, "Actual immutable publication", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_model_ready() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.import_ready' THEN RAISE EXCEPTION 'synthetic publication audit failure';END IF;RETURN NEW;END$$;CREATE TRIGGER reject_model_ready BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_model_ready()`); e != nil {
		t.Fatal(e)
	}
	_, publicationError := service.ProcessNext(ctx)
	if publicationError == nil {
		t.Fatal("publication audit rollback was ignored")
	}
	if models, e := service.List(ctx, admin, input.WorkspaceID); e != nil || len(models) != 0 {
		t.Fatal("model metadata survived audit rollback", e)
	}
	if _, e = os.Stat(filepath.Join(root, "objects", value.ID, "model.safetensors")); e != nil {
		t.Fatalf("recoverable verified filesystem publication was lost: %v (worker: %v)", e, publicationError)
	}
	upload := filepath.Join(root, "imports", value.ID)
	if _, e = os.Stat(filepath.Join(upload, "chunk-000000000000")); e != nil {
		t.Fatal("failed publication lost resumable upload", e)
	}
	t.Log("Publication rolled back and the upload remained available")
	oldStage := filepath.Join(upload, "stage-1")
	if e = os.Mkdir(oldStage, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(oldStage, "config.json"), []byte("unfinished stage"), 0600); e != nil {
		t.Fatal(e)
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
	t.Log("Fresh worker published the verified revision")
	// Lose the SQL cleanup after filesystem removal. A second worker must
	// discover the retained work from SQL even though the directory is gone.
	if _, e = s.DB.Exec(ctx, `CREATE SEQUENCE chunk_cleanup_attempt;CREATE FUNCTION reject_chunk_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM nextval('chunk_cleanup_attempt');RAISE EXCEPTION 'synthetic cleanup acknowledgement failure';END$$;CREATE TRIGGER reject_chunk_cleanup BEFORE DELETE ON model_import_chunks FOR EACH ROW EXECUTE FUNCTION reject_chunk_cleanup()`); e != nil {
		t.Fatal(e)
	}
	waitForModelWorker(t, &fresh, func() bool {
		var attempted bool
		if e := s.DB.QueryRow(ctx, "SELECT is_called FROM chunk_cleanup_attempt").Scan(&attempted); e != nil {
			t.Fatal(e)
		}
		_, e := os.Lstat(upload)
		return attempted && os.IsNotExist(e)
	})
	chunksRemain := func() bool {
		t.Helper()
		var exists bool
		if e := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_import_chunks WHERE import_id=$1)`, value.ID).Scan(&exists); e != nil {
			t.Fatal(e)
		}
		return exists
	}
	if !chunksRemain() {
		t.Fatal("failed SQL cleanup lost the retry marker")
	}
	t.Log("Upload and failed stage removed while SQL cleanup was blocked")
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_chunk_cleanup ON model_import_chunks"); e != nil {
		t.Fatal(e)
	}
	waitForModelWorker(t, &fresh, func() bool { return !chunksRemain() })
	reconciled, e := fresh.GetImport(ctx, admin, value.ID)
	if e != nil || reconciled.State != "ready" || reconciled.UploadedBytes != value.ExpectedBytes {
		t.Fatal("staging cleanup changed published import history", e)
	}
	if e = fresh.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("published files were unreadable after cleanup", e)
	}
	t.Log("Restarted worker cleared the chunk records and the published files verified")
	t.Run("snapshot-and-retirement", func(t *testing.T) {
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
	})
}

func readAlternateModelChunk(t *testing.T, path string) []byte {
	t.Helper()
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	chunk := make([]byte, models.ChunkBytes)
	if _, err = io.ReadFull(source, chunk); err != nil {
		t.Fatal(err)
	}
	return chunk
}

// waitForModelWorker runs a fresh instance of the installed background worker.
func waitForModelWorker(t *testing.T, service *models.Service, complete func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !complete() {
		select {
		case <-ctx.Done():
			t.Fatal("model cleanup did not reach the expected durable state")
		case <-ticker.C:
		}
	}
}
