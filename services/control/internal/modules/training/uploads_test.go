package training_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func attachStore(t *testing.T, f scenario) {
	t.Helper()
	path := f.s.Identity.Config.Training.ArtifactRoot
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := training.InitializeStore(path, f.admin.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	s, e := training.OpenStore(path, f.admin.Me.Workspace.TenantID, 4096, wire.DefaultResources().MaximumOutputBytes, wire.DefaultResources().MaximumOutputFiles)
	if e != nil {
		t.Fatal(e)
	}
	f.s.Store = s
	t.Cleanup(func() { s.Close() })
}
func checkpointMetadata(t *testing.T, f scenario, command wire.Command) []byte {
	t.Helper()
	request, e := command.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	receipt := wire.CheckpointReceipt{SchemaVersion: 1, Bindings: wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository), GlobalStep: 1}
	// These explicit synthetic file descriptors test metadata intent only; no bytes or accepted checkpoint are fabricated.
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth"} {
		receipt.Files = append(receipt.Files, datawire.Asset{Path: name, SizeBytes: 1, SHA256: strings.Repeat("a", 64)})
	}
	raw, e := json.Marshal(receipt)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func announce(t *testing.T, f scenario, l agentwire.Lease, metadata []byte) {
	t.Helper()
	events := prelude(*l.Training)
	events = append(events, eventFixture(*l.Training, 3, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": digest(metadata)}))
	if _, e := f.s.RecordEvents(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, events); e != nil {
		t.Fatal(e)
	}
}

func TestActualTrainingDurableCheckpointMetadataIntentExactReplayAndRevocation(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	metadata := checkpointMetadata(t, f, *l.Training)
	progress := prelude(*l.Training)
	progress = append(progress, eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.0}}))
	if _, e := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, progress); e != nil {
		t.Fatal(e)
	}
	_, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", metadata)
	expected(t, e, "TRAINING_CHECKPOINT_NOT_ANNOUNCED")
	if _, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, eventFixture(*l.Training, 4, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": digest(metadata)})); e != nil {
		t.Fatal(e)
	}
	first, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", metadata)
	if e != nil || first.State != "uploading" || first.ReceivedBytes != 0 || len(first.Files) != 7 || first.ChunkBytes != wire.TransferChunkBytes {
		t.Fatal("actual durable metadata intent", e)
	}
	again, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", metadata)
	if e != nil || first.ID != again.ID {
		t.Fatal("metadata intent replay", e)
	}
	var retained []byte
	if e = f.s.DB.QueryRow(ctx, "SELECT manifest_bytes FROM training_uploads WHERE id=$1", first.ID).Scan(&retained); e != nil || string(retained) != string(metadata) {
		t.Fatal("original receipt bytes/digest lost", e)
	}
	if count(t, f, "SELECT count(*) FROM training_uploads") != 1 || count(t, f, "SELECT count(*) FROM training_checkpoints") != 0 || count(t, f, "SELECT count(*) FROM training_artifacts") != 0 {
		t.Fatal("an intent became an accepted model/checkpoint")
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.upload_started'") != 1 {
		t.Fatal("replay duplicated upload audit")
	}
	if _, e = os.Stat(filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "uploads", first.ID, "upload.json")); e != nil {
		t.Fatal("private metadata stage", e)
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", metadata)
	expected(t, e, "ACCESS_DENIED")
}

func TestActualTrainingCheckpointIntentAuditRollbackAndConflictingStepRefusal(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	metadata := checkpointMetadata(t, f, *l.Training)
	announce(t, f, l, metadata)
	if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_upload() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.upload_started' THEN RAISE EXCEPTION 'SYNTHETIC_TRAINING_UPLOAD_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_upload BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_upload()`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", metadata); e == nil {
		t.Fatal("failed upload audit reported success")
	}
	if count(t, f, "SELECT count(*) FROM training_uploads") != 0 {
		t.Fatal("upload survived audit rollback")
	}
	entries, e := os.ReadDir(filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "uploads"))
	if e != nil || len(entries) != 0 {
		t.Fatal("audit failure created private file effect", e)
	}
	_, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, eventFixture(*l.Training, 4, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": strings.Repeat("b", 64)}))
	expected(t, e, "TRAINING_CHECKPOINT_CONFLICT")
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_training_upload ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", metadata); e != nil {
		t.Fatal("explicit unchanged intent retry", e)
	}
	foreign := *l.Training
	foreign.Scope.WorkspaceID = uuid.NewString()
	request, e := foreign.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	var altered wire.CheckpointReceipt
	json.Unmarshal(metadata, &altered)
	altered.Bindings = wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository)
	raw, _ := json.Marshal(altered)
	_, e = f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", raw)
	expected(t, e, "TRAINING_CHECKPOINT_INVALID")
}
