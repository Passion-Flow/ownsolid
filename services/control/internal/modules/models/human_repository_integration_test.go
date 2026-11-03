//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type humanImportFixture struct {
	modelReadFixture
	input     models.ImportRequest
	key, root string
}

func humanSetup(t *testing.T) humanImportFixture {
	t.Helper()
	x := humanImportFixture{modelReadFixture: modelReadSetup(t), root: t.TempDir(), key: uuid.NewString()}
	if e := os.Chmod(x.root, 0700); e != nil {
		t.Fatal(e)
	}
	x.s.RootDirectory = x.root
	x.exec(t, `UPDATE model_versions SET state='deleted',deleted_at=clock_timestamp() WHERE id=$1`, `UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE id=?`, x.model)
	x.input = models.ImportRequest{WorkspaceID: x.f.Workspace, Name: "Real paired intent \u4e16\u754c", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: modelartifact.MinimumArchiveBytes(), LicenseAcknowledged: true, Reason: "Isolated synthetic human import acceptance"}
	return x
}

func humanRefused(t *testing.T, v models.Import, e error, code string) {
	t.Helper()
	modelReadError(t, e, code)
	if v.ID != "" || v.State != "" || v.ErrorCode != nil {
		t.Fatal("failed human mutation published DTO")
	}
}
func (x humanImportFixture) start(t *testing.T) models.Import {
	t.Helper()
	v, e := x.s.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
	if e != nil || v.ID == "" || v.State != "uploading" {
		t.Fatal("actual paired intent", e)
	}
	return v
}
func (x humanImportFixture) resolve(t *testing.T, id string) {
	t.Helper()
	r, e := x.s.ResolveImport(x.ctx, x.admin, x.f.Workspace, x.key)
	if e != nil || r.Status != models.ImportResolutionFound || r.Import == nil || r.Import.ID != id {
		t.Fatal("actual durable intent resolution", e)
	}
}
func (x humanImportFixture) count(t *testing.T, action, id string) int {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE action=$1 AND target_id=$2`, `SELECT COUNT(*) FROM audit_events WHERE action=? AND target_id=?`, action, id)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// This explicitly identified low-byte SQL fixture exercises repository/chunk/
// queue mechanics. It is not an approved archive or a successful public import.
func (x humanImportFixture) small(t *testing.T) models.Import {
	t.Helper()
	id := x.imports[0]
	x.exec(t, `UPDATE model_imports SET state='uploading',expected_bytes=100,uploaded_bytes=0,attempts=0,error_code=NULL WHERE id=$1`, `UPDATE model_imports SET state='uploading',expected_bytes=100,uploaded_bytes=0,attempts=0,error_code=NULL WHERE id=?`, id)
	v, e := x.s.GetImport(x.ctx, x.admin, id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func humanPayload() ([]byte, string) {
	b := bytes.Repeat([]byte("x"), 100)
	h := sha256.Sum256(b)
	return b, hex.EncodeToString(h[:])
}
func (x humanImportFixture) upload(t *testing.T, id string) models.Import {
	t.Helper()
	b, h := humanPayload()
	v, e := x.s.UploadChunk(x.ctx, x.admin, id, 0, int64(len(b)), h, bytes.NewReader(b))
	if e != nil || v.UploadedBytes != 100 {
		t.Fatal("actual bounded chunk", e)
	}
	return v
}
func (x humanImportFixture) chunkCount(t *testing.T, id string, expected int) {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*) FROM model_import_chunks WHERE import_id=$1`, `SELECT COUNT(*) FROM model_import_chunks WHERE import_id=?`, id)).Scan(&n); e != nil || n != expected {
		t.Fatal("durable chunk metadata count", n, e)
	}
}

// Explicit scheduling/outcome double around real transactions. It does not
// emulate a database or establish real COMMIT wire-loss qualification.
type humanTransactionStore struct {
	transaction.Store
	calls            atomic.Int32
	unknown, gate    int32
	entered, release chan struct{}
}

