//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Emit the live path/filesystem instead of inferring runtime storage from a
// compiler setting. The declared engine runner checks the required filesystem.
func parallelPreparationRuntimeStorage(t *testing.T, path string) {
	t.Helper()
	var observed syscall.Statfs_t
	if e := syscall.Statfs(path, &observed); e != nil {
		t.Fatal(e)
	}
	actual, e := filepath.EvalSymlinks(path)
	if e != nil {
		t.Fatal(e)
	}
	temporary, e := filepath.EvalSymlinks(os.Getenv("TMPDIR"))
	if e != nil {
		t.Fatal("PREPARATION_RUNTIME_PRIVATE_TMP_REQUIRED", e)
	}
	_, inherited := os.LookupEnv("GOTMPDIR")
	receipt, e := json.Marshal(map[string]any{"path": actual, "temporary_root": temporary, "filesystem_type": fmt.Sprintf("%x", observed.Type), "gotmpdir_present": inherited, "gotmpdir": os.Getenv("GOTMPDIR")})
	if e != nil {
		t.Fatal(e)
	}
	t.Log("PREPARATION_RUNTIME_STORAGE " + string(receipt))
	if inherited {
		t.Fatal("PREPARATION_RUNTIME_COMPILER_TMP_INHERITED")
	}
	relative, e := filepath.Rel(temporary, actual)
	if e != nil || !filepath.IsAbs(temporary) || !filepath.IsLocal(relative) || relative == "." {
		t.Fatal("PREPARATION_RUNTIME_PRIVATE_TMP_REQUIRED")
	}
}

func TestParallelMySQLPreparationRuntimeStorage(t *testing.T) {
	parallelPreparationRuntimeStorage(t, t.TempDir())
}

func parallelPreparationUnknown(g reviewStory, at int) *Service {
	identity := *g.s.Identity
	identity.Storage = &reviewUnknownStore{Store: g.f.Store, failAt: at}
	identity.DB = nil
	workspace := *g.s.Workspaces
	workspace.Identity, workspace.DB = &identity, nil
	s := *g.s
	s.Identity, s.Workspaces, s.DB = &identity, &workspace, nil
	return &s
}

// Seed an explicit synthetic durable upload intent, then exercise the real
// selected SQL, lease, filesystem stream and accounting implementation. The
// tiny target bytes are not a prepared model or an independently accepted report.
func parallelPreparationUpload(t *testing.T) (reviewStory, string, string, UploadDescriptor, []byte, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "preparation-store")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	parallelPreparationRuntimeStorage(t, root)
	g, credential, token := parallelPreparationRuntime(t, true)
	if e := InitializeStore(root, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := OpenStore(root, g.f.Tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	g.s.Store = store
	body := bytes.Repeat([]byte("synthetic private chunk only\n"), 8)
	metadata := []byte(`{"synthetic_private_chunk_fixture":true}`)
	d := UploadDescriptor{SchemaVersion: 1, Scope: g.c.Scope, UploadID: uuid.NewString(), AttemptID: g.attempt, ManifestSHA256: hash(metadata), Files: wire.ApprovedBaseFiles()}
	for i := range d.Files {
		if d.Files[i].Path == "model.safetensors" {
			d.Files[i].SizeBytes, d.Files[i].SHA256 = int64(len(body)), hash(body)
		}
	}
	if e := d.Validate(g.c.Recipe.Resources.MaximumOutputBytes, g.c.Recipe.Resources.MaximumOutputFiles); e != nil {
		t.Fatal(e)
	}
	g.seed(t, "model_preparation_uploads", map[string]any{"id": d.UploadID, "preparation_id": g.id, "attempt_id": g.attempt, "manifest_sha256": d.ManifestSHA256, "manifest": reviewJSON(metadata), "manifest_bytes": metadata, "descriptor": reviewJSON(reviewRaw(t, d))})
	if e := store.PrepareUpload(g.ctx, d, metadata); e != nil {
		t.Fatal(e)
	}
	return g, credential, token, d, body, root
}

func parallelPreparationChunk(t *testing.T, g reviewStory, d UploadDescriptor, body []byte, rows int, active bool, audits int) {
	t.Helper()
	var count, auditCount int
	var received int64
	var ticket bool
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*),COALESCE(sum(size_bytes),0) FROM model_preparation_upload_chunks WHERE upload_id=$1`, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM model_preparation_upload_chunks WHERE upload_id=?`, d.UploadID)).Scan(&count, &received); e != nil || count != rows || received != int64(rows*len(body)) {
		t.Fatal("durable chunk accounting", count, received, e)
	}
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT active_chunk_id IS NOT NULL FROM model_preparation_uploads WHERE id=$1 AND state='uploading'`, `SELECT active_chunk_id IS NOT NULL FROM model_preparation_uploads WHERE id=? AND state='uploading'`, d.UploadID)).Scan(&ticket); e != nil || ticket != active {
		t.Fatal("durable ticket", ticket, active, e)
	}
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='model.preparation_chunk_accepted'`, `SELECT count(*) FROM audit_events WHERE target_id=? AND action='model.preparation_chunk_accepted'`, g.id)).Scan(&auditCount); e != nil || auditCount != audits {
		t.Fatal("atomic chunk audit", auditCount, audits, e)
	}
	if rows != 0 {
		if e := g.s.Store.VerifyChunk(g.ctx, d, ChunkReceipt{Asset: "model.safetensors", SizeBytes: int64(len(body)), SHA256: hash(body)}); e != nil {
			t.Fatal("real retained private chunk", e)
		}
	}
}

