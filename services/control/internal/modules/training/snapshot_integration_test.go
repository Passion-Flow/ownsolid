//go:build training_artifact_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/teststorage"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func snapshotTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	teststorage.Observe(t, "snapshot", directory)
	return directory
}

func acceptedSnapshotCheckpoint(t *testing.T) (scenario, string, string) {
	t.Helper()
	f, l, u, files, checkpoint := actualCheckpointIntent(t)
	ctx := context.Background()
	uploadActualFiles(t, f, l, u, files)
	if _, e := f.s.FinishUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID); e != nil {
		t.Fatal(e)
	}
	// Adapt only the original CPU result's bindings to this synthetic node scope; physical tensors remain unchanged.
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	raw, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]json.RawMessage
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	set := func(key string, value any) {
		b, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		result[key] = b
	}
	request, e := l.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	set("bindings", wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository))
	set("job_id", request.JobID)
	set("attempt_id", request.AttemptID)
	set("dataset_version_id", request.DatasetVersionID)
	set("dataset_manifest_sha256", request.DatasetManifestSHA256)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", wire.ProductionProfile)
	set("event_count", 3)
	var train, validation int64
	if e = f.s.DB.QueryRow(ctx, "SELECT (manifest->>'train_rows')::bigint,(manifest->>'validation_rows')::bigint FROM dataset_versions WHERE id=$1", f.input.DatasetVersionID).Scan(&train, &validation); e != nil {
		t.Fatal(e)
	}
	set("train_rows", train)
	set("validation_rows", validation)
	set("checkpoints", []wire.Checkpoint{{GlobalStep: 1, Path: "checkpoint-1", ReceiptSHA256: digest(checkpoint)}})
	var assets []datawire.Asset
	if e = json.Unmarshal(result["files"], &assets); e != nil {
		t.Fatal(e)
	}
	for i := range assets {
		if assets[i].Path == "checkpoints/checkpoint-1/projectone-checkpoint.json" {
			assets[i].SizeBytes = int64(len(checkpoint))
			assets[i].SHA256 = digest(checkpoint)
		}
	}
	set("files", assets)
	raw, e = json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	artifact, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "artifact", raw)
	if e != nil {
		t.Fatal(e)
	}
	adapter := map[string][]byte{}
	for _, asset := range artifact.Files {
		adapter[asset.Path], e = os.ReadFile(filepath.Join(output, "adapter", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
	}
	uploadActualFiles(t, f, l, artifact, adapter)
	if _, e = f.s.FinishUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, artifact.ID); e != nil {
		t.Fatal(e)
	}
	// Physical stop is an explicitly synthetic report; all checkpoint bytes were produced by the actual approved CPU worker.
	receipt := stopFixture(t, f, l)
	receipt.Proof = "owned_container_removed"
	container := strings.Repeat("d", 64)
	receipt.ContainerID = &container
	outcomeSHA := digest(raw)
	receipt.OutcomeSHA256 = &outcomeSHA
	if _, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, receipt, nil); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(snapshotTestDirectory(t), "snapshot")
	if e := f.s.ExportFiles(ctx, backup); e != nil {
		t.Fatal("actual accepted CPU snapshot export", e)
	}
	return f, backup, filepath.Join("checkpoints", l.Training.Scope.JobID, l.Training.AttemptID, u.ID)
}
func TestActualCpuTrainingSnapshotRoundTripPreservesPrivateCheckpointAndNamespace(t *testing.T) {
	f, backup, key := acceptedSnapshotCheckpoint(t)
	ctx := context.Background()
	before, e := os.ReadFile(filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "namespace.json"))
	if e != nil {
		t.Fatal(e)
	}
	destination := snapshotTestDirectory(t)
	copyService := *f.s
	copyService.Store = nil
	if e = copyService.ImportFiles(ctx, backup, destination); e != nil {
		t.Fatal("actual snapshot import", e)
	}
	marker, e := os.ReadFile(filepath.Join(destination, "namespace.json"))
	if e != nil || !bytes.Equal(marker, before) {
		t.Fatal("accepted namespace changed", e)
	}
	store, e := training.OpenStore(destination, f.admin.Me.Workspace.TenantID, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	copyService.Store = store
	if e = copyService.VerifyStored(ctx); e != nil {
		t.Fatal("imported immutable references not ready", e)
	}
	var adapterKey string
	if e = f.s.DB.QueryRow(ctx, "SELECT storage_key FROM training_artifacts").Scan(&adapterKey); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "README.md", "publication.json"} {
		original, e := os.ReadFile(filepath.Join(backup, adapterKey, name))
		if e != nil {
			t.Fatal(e)
		}
		restored, e := os.ReadFile(filepath.Join(destination, adapterKey, name))
		if e != nil || !bytes.Equal(original, restored) {
			t.Fatal("safe adapter publication not restored", e)
		}
	}
	for _, name := range []string{"optimizer.pt", "rng_state.pth", "adapter_model.safetensors", "projectone-checkpoint.json", "publication.json"} {
		source, e := os.ReadFile(filepath.Join(backup, key, name))
		if e != nil {
			t.Fatal(e)
		}
		restored, e := os.ReadFile(filepath.Join(destination, key, name))
		if e != nil || !bytes.Equal(source, restored) {
			t.Fatal("accepted checkpoint bytes changed", name, e)
		}
		info, e := os.Stat(filepath.Join(destination, key, name))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private backup restored public checkpoint", e)
		}
	}
	entries, e := os.ReadDir(filepath.Join(destination, "uploads"))
	if e != nil || len(entries) != 0 {
		t.Fatal("unfinished upload stage was adopted", e)
	}
	if e = f.s.AuditStoreOperation(ctx, "export", "Verified isolated native checkpoint snapshot export"); e != nil {
		t.Fatal(e)
	}
	if e = copyService.AuditStoreOperation(ctx, "import", "Verified isolated native checkpoint snapshot import"); e != nil {
		t.Fatal(e)
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE audience='operator' AND action IN ('training.store_export_verified','training.store_import_verified')") != 2 {
		t.Fatal("actual snapshot operator actions not audited")
	}
}
func cloneSnapshotFixture(t *testing.T, source string) string {
	t.Helper()
	destination := filepath.Join(snapshotTestDirectory(t), "copy")
	if e := os.Mkdir(destination, 0700); e != nil {
		t.Fatal(e)
	}
	e := filepath.WalkDir(source, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		body, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, body, 0600)
	})
	if e != nil {
		t.Fatal(e)
	}
	return destination
}
func TestActualCpuTrainingSnapshotFaultsFailBeforeDestinationMutation(t *testing.T) {
	f, backup, key := acceptedSnapshotCheckpoint(t)
	for _, mode := range []string{"bytes", "missing_manifest", "extra_file", "symlink", "hardlink", "foreign_tenant", "manifest_null", "manifest_duplicate", "nonempty_destination", "byte_limit"} {
		t.Run(mode, func(t *testing.T) {
			source := cloneSnapshotFixture(t, backup)
			destination := snapshotTestDirectory(t)
			restorer := *f.s
			restorer.Store = nil
			manifest := filepath.Join(source, "snapshot.json")
			var sentinel string
			switch mode {
			case "bytes":
				file := filepath.Join(source, key, "optimizer.pt")
				body, e := os.ReadFile(file)
				if e != nil {
					t.Fatal(e)
				}
				body[0] ^= 1
				if e = os.WriteFile(file, body, 0600); e != nil {
					t.Fatal(e)
				}
			case "missing_manifest":
				if e := os.Remove(manifest); e != nil {
					t.Fatal(e)
				}
			case "extra_file":
				sentinel = filepath.Join(source, "preserve-unknown")
				os.WriteFile(sentinel, []byte("PRESERVE_UNKNOWN"), 0600)
			case "symlink":
				file := filepath.Join(source, key, "optimizer.pt")
				if e := os.Rename(file, file+".retained"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(file+".retained", file); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(filepath.Join(source, key, "optimizer.pt"), filepath.Join(source, "retained-link")); e != nil {
					t.Fatal(e)
				}
			case "foreign_tenant":
				body, _ := os.ReadFile(manifest)
				var v map[string]any
				json.Unmarshal(body, &v)
				v["references"].(map[string]any)["tenant_id"] = uuid.NewString()
				body, _ = json.Marshal(v)
				os.WriteFile(manifest, body, 0600)
			case "manifest_null":
				os.WriteFile(manifest, []byte("null"), 0600)
			case "manifest_duplicate":
				body, _ := os.ReadFile(manifest)
				body = append([]byte(`{"schema_version":1,`), body[1:]...)
				os.WriteFile(manifest, body, 0600)
			case "nonempty_destination":
				sentinel = filepath.Join(destination, "preserve-unknown")
				os.WriteFile(sentinel, []byte("PRESERVE_UNKNOWN"), 0600)
			case "byte_limit":
				identityCopy := *f.s.Identity
				policy := identityCopy.Config.Policy()
				policy.Training.SnapshotMaximumBytes = 1048576
				identityCopy.Config.Runtime = &policy
				restorer.Identity = &identityCopy
			}
			if e := restorer.ImportFiles(context.Background(), source, destination); e == nil {
				t.Fatal("unsafe/incomplete/foreign snapshot reported success")
			}
			entries, e := os.ReadDir(destination)
			if e != nil {
				t.Fatal(e)
			}
			expectedCount := 0
			if mode == "nonempty_destination" {
				expectedCount = 1
			}
			if len(entries) != expectedCount {
				t.Fatal("failed validation mutated the destination")
			}
			if sentinel != "" {
				body, e := os.ReadFile(sentinel)
				if e != nil || string(body) != "PRESERVE_UNKNOWN" {
					t.Fatal("unknown retained data changed", e)
				}
			}
		})
	}
}
func TestActualCpuTrainingSnapshotAuditFailureCancellationAndReferenceMismatchRetainData(t *testing.T) {
	f, backup, _ := acceptedSnapshotCheckpoint(t)
	ctx := context.Background()
	if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_snapshot_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.store_export_verified' THEN RAISE EXCEPTION 'SYNTHETIC_SNAPSHOT_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_snapshot_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_snapshot_audit()`); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(backup, "snapshot.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.AuditStoreOperation(ctx, "export", "Synthetic audit failure must retain the complete snapshot"); e == nil {
		t.Fatal("failed snapshot audit reported success")
	}
	after, e := os.ReadFile(filepath.Join(backup, "snapshot.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("failed audit changed retained snapshot", e)
	}
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_snapshot_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if e = f.s.AuditStoreOperation(ctx, "export", "Explicit verified retry of retained immutable snapshot"); e != nil {
		t.Fatal(e)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	newBackup := filepath.Join(snapshotTestDirectory(t), "cancelled")
	if e = f.s.ExportFiles(cancelled, newBackup); e == nil {
		t.Fatal("cancelled export succeeded")
	}
	if _, e = os.Stat(newBackup); !os.IsNotExist(e) {
		t.Fatal("already cancelled export created backup")
	}
	other := setup(t)
	destination := snapshotTestDirectory(t)
	if e = other.s.ImportFiles(ctx, backup, destination); e == nil {
		t.Fatal("foreign installation imported training references")
	}
	entries, e := os.ReadDir(destination)
	if e != nil || len(entries) != 0 {
		t.Fatal("foreign import mutated destination", e)
	}
}
