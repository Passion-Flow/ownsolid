//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"io"
	"testing"
	"time"
)

func TestActualUploadLeaseAndAuditRollbackReconcileOneImmutableSource(t *testing.T) {
	s, _, actor := workerScenario(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workspace := actor.Me.Workspace.ID
	data, e := s.Create(ctx, actor, CreateRequest{WorkspaceID: workspace, Name: "Upload reconciliation", Origin: "Synthetic local test", License: "Synthetic CC0", RightsAsserted: true, Reason: "Exercise upload commit failure"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte(row("a", "first", "one") + row("b", "second", "two"))
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	draft, e := s.CreateDraft(ctx, actor, workspace, data.ID, DraftRequest{SourceName: "recovery.jsonl", ExpectedBytes: int64(len(raw)), SourceSHA256: digest, Reason: "Bounded immutable source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_dataset_source_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='dataset.source_received' THEN RAISE EXCEPTION 'synthetic source audit failure'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_dataset_source_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_dataset_source_audit()`); e != nil {
		t.Fatal(e)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		_, err := s.Upload(ctx, actor, workspace, data.ID, draft.ID, digest, int64(len(raw)), reader, uuid.NewString())
		done <- err
	}()
	for {
		var leased bool
		if e = s.DB.QueryRow(ctx, "SELECT upload_attempt_id IS NOT NULL FROM dataset_drafts WHERE id=$1", draft.ID).Scan(&leased); e != nil {
			t.Fatal(e)
		}
		if leased {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("upload lease not acquired")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, e = s.Upload(ctx, actor, workspace, data.ID, draft.ID, digest, int64(len(raw)), bytes.NewReader(raw), uuid.NewString()); e == nil || e.Error() != "DATASET_UPLOAD_BUSY" {
		t.Fatalf("competing upload: %v", e)
	}
	if _, e = writer.Write(raw); e != nil {
		t.Fatal(e)
	}
	writer.Close()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("source audit failure ignored")
		}
	case <-ctx.Done():
		t.Fatal("upload did not terminate")
	}
	var jobs int
	var received bool
	var object string
	if e = s.DB.QueryRow(ctx, "SELECT source_received_at IS NOT NULL,source_object_id::text,(SELECT count(*) FROM dataset_jobs WHERE draft_id=$1) FROM dataset_drafts WHERE id=$1", draft.ID).Scan(&received, &object, &jobs); e != nil || received || jobs != 0 {
		t.Fatal("source commit was not atomic", e)
	}
	if _, e = s.Store.Source(object, digest, int64(len(raw))); e != nil {
		t.Fatal("uncertain immutable source lost", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_dataset_source_audit ON audit_events;DROP FUNCTION reject_dataset_source_audit();UPDATE dataset_drafts SET upload_expires_at=clock_timestamp()-interval '1 second' WHERE id='"+draft.ID+"'::uuid"); e != nil {
		t.Fatal(e)
	}
	recovered, e := s.Upload(ctx, actor, workspace, data.ID, draft.ID, digest, int64(len(raw)), bytes.NewReader(nil), uuid.NewString())
	if e != nil || recovered.State != "validating" || recovered.CurrentJobID == nil {
		t.Fatal("immutable source reconciliation failed", e)
	}
	replayed, e := s.Upload(ctx, actor, workspace, data.ID, draft.ID, digest, int64(len(raw)), bytes.NewReader(nil), uuid.NewString())
	if e != nil || replayed.CurrentJobID == nil || *replayed.CurrentJobID != *recovered.CurrentJobID {
		t.Fatal("replay duplicated import", e)
	}
	if _, e = s.Upload(ctx, actor, workspace, data.ID, draft.ID, digest, int64(len(raw))+1, bytes.NewReader(raw), uuid.NewString()); e == nil || e.Error() != "DATASET_SOURCE_CHANGED" {
		t.Fatal("changed replay accepted", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM dataset_jobs WHERE draft_id=$1", draft.ID).Scan(&jobs); e != nil || jobs != 1 {
		t.Fatal("duplicate import jobs", e)
	}
	process(t, s)
	accepted, e := s.GetDraft(ctx, actor, workspace, data.ID, draft.ID)
	if e != nil || accepted.State != "editing" || accepted.TotalRows == nil || *accepted.TotalRows != 2 {
		t.Fatal("reconciled actual validation", e)
	}
}