type parallelPreparationStream struct {
	reader io.Reader
	before func()
	once   sync.Once
	reads  int
}

func (r *parallelPreparationStream) Read(p []byte) (int, error) {
	r.reads++
	r.once.Do(r.before)
	return r.reader.Read(p)
}

func TestParallelMySQLPreparationUploadTransactions(t *testing.T) {
	t.Run("real_chunk_replay_and_incomplete_output_refusal", func(t *testing.T) {
		g, credential, token, d, body, root := parallelPreparationUpload(t)
		input := ChunkRequest{Asset: "model.safetensors", Length: int64(len(body)), SHA256: hash(body)}
		first, e := g.s.UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, bytes.NewReader(body))
		if e != nil || first.Duplicate || first.ReceivedBytes != int64(len(body)) {
			t.Fatal("real selected upload", e)
		}
		if e := g.s.Store.Close(); e != nil {
			t.Fatal(e)
		}
		reopened, e := OpenStore(root, g.f.Tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
		if e != nil {
			t.Fatal("private store reopen", e)
		}
		g.s.Store = reopened
		t.Cleanup(func() {
			if e := reopened.Close(); e != nil {
				t.Error(e)
			}
		})
		parallelPreparationRuntimeStorage(t, root)
		again, e := g.s.UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, bytes.NewReader(body))
		if e != nil || !again.Duplicate || again.ReceivedBytes != first.ReceivedBytes {
			t.Fatal("exact replay", e)
		}
		wrong := bytes.Repeat([]byte{'x'}, len(body))
		refused, e := g.s.UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, bytes.NewReader(wrong))
		if e == nil || !reflect.ValueOf(refused).IsZero() {
			t.Fatal("changed body disclosed acceptance")
		}
		parallelPreparationChunk(t, g, d, body, 1, false, 1)
		output, e := g.s.FinishUpload(g.ctx, g.node, credential, g.task, token, d.UploadID)
		runtimeError(t, e, "MODEL_PREPARATION_UPLOAD_INCOMPLETE")
		if !reflect.ValueOf(output).IsZero() {
			t.Fatal("incomplete output disclosed acceptance")
		}
		var artifacts int
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM model_preparation_artifacts WHERE preparation_id=$1`, `SELECT count(*) FROM model_preparation_artifacts WHERE preparation_id=?`, g.id)).Scan(&artifacts); e != nil || artifacts != 0 {
			t.Fatal("incomplete upload manufactured artifact", artifacts, e)
		}
		parallelPreparationState(t, g, "preparing", "held", false, 0)
	})
	t.Run("unknown_ticket_commit_does_not_read_body", func(t *testing.T) {
		g, credential, token, d, body, root := parallelPreparationUpload(t)
		input := ChunkRequest{Asset: "model.safetensors", Length: int64(len(body)), SHA256: hash(body)}
		reader := &parallelPreparationStream{reader: bytes.NewReader(body), before: func() {}}
		out, e := parallelPreparationUnknown(g, 1).UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, reader)
		runtimeError(t, e, string(transaction.CommitUnknown))
		if !reflect.ValueOf(out).IsZero() || reader.reads != 0 {
			t.Fatal("unknown ticket projected output or consumed body")
		}
		parallelPreparationChunk(t, g, d, body, 0, true, 0)
		if _, e := os.Stat(filepath.Join(root, "uploads", d.UploadID, "chunks", "model.safetensors", chunkName(0))); !os.IsNotExist(e) {
			t.Fatal("unknown intent performed chunk effect", e)
		}
		out, e = g.s.UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, bytes.NewReader(body))
		runtimeError(t, e, "MODEL_PREPARATION_UPLOAD_BUSY")
		if !reflect.ValueOf(out).IsZero() {
			t.Fatal("live uncertain ticket implicitly retried")
		}
	})
	t.Run("unknown_chunk_commit_requires_exact_replay", func(t *testing.T) {
		g, credential, token, d, body, _ := parallelPreparationUpload(t)
		input := ChunkRequest{Asset: "model.safetensors", Length: int64(len(body)), SHA256: hash(body)}
		out, e := parallelPreparationUnknown(g, 2).UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, bytes.NewReader(body))
		runtimeError(t, e, string(transaction.CommitUnknown))
		if !reflect.ValueOf(out).IsZero() {
			t.Fatal("unknown accounting commit disclosed acceptance")
		}
		parallelPreparationChunk(t, g, d, body, 1, false, 1)
		out, e = g.s.UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, bytes.NewReader(body))
		if e != nil || !out.Duplicate || out.ReceivedBytes != int64(len(body)) {
			t.Fatal("explicit exact chunk readback/replay", e)
		}
		parallelPreparationChunk(t, g, d, body, 1, false, 1)
	})
	t.Run("actor_revocation_during_stream_does_not_account_bytes", func(t *testing.T) {
		g, credential, token, d, body, _ := parallelPreparationUpload(t)
		input := ChunkRequest{Asset: "model.safetensors", Length: int64(len(body)), SHA256: hash(body)}
		reader := &parallelPreparationStream{reader: bytes.NewReader(body), before: func() {
			g.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, g.f.User)
		}}
		out, e := g.s.UploadChunk(g.ctx, g.node, credential, g.task, token, d.UploadID, input, reader)
		runtimeError(t, e, "ACCESS_DENIED")
		if !reflect.ValueOf(out).IsZero() || reader.reads == 0 {
			t.Fatal("revoked stream projected acceptance")
		}
		parallelPreparationChunk(t, g, d, body, 0, true, 0)
		if e := g.s.Store.VerifyChunk(g.ctx, d, ChunkReceipt{Asset: input.Asset, SizeBytes: input.Length, SHA256: input.SHA256}); e != nil {
			t.Fatal("private orphan chunk must remain recoverable", e)
		}
		parallelPreparationState(t, g, "preparing", "held", false, 0)
	})
}

func TestParallelMySQLPreparationAtomicRecovery(t *testing.T) {
	t.Run("reconcile_audit_failure_rolls_back_receipt_and_gpu", func(t *testing.T) {
		g, _, _ := parallelPreparationRuntime(t, false)
		g.exec(t, `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=?`, g.node)
		if g.f.Store.Dialect() == transaction.MySQL {
			g.exec(t, "", `CREATE TRIGGER reject_preparation_reconcile_audit BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='model.preparation_undelivered_stopped' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_RECONCILE_AUDIT_FAILURE'; END IF; END`)
		} else {
			g.exec(t, `CREATE FUNCTION reject_preparation_reconcile_audit_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_undelivered_stopped' THEN RAISE EXCEPTION 'SYNTHETIC_RECONCILE_AUDIT_FAILURE'; END IF; RETURN NEW; END;$$`, "")
			g.exec(t, `CREATE TRIGGER reject_preparation_reconcile_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_reconcile_audit_fn()`, "")
		}
		worked, e := g.s.ReconcileNext(g.ctx)
		if e == nil || worked {
			t.Fatal("failed audit reported success")
		}
		parallelPreparationState(t, g, "preparing", "held", false, 0)
		if g.f.Store.Dialect() == transaction.MySQL {
			g.exec(t, "", `DROP TRIGGER reject_preparation_reconcile_audit`)
		} else {
			g.exec(t, `DROP TRIGGER reject_preparation_reconcile_audit ON audit_events`, "")
			g.exec(t, `DROP FUNCTION reject_preparation_reconcile_audit_fn()`, "")
		}
		worked, e = g.s.ReconcileNext(g.ctx)
		if e != nil || !worked {
			t.Fatal("fresh reconciliation after rollback", worked, e)
		}
		parallelPreparationState(t, g, "failed", "released", true, 1)
	})
	t.Run("event_unknown_commit_returns_empty_and_replays_once", func(t *testing.T) {
		g, credential, token := parallelPreparationRuntime(t, true)
		events := runtimeEvents(g.id, g.attempt, 3)[:1]
		out, e := parallelPreparationUnknown(g, 1).RecordEvents(g.ctx, g.node, credential, g.task, token, events)
		runtimeError(t, e, string(transaction.CommitUnknown))
		if len(out) != 0 {
			t.Fatal("unknown event commit disclosed acceptance")
		}
		out, e = g.s.RecordEvents(g.ctx, g.node, credential, g.task, token, events)
		if e != nil || len(out) != 1 || !out[0].Duplicate {
			t.Fatal("exact durable event replay", e)
		}
		var rows, audits int
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM model_preparation_events WHERE attempt_id=$1`, `SELECT count(*) FROM model_preparation_events WHERE attempt_id=?`, g.attempt)).Scan(&rows); e != nil || rows != 1 {
			t.Fatal("duplicate event row", rows, e)
		}
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='model.preparation_event_recorded'`, `SELECT count(*) FROM audit_events WHERE target_id=? AND action='model.preparation_event_recorded'`, g.id)).Scan(&audits); e != nil || audits != 1 {
			t.Fatal("duplicate event audit", audits, e)
		}
	})
}
