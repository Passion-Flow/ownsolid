package browser_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM20ModelRecoveryTailBrowser(t *testing.T) {
	testProductionBrowser(t, "models/recovery-tail.mjs", "model-recovery-tail-browser", false, func(ctx context.Context, service *models.Service, output string) {
		verifyModelRecoveryBrowser(t, ctx, service, output, true)
	})
}

// Explicit test precondition: a verified immutable object exists after a prior
// publication interruption, with byte metadata but no retained chunk history.
// The actual production queue/worker must verify it and publish the real model.
func seedModelRecoveryTail(t *testing.T, parent context.Context, s *models.Service, password string, manifest map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	a := fixture.Login(t, s.Identity, "admin@example.invalid", password, "admin")
	archive, e := os.Stat(manifest["model_bundle"].(string))
	if e != nil {
		t.Fatal("approved producer archive required", e)
	}
	v, e := s.StartImport(ctx, a, models.ImportRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Actual model browser <img>", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: archive.Size(), LicenseAcknowledged: true, Reason: "Synthetic verified-object recovery precondition; no browser upload"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual recovery fixture intent", e)
	}
	objects := filepath.Join(s.RootDirectory, "objects")
	destination := filepath.Join(objects, v.ID)
	for _, directory := range []string{objects, destination} {
		if e = os.Mkdir(directory, 0700); e != nil {
			t.Fatal("fresh owned recovery fixture directory", e)
		}
	}
	for _, expected := range wire.ApprovedModel().Files {
		source, err := os.OpenFile(filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY"), expected.Path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			t.Fatal("bound approved file required", err)
		}
		target, err := os.OpenFile(filepath.Join(destination, expected.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		digest := sha256.New()
		count, copied := io.Copy(io.MultiWriter(target, digest), source)
		synced, closedSource, closedTarget := target.Sync(), source.Close(), target.Close()
		if copied != nil || synced != nil || closedSource != nil || closedTarget != nil || count != expected.SizeBytes || hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
			t.Fatal("actual approved recovery file copy/digest/sync failed", expected.Path)
		}
	}
	for _, directory := range []string{destination, objects, s.RootDirectory} {
		d, err := os.Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		err = d.Sync()
		closed := d.Close()
		if err != nil || closed != nil {
			t.Fatal("recovery fixture directory sync", err, closed)
		}
	}
	// This is deliberately fixture metadata, never an API upload success stub.
	if _, e = s.DB.Exec(ctx, `UPDATE model_imports SET uploaded_bytes=expected_bytes WHERE id=$1 AND state='uploading'`, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.QueueImport(ctx, a, v.ID, "Verify actual immutable recovery object", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for {
		var state string
		if e = s.DB.QueryRow(ctx, `SELECT state FROM model_imports WHERE id=$1`, v.ID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if state == "ready" {
			break
		}
		if state == "failed" {
			t.Fatal("actual production worker refused the recovery object")
		}
		select {
		case <-ctx.Done():
			t.Fatal("recovery object publication deadline")
		case <-time.After(50 * time.Millisecond):
		}
	}
	manifest["model_id"], manifest["workspace_id"], manifest["actor_id"] = v.ID, v.WorkspaceID, a.Me.User.ID
	manifest["fixture_mode"] = "verified_immutable_object_recovery_no_chunk_rows"
}
