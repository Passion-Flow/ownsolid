package training_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func chunkIntent(t *testing.T, f scenario) (agentwire.Lease, training.Upload, training.ChunkRequest) {
	t.Helper()
	attachStore(t, f)
	dispatched(t, f)
	l := claimed(t, f)
	var receipt wire.CheckpointReceipt
	if e := json.Unmarshal(checkpointMetadata(t, f, *l.Training), &receipt); e != nil {
		t.Fatal(e)
	}
	for i := range receipt.Files {
		receipt.Files[i].SHA256 = digest([]byte("x"))
	}
	raw, e := json.Marshal(receipt)
	if e != nil {
		t.Fatal(e)
	}
	announce(t, f, l, raw)
	u, e := f.s.BeginUpload(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", raw)
	if e != nil {
		t.Fatal(e)
	}
	return l, u, training.ChunkRequest{Asset: "optimizer.pt", Offset: 0, Length: 1, SHA256: digest([]byte("x"))}
}

// This explicitly synthetic one-byte input exercises streaming admission/accounting, never accepted checkpoint semantics.
type heldChunk struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	sent    bool
}

func (r *heldChunk) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.once.Do(func() { close(r.entered) })
	<-r.release
	r.sent = true
	return copy(p, "x"), nil
}

func TestActualTrainingChunkStreamReleasesBusinessLocksAndRejectsStaleAuthority(t *testing.T) {
	for _, mode := range []string{"renew", "epoch", "cancel", "ticket_expiry"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			l, u, input := chunkIntent(t, f)
			reader := &heldChunk{entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(reader.release) }) })
			done := make(chan error, 1)
			go func() {
				_, e := f.s.UploadChunk(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID, input, reader)
				done <- e
			}()
			select {
			case <-reader.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not enter reader")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, e := f.s.Nodes.Renew(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken); e != nil {
				t.Fatal("stream retained business lock", e)
			}
			if _, e := f.s.UploadChunk(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID, input, strings.NewReader("x")); e == nil || e.Error() != "TRAINING_UPLOAD_BUSY" {
				t.Fatal("parallel upload was not refused", e)
			}
			var e error
			switch mode {
			case "epoch":
				_, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID)
			case "cancel":
				j, err := f.s.Get(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID)
				if err != nil {
					t.Fatal(err)
				}
				_, e = f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Cancel while output bytes remain in flight"}, uuid.NewString())
			case "ticket_expiry":
				_, e = f.s.DB.Exec(ctx, "UPDATE training_uploads SET active_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", u.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			release.Do(func() { close(reader.release) })
			select {
			case e = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stream failed to return")
			}
			if mode == "renew" {
				if e != nil || count(t, f, "SELECT count(*) FROM training_upload_chunks") != 1 {
					t.Fatal("live exact stream not accepted", e)
				}
			} else {
				code := "ACCESS_DENIED"
				if mode == "ticket_expiry" {
					code = "TRAINING_UPLOAD_TICKET_STALE"
				}
				expected(t, e, code)
				if count(t, f, "SELECT count(*) FROM training_upload_chunks") != 0 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.upload_chunk_accepted'") != 0 {
					t.Fatal("stale stream acquired accepted accounting")
				}
			}
			if count(t, f, "SELECT count(*) FROM training_checkpoints") != 0 || count(t, f, "SELECT count(*) FROM training_artifacts") != 0 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state IN ('held','recovery_required')") != 1 {
				t.Fatal("chunk created output or released physical ownership")
			}
		})
	}
}

func TestActualTrainingChunkReplayChecksBodyAuditRollbackAndIntentImmutability(t *testing.T) {
	f := setup(t)
	l, u, input := chunkIntent(t, f)
	ctx := context.Background()
	put := func(body string) (training.ChunkAcceptance, error) {
		return f.s.UploadChunk(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID, input, strings.NewReader(body))
	}
	_, e := put("xx")
	expected(t, e, "TRAINING_UPLOAD_CHECKSUM_MISMATCH")
	if count(t, f, "SELECT count(*) FROM training_upload_chunks") != 0 {
		t.Fatal("oversized bytes accepted")
	}
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_chunk() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.upload_chunk_accepted' THEN RAISE EXCEPTION 'SYNTHETIC_CHUNK_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_chunk BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_chunk()`); e != nil {
		t.Fatal(e)
	}
	if _, e = put("x"); e == nil {
		t.Fatal("audit failure reported chunk accepted")
	}
	if count(t, f, "SELECT count(*) FROM training_upload_chunks") != 0 {
		t.Fatal("chunk accounting survived audit rollback")
	}
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_training_chunk ON audit_events"); e != nil {
		t.Fatal(e)
	}
	first, e := put("x")
	if e != nil || first.Duplicate || first.ReceivedBytes != 1 {
		t.Fatal("private orphan chunk retry not accepted exactly once", e)
	}
	again, e := put("x")
	if e != nil || !again.Duplicate || again.ReceivedBytes != 1 {
		t.Fatal("replay changed byte accounting", e)
	}
	_, e = put("y")
	expected(t, e, "TRAINING_UPLOAD_CHECKSUM_MISMATCH")
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.upload_chunk_accepted'") != 1 {
		t.Fatal("replay duplicated audit")
	}
	_, e = f.s.UploadChunk(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, uuid.NewString(), input, strings.NewReader("x"))
	expected(t, e, "TRAINING_UPLOAD_NOT_FOUND")
	for _, query := range []string{"UPDATE training_uploads SET manifest_bytes=convert_to('{}','UTF8') WHERE id=$1", "UPDATE training_uploads SET manifest_sha256=repeat('b',64) WHERE id=$1", "UPDATE training_upload_chunks SET sha256=repeat('b',64) WHERE upload_id=$1"} {
		if _, e = f.s.DB.Exec(ctx, query, u.ID); e == nil {
			t.Fatal("immutable upload input/accounting could be rewritten")
		}
	}
}
