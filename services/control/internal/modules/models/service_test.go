package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestModelImportIntentChunkReplayAndAuthority(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, s, "admin@example.invalid", password, "portal")
	root := t.TempDir()
	os.Chmod(root, 0700)
	service := &models.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}, RootDirectory: root}
	ctx := context.Background()
	input := models.ImportRequest{WorkspaceID: admin.Me.Workspace.ID, Name: "Synthetic import", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: wire.ModelBytes() + 20000, LicenseAcknowledged: true, Reason: "Synthetic chunk authority"}
	key := uuid.NewString()
	value, e := service.StartImport(ctx, admin, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := service.StartImport(ctx, admin, input, key, uuid.NewString())
	if e != nil || again.ID != value.ID {
		t.Fatal("import intent replay failed", e)
	}
	changed := input
	changed.Name = "Changed import"
	if _, e = service.StartImport(ctx, admin, changed, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal(e)
	}
	if _, e = service.StartImport(ctx, portal, input, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("portal imported model", e)
	}
	payload := bytes.Repeat([]byte("x"), int(models.ChunkBytes))
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	if _, e = service.UploadChunk(ctx, admin, value.ID, models.ChunkBytes, models.ChunkBytes, digest, bytes.NewReader(payload)); e == nil || e.Error() != "MODEL_CHUNK_OFFSET_MISMATCH" {
		t.Fatal("upload skipped preceding chunk", e)
	}
	first, e := service.UploadChunk(ctx, admin, value.ID, 0, models.ChunkBytes, digest, bytes.NewReader(payload))
	if e != nil {
		t.Fatal(e)
	}
	replay, e := service.UploadChunk(ctx, admin, value.ID, 0, models.ChunkBytes, digest, bytes.NewReader(payload))
	if e != nil || replay.UploadedBytes != first.UploadedBytes {
		t.Fatal("chunk replay changed byte count", e)
	}
	if _, e = service.QueueImport(ctx, admin, value.ID, "Incomplete synthetic upload", uuid.NewString()); e == nil || e.Error() != "MODEL_IMPORT_INCOMPLETE" {
		t.Fatal("partial model was queued", e)
	}
	if _, e = service.GetImport(ctx, portal, value.ID); e == nil {
		t.Fatal("portal read private upload")
	}
	unknown := filepath.Join(root, "imports", value.ID, "unowned-sentinel")
	if e = os.WriteFile(unknown, []byte("PRESERVE_UNKNOWN_FILE"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = service.CancelImport(ctx, admin, value.ID, "Denied unknown cleanup", uuid.NewString()); e == nil || e.Error() != "MODEL_CLEANUP_CONFLICT" {
		t.Fatal("unknown data was deleted", e)
	}
	if _, e = os.Stat(unknown); e != nil {
		t.Fatal("unknown file lost", e)
	}
	if e = os.Remove(unknown); e != nil {
		t.Fatal(e)
	}
	cancelled, e := service.CancelImport(ctx, admin, value.ID, "Discard exact synthetic upload", uuid.NewString())
	if e != nil || cancelled.State != "cancelled" {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "imports", value.ID)); !os.IsNotExist(e) {
		t.Fatal("owned chunks survived explicit discard")
	}

}
