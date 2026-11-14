//go:build model_integration

package models_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

type importSafetyPatch struct {
	io.ReaderAt
	offset int64
	bytes  []byte
}

func (p importSafetyPatch) ReadAt(data []byte, offset int64) (int, error) {
	n, err := p.ReaderAt.ReadAt(data, offset)
	begin, end := max(offset, p.offset), min(offset+int64(n), p.offset+int64(len(p.bytes)))
	if begin < end {
		copy(data[begin-offset:end-offset], p.bytes[begin-p.offset:end-p.offset])
	}
	return n, err
}

func unsafeImportVariants(t *testing.T, archive *os.File) (importSafetyPatch, importSafetyPatch) {
	t.Helper()
	var executable, pickle importSafetyPatch
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		payload, err := archive.Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		switch header.Name {
		case "model.safetensors":
			pickle = importSafetyPatch{ReaderAt: archive, offset: payload, bytes: []byte{0x80, 0x04, 'N', '.'}}
		case "vocab.json":
			var encoded bytes.Buffer
			writer := tar.NewWriter(&encoded)
			if err = writer.WriteHeader(&tar.Header{
				Name: "modeling.py", Mode: 0755, Size: header.Size, Format: tar.FormatUSTAR,
			}); err != nil {
				t.Fatal(err)
			}
			// The attack changes only one USTAR header, preserving the size and
			// every preceding entry. Existing payload bytes remain inert.
			executable = importSafetyPatch{ReaderAt: archive, offset: payload - int64(encoded.Len()), bytes: encoded.Bytes()}
		}
	}
	if executable.offset <= models.ChunkBytes || len(executable.bytes) != 512 || pickle.offset <= 0 {
		t.Fatal("expected pinned weight header and a late member in the real archive")
	}
	return executable, pickle
}

type fileSafetyFixture struct {
	service *models.Service
	actor   identity.Auth
	ctx     context.Context
	size    int64
	buffer  []byte
}