func (s *humanTransactionStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	n := s.calls.Add(1)
	if n == s.gate {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if e := s.Store.Within(ctx, ro, fn); e != nil {
		return e
	}
	if n == s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

func TestActualHumanModelImports(t *testing.T) {
	for _, name := range []string{"intent_replay_resolution", "parallel_intents", "changed_fingerprint", "scope_isolation", "portal_refusal", "revoked_session", "actor_recheck_after_read", "chunk_replay_queue", "invalid_chunks", "chunk_tamper", "file_survives_SQL_rollback", "queue_audit_rollback", "intent_audit_rollback", "file_lock_busy", "pending_admission", "cancelled_context", "expired_deadline", "unknown_intent", "unknown_upload", "unknown_queue", "unknown_resolution", "policy_decrease_replay"} {
		t.Run(name, func(t *testing.T) {
			x := humanSetup(t)
			switch name {
			case "intent_replay_resolution":
				r, e := x.s.ResolveImport(x.ctx, x.admin, x.f.Workspace, x.key)
				if e != nil || r.Status != models.ImportResolutionAbsent || r.Import != nil {
					t.Fatal("explicit absent intent", e)
				}
				v := x.start(t)
				again := x.start(t)
				if again.ID != v.ID {
					t.Fatal("intent replay duplicated record")
				}
				x.resolve(t, v.ID)
				if x.count(t, "model.import_started", v.ID) != 1 {
					t.Fatal("duplicate intent audit")
				}
				payload := bytes.Repeat([]byte("x"), int(models.ChunkBytes))
				hash := sha256.Sum256(payload)
				digest := hex.EncodeToString(hash[:])
				got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, models.ChunkBytes, models.ChunkBytes, digest, bytes.NewReader(payload))
				humanRefused(t, got, e, "MODEL_CHUNK_OFFSET_MISMATCH")
				got, e = x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, models.ChunkBytes, digest, bytes.NewReader(payload))
				if e != nil || got.UploadedBytes != models.ChunkBytes {
					t.Fatal("real intent first bounded chunk", e)
				}
				got, e = x.s.QueueImport(x.ctx, x.admin, v.ID, "Refuse partially uploaded public intent", uuid.NewString())
				humanRefused(t, got, e, "MODEL_IMPORT_INCOMPLETE")
				other, e := x.s.StartImport(x.ctx, x.admin, x.input, uuid.NewString(), uuid.NewString())
				humanRefused(t, other, e, "MODEL_IMPORT_BUSY")
			case "parallel_intents":
				type result struct {
					v models.Import
					e error
				}
				done := make(chan result, 2)
				start := make(chan struct{})
				for range 2 {
					go func() {
						<-start
						v, e := x.s.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
						done <- result{v, e}
					}()
				}
				close(start)
				var id string
				for range 2 {
					select {
					case r := <-done:
						if r.e != nil || r.v.ID == "" {
							t.Fatal("actual concurrent intent", r.e)
						}
						if id != "" && id != r.v.ID {
							t.Fatal("parallel replay split identity")
						}
						id = r.v.ID
					case <-x.ctx.Done():
						t.Fatal("parallel intent did not finish")
					}
				}
				if x.count(t, "model.import_started", id) != 1 {
					t.Fatal("parallel audit duplicated")
				}
			case "changed_fingerprint":
				v := x.start(t)
				changed := x.input
				changed.Name = "Changed request"
				got, e := x.s.StartImport(x.ctx, x.admin, changed, x.key, uuid.NewString())
				humanRefused(t, got, e, "IDEMPOTENCY_CONFLICT")
				x.resolve(t, v.ID)
			case "scope_isolation":
				v := x.start(t)
				other, tenant, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other','active')`, other, x.f.Tenant)
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign','active')`, tenant)
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign','active')`, foreign, tenant)
				r, e := x.s.ResolveImport(x.ctx, x.admin, other, x.key)
				if e != nil || r.Status != models.ImportResolutionAbsent || r.Import != nil {
					t.Fatal("resolution leaked another Workspace", e)
				}
				r, e = x.s.ResolveImport(x.ctx, x.admin, foreign, x.key)
				modelReadError(t, e, "RESOURCE_NOT_FOUND")
				if r.Status != "" || r.Import != nil {
					t.Fatal("foreign resolution projection")
				}
				input := x.input
				input.WorkspaceID = foreign
				got, e := x.s.StartImport(x.ctx, x.admin, input, uuid.NewString(), uuid.NewString())
				humanRefused(t, got, e, "RESOURCE_NOT_FOUND")
				x.resolve(t, v.ID)
			case "portal_refusal", "revoked_session":
				v := x.small(t)
				a := x.portal
				code := "ACCESS_DENIED"
				if name == "revoked_session" {
					a = x.admin
					code = "AUTH_SESSION_EXPIRED"
					x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_human_import' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_human_import' WHERE id=?`, a.SessionID)
				}
				got, e := x.s.StartImport(x.ctx, a, x.input, x.key, uuid.NewString())
				humanRefused(t, got, e, code)
				b, h := humanPayload()
				got, e = x.s.UploadChunk(x.ctx, a, v.ID, 0, 100, h, bytes.NewReader(b))
				humanRefused(t, got, e, code)
				got, e = x.s.QueueImport(x.ctx, a, v.ID, "Denied queue", uuid.NewString())
				humanRefused(t, got, e, code)
				r, e := x.s.ResolveImport(x.ctx, a, x.f.Workspace, x.key)
				modelReadError(t, e, code)
				if r.Import != nil || r.Status != "" {
					t.Fatal("denied resolution DTO")
				}
				x.chunkCount(t, v.ID, 0)
			case "actor_recheck_after_read":
				v := x.small(t)
				gate := &humanTransactionStore{Store: x.f.Store, gate: 2, entered: make(chan struct{}), release: make(chan struct{})}
				x.owner.Storage = gate
				type result struct {
					v models.Import
					e error
				}
				done := make(chan result, 1)
				b, h := humanPayload()
				go func() {
					v, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(b))
					done <- result{v, e}
				}()
				select {
				case <-gate.entered:
				case <-x.ctx.Done():
					t.Fatal("upload did not reach actual mutation fence")
				}
				x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_human_fence' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_human_fence' WHERE id=?`, x.admin.SessionID)
				close(gate.release)
				select {
				case r := <-done:
					humanRefused(t, r.v, r.e, "AUTH_SESSION_EXPIRED")
				case <-x.ctx.Done():
					t.Fatal("fenced upload did not finish")
				}
				x.chunkCount(t, v.ID, 0)
				if _, e := os.Lstat(filepath.Join(x.root, "imports", v.ID)); !os.IsNotExist(e) {
					t.Fatal("revoked actor created upload scope", e)
				}
			case "chunk_replay_queue":
				v := x.small(t)
				got, e := x.s.QueueImport(x.ctx, x.admin, v.ID, "Refuse incomplete synthetic bytes", uuid.NewString())
				humanRefused(t, got, e, "MODEL_IMPORT_INCOMPLETE")
				x.upload(t, v.ID)
				x.upload(t, v.ID)
				x.chunkCount(t, v.ID, 1)
				for range 2 {
					got, e = x.s.QueueImport(x.ctx, x.admin, v.ID, "Queue verified chunk metadata", uuid.NewString())
					if e != nil || got.State != "queued" {
						t.Fatal("actual queue/replay", e)
					}
				}
				if x.count(t, "model.import_queued", v.ID) != 1 {
					t.Fatal("queue replay audit duplicated")
				}
			case "invalid_chunks":
				v := x.small(t)
				b, h := humanPayload()
				for _, raw := range [][]byte{b[:99], append(append([]byte{}, b...), byte('x'))} {
					got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(raw))
					humanRefused(t, got, e, "MODEL_CHUNK_INVALID")
				}
				got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, "", bytes.NewReader(b))
				humanRefused(t, got, e, "MODEL_CHUNK_INVALID")
				got, e = x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, nil)
				humanRefused(t, got, e, "MODEL_CHUNK_INVALID")
				x.chunkCount(t, v.ID, 0)
				x.upload(t, v.ID)
			case "chunk_tamper":
				v := x.small(t)
				x.upload(t, v.ID)
				path := filepath.Join(x.root, "imports", v.ID, "chunk-000000000000")
				tampered := bytes.Repeat([]byte("y"), 100)
				if e := os.WriteFile(path, tampered, 0600); e != nil {
					t.Fatal(e)
				}
				b, h := humanPayload()
				got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(b))
				humanRefused(t, got, e, "MODEL_CHUNK_CONFLICT")
				if raw, e := os.ReadFile(path); e != nil || !bytes.Equal(raw, tampered) {
					t.Fatal("conflicting file was overwritten", e)
				}
				x.chunkCount(t, v.ID, 1)
			case "file_survives_SQL_rollback":
				v := x.small(t)
				if x.f.Store.Dialect() == transaction.PostgreSQL {
					x.exec(t, `CREATE FUNCTION human_chunk_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic chunk rollback'; END $$`, "")
					x.exec(t, `CREATE TRIGGER human_chunk_fault BEFORE INSERT ON model_import_chunks FOR EACH ROW EXECUTE FUNCTION human_chunk_fault()`, "")
				} else {
					x.exec(t, "", `CREATE TRIGGER human_chunk_fault BEFORE INSERT ON model_import_chunks FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic chunk rollback'`)
				}
				b, h := humanPayload()
				got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(b))
				humanRefused(t, got, e, "STORAGE_UNAVAILABLE")
				x.chunkCount(t, v.ID, 0)
				path := filepath.Join(x.root, "imports", v.ID, "chunk-000000000000")
				if raw, e := os.ReadFile(path); e != nil || !bytes.Equal(raw, b) {
					t.Fatal("surviving physical chunk not retained", e)
				}
				x.exec(t, `DROP TRIGGER human_chunk_fault ON model_import_chunks`, `DROP TRIGGER human_chunk_fault`)
				if x.f.Store.Dialect() == transaction.PostgreSQL {
					x.exec(t, `DROP FUNCTION human_chunk_fault()`, "")
				}
				x.upload(t, v.ID)
				x.chunkCount(t, v.ID, 1)
			case "queue_audit_rollback", "intent_audit_rollback":
				action := "model.import_started"
				var v models.Import
				if name == "queue_audit_rollback" {
					action = "model.import_queued"
					v = x.small(t)
					x.upload(t, v.ID)
				}
				repair := (cancellationFixture{modelReadFixture: x.modelReadFixture}).auditFault(t, action)
				if name == "intent_audit_rollback" {
					got, e := x.s.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
					humanRefused(t, got, e, "STORAGE_UNAVAILABLE")
					r, e := x.s.ResolveImport(x.ctx, x.admin, x.f.Workspace, x.key)
					if e != nil || r.Status != models.ImportResolutionAbsent {
						t.Fatal("rolled-back intent survived", e)
					}
					repair()
					x.start(t)
				} else {
					got, e := x.s.QueueImport(x.ctx, x.admin, v.ID, "Synthetic refused queue audit", uuid.NewString())
					humanRefused(t, got, e, "STORAGE_UNAVAILABLE")
					current, e := x.s.GetImport(x.ctx, x.admin, v.ID)
					if e != nil || current.State != "uploading" || current.UploadedBytes != 100 {
						t.Fatal("queue/audit rollback lost prior chunk", e)
					}
					repair()
					got, e = x.s.QueueImport(x.ctx, x.admin, v.ID, "Recovered queue audit", uuid.NewString())
					if e != nil || got.State != "queued" || x.count(t, action, v.ID) != 1 {
						t.Fatal("actual queue audit repair", e)
					}
				}
			case "file_lock_busy":
				v := x.small(t)
				parent := filepath.Join(x.root, "import-locks")
				if e := os.Mkdir(parent, 0700); e != nil {
					t.Fatal(e)
				}
				file, e := os.OpenFile(filepath.Join(parent, ".lock-"+v.ID), os.O_CREATE|os.O_RDWR, 0600)
				if e != nil {
					t.Fatal(e)
				}
				defer file.Close()
				if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
					t.Fatal(e)
				}
				b, h := humanPayload()
				got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(b))
				humanRefused(t, got, e, "MODEL_IMPORT_BUSY")
				x.chunkCount(t, v.ID, 0)
				if e = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); e != nil {
					t.Fatal(e)
				}
				x.upload(t, v.ID)
			case "pending_admission":
				v := x.small(t)
				x.exec(t, `UPDATE model_imports SET error_code='MODEL_CANCELLATION_PENDING' WHERE id=$1`, `UPDATE model_imports SET error_code='MODEL_CANCELLATION_PENDING' WHERE id=?`, v.ID)
				b, h := humanPayload()
				got, e := x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(b))
				humanRefused(t, got, e, "MODEL_IMPORT_STATE_CONFLICT")
				got, e = x.s.QueueImport(x.ctx, x.admin, v.ID, "Refused pending queue", uuid.NewString())
				humanRefused(t, got, e, "MODEL_IMPORT_STATE_CONFLICT")
				x.chunkCount(t, v.ID, 0)
			case "cancelled_context", "expired_deadline":
				ctx, cancel := context.WithCancel(x.ctx)
				code := string(transaction.Cancelled)
				if name == "expired_deadline" {
					cancel()
					ctx, cancel = context.WithDeadline(x.ctx, time.Now().Add(-time.Second))
					code = string(transaction.Timeout)
				}
				cancel()
				got, e := x.s.StartImport(ctx, x.admin, x.input, x.key, uuid.NewString())
				humanRefused(t, got, e, code)
				r, e := x.s.ResolveImport(ctx, x.admin, x.f.Workspace, x.key)
				modelReadError(t, e, code)
				if r.Import != nil || r.Status != "" {
					t.Fatal("cancelled resolution DTO")
				}
			case "unknown_intent", "unknown_upload", "unknown_queue", "unknown_resolution":
				phase := int32(1)
				var v models.Import
				if name == "unknown_upload" || name == "unknown_queue" {
					v = x.small(t)
					phase = 2
					if name == "unknown_queue" {
						x.upload(t, v.ID)
					}
				} else if name == "unknown_resolution" {
					v = x.start(t)
				}
				x.owner.Storage = &humanTransactionStore{Store: x.f.Store, unknown: phase}
				if name == "unknown_resolution" {
					r, e := x.s.ResolveImport(x.ctx, x.admin, x.f.Workspace, x.key)
					modelReadError(t, e, string(transaction.CommitUnknown))
					if r.Import != nil || r.Status != "" {
						t.Fatal("unknown resolution DTO")
					}
				} else {
					var got models.Import
					var e error
					switch name {
					case "unknown_intent":
						got, e = x.s.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
					case "unknown_upload":
						b, h := humanPayload()
						got, e = x.s.UploadChunk(x.ctx, x.admin, v.ID, 0, 100, h, bytes.NewReader(b))
					case "unknown_queue":
						got, e = x.s.QueueImport(x.ctx, x.admin, v.ID, "Synthetic lost queue outcome", uuid.NewString())
					}
					humanRefused(t, got, e, string(transaction.CommitUnknown))
				}
				x.owner.Storage = x.f.Store
				if name == "unknown_intent" {
					r, e := x.s.ResolveImport(x.ctx, x.admin, x.f.Workspace, x.key)
					if e != nil || r.Status != models.ImportResolutionFound || r.Import == nil {
						t.Fatal("durable unknown intent not resolved", e)
					}
					v = x.start(t)
					if v.ID != r.Import.ID || x.count(t, "model.import_started", v.ID) != 1 {
						t.Fatal("explicit resolved replay duplicated intent")
					}
				} else if name == "unknown_resolution" {
					x.resolve(t, v.ID)
				} else if name == "unknown_upload" {
					x.upload(t, v.ID)
					x.chunkCount(t, v.ID, 1)
				} else {
					got, e := x.s.QueueImport(x.ctx, x.admin, v.ID, "Explicit queue reconciliation", uuid.NewString())
					if e != nil || got.State != "queued" || x.count(t, "model.import_queued", v.ID) != 1 {
						t.Fatal("unknown queue durable read/replay", e)
					}
				}
			case "policy_decrease_replay":
				x.input.ExpectedBytes += 100000
				v := x.start(t)
				p := config.DefaultRuntimePolicy()
				p.Models.MaximumBundleBytes = int(modelartifact.MinimumArchiveBytes())
				x.owner.Config.Runtime = &p
				again := x.start(t)
				if again.ID != v.ID {
					t.Fatal("policy decrease invalidated retained replay")
				}
				got, e := x.s.StartImport(x.ctx, x.admin, x.input, uuid.NewString(), uuid.NewString())
				humanRefused(t, got, e, "VALIDATION_FAILED")
				x.resolve(t, v.ID)
				got, e = x.s.CancelImport(x.ctx, x.admin, v.ID, "Cancel intent after cap decrease", uuid.NewString())
				if e != nil || got.State != "cancelled" {
					t.Fatal("cap decrease prevented cancellation", e)
				}
			}
		})
	}
}