func (f fileSafetyFixture) start(t *testing.T) models.Import {
	t.Helper()
	input := models.ImportRequest{
		WorkspaceID: f.actor.Me.Workspace.ID, Name: "Offline file review fixture",
		CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: f.size,
		LicenseAcknowledged: true, Reason: "Verify offline model file review",
	}
	v, err := f.service.StartImport(f.ctx, f.actor, input, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f fileSafetyFixture) upload(t *testing.T, id string, source io.ReaderAt, from, to int64) {
	t.Helper()
	for offset := from; offset < to; {
		buf := f.buffer[:min(models.ChunkBytes, f.size-offset)]
		if _, err := source.ReadAt(buf, offset); err != nil {
			t.Fatalf("read upload chunk at %d: %v", offset, err)
		}
		hash := sha256.Sum256(buf)
		v, err := f.service.UploadChunk(f.ctx, f.actor, id, offset, int64(len(buf)), hex.EncodeToString(hash[:]), bytes.NewReader(buf))
		if err != nil || v.UploadedBytes != offset+int64(len(buf)) {
			t.Fatalf("commit chunk at %d: offset=%d err=%v", offset, v.UploadedBytes, err)
		}
		offset = v.UploadedBytes
	}
}

func (f fileSafetyFixture) queue(t *testing.T, id string) error {
	t.Helper()
	_, err := f.service.QueueImport(f.ctx, f.actor, id, "Verify the complete bundle", uuid.NewString())
	return err
}

func (f fileSafetyFixture) assertUnpublished(t *testing.T, id string) {
	t.Helper()
	versions, err := f.service.List(f.ctx, f.actor, f.actor.Me.Workspace.ID)
	if err != nil || len(versions) != 0 {
		t.Fatalf("refused import produced a version: %d, %v", len(versions), err)
	}
	var ready int
	if err = f.service.DB.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='model.import_ready' AND target_id=$1`, id).Scan(&ready); err != nil || ready != 0 {
		t.Fatalf("refused import produced a ready audit: %d, %v", ready, err)
	}
	if _, err = os.Lstat(filepath.Join(f.service.RootDirectory, "objects", id)); !os.IsNotExist(err) {
		t.Fatalf("refused import left a published object: %v", err)
	}
}

func TestActualModelImportFileSafety(t *testing.T) {
	path := os.Getenv("OWNSOLID_TEST_MODEL_FILE_SAFETY_ARCHIVE")
	if path == "" {
		t.Skip("provide the pinned Qwen USTAR archive in OWNSOLID_TEST_MODEL_FILE_SAFETY_ARCHIVE")
	}
	archive, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	info, err := archive.Stat()
	if err != nil {
		t.Fatal(err)
	}
	executable, pickle := unsafeImportVariants(t, archive)
	id := fixture.NewWithSetup(t, fixture.SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30})
	password := fixture.Bootstrap(t, id)
	admin := fixture.Login(t, id, "admin@example.invalid", password, "admin")
	root := t.TempDir()
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	f := fileSafetyFixture{
		service: &models.Service{DB: id.DB, Identity: id, Workspaces: &workspaces.Service{DB: id.DB, Identity: id}, RootDirectory: root},
		ctx:     ctx, actor: admin, size: info.Size(), buffer: make([]byte, models.ChunkBytes),
	}
	for _, tc := range []struct {
		name, code string
		source     io.ReaderAt
	}{
		{name: "late executable member", code: "MODEL_ARCHIVE_INVALID", source: executable},
		{name: "pickle disguised as approved weights", code: "MODEL_SAFETENSORS_INVALID", source: pickle},
	} {
		if !t.Run(tc.name, func(t *testing.T) {
			value := f.start(t)
			f.upload(t, value.ID, tc.source, 0, models.ChunkBytes)
			if e := f.queue(t, value.ID); e == nil || e.Error() != "MODEL_IMPORT_INCOMPLETE" {
				t.Fatalf("partial upload reached verification: %v", e)
			}
			if worked, e := f.service.ProcessNext(ctx); e != nil || worked {
				t.Fatalf("worker touched a partial upload: %v %v", worked, e)
			}
			f.assertUnpublished(t, value.ID)
			f.upload(t, value.ID, tc.source, models.ChunkBytes, f.size)
			t.Logf("Committed %d bytes through the resumable upload", f.size)
			for attempt := 1; attempt <= 2; attempt++ {
				if e := f.queue(t, value.ID); e != nil {
					t.Fatal(e)
				}
				worked, e := f.service.ProcessNext(ctx)
				if !worked || e == nil || e.Error() != tc.code {
					t.Fatalf("verification attempt %d: worked=%v err=%v", attempt, worked, e)
				}
				current, e := f.service.GetImport(ctx, f.actor, value.ID)
				if e != nil || current.State != "failed" || current.ErrorCode == nil ||
					*current.ErrorCode != tc.code || current.Attempts != attempt || current.ModelID != nil {
					t.Fatalf("durable refusal: %+v %v", current, e)
				}
				f.assertUnpublished(t, value.ID)
				scope := filepath.Join(root, "imports", value.ID)
				stages, e := filepath.Glob(filepath.Join(scope, "stage-*"))
				if e != nil || len(stages) != attempt {
					t.Fatalf("attempt stages: %d %v", len(stages), e)
				}
				for _, stage := range stages {
					entries, e := os.ReadDir(stage)
					if e != nil || len(entries) != 0 {
						t.Fatalf("file review staged a payload before rejecting it: %d %v", len(entries), e)
					}
				}
			}
			cancelled, e := f.service.CancelImport(ctx, f.actor, value.ID, "Discard the refused bundle", uuid.NewString())
			if e != nil || cancelled.State != "cancelled" {
				t.Fatalf("unable to replace the refused upload: %+v %v", cancelled, e)
			}
			f.assertUnpublished(t, value.ID)
			t.Logf("Both verification attempts refused %s without staging a payload", tc.code)
		}) {
			return
		}
	}

	t.Run("real approved Qwen bundle", func(t *testing.T) {
		value := f.start(t)
		f.upload(t, value.ID, archive, 0, f.size)
		t.Logf("Committed %d bytes from the unchanged approved bundle", f.size)
		if e := f.queue(t, value.ID); e != nil {
			t.Fatal(e)
		}
		if worked, e := f.service.ProcessNext(ctx); e != nil || !worked {
			t.Fatalf("approved bundle did not publish: %v %v", worked, e)
		}
		current, e := f.service.GetImport(ctx, f.actor, value.ID)
		if e != nil || current.State != "ready" || current.ErrorCode != nil || current.ModelID == nil || *current.ModelID != value.ID {
			t.Fatalf("approved import state: %+v %v", current, e)
		}
		versions, e := f.service.List(ctx, f.actor, f.actor.Me.Workspace.ID)
		if e != nil || len(versions) != 1 || versions[0].ID != value.ID || versions[0].CatalogDigest != wire.ModelCatalogDigest() {
			t.Fatalf("approved inventory mismatch: %+v %v", versions, e)
		}
		if e = modelartifact.VerifyDirectory(ctx, filepath.Join(root, "objects", value.ID)); e != nil {
			t.Fatal(e)
		}
		var ready int
		if e = f.service.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='model.import_ready' AND target_id=$1`, value.ID).Scan(&ready); e != nil || ready != 1 {
			t.Fatalf("approved publication audit: %d %v", ready, e)
		}
	})
}
