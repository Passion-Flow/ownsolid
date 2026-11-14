//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const cancellationPending = "MODEL_CANCELLATION_PENDING"

type cancellationFixture struct {
	modelReadFixture
	id, root, chunk string
}

func cancellationSetup(t *testing.T) cancellationFixture {
	t.Helper()
	x := cancellationFixture{modelReadFixture: modelReadSetup(t), root: t.TempDir()}
	x.id = x.imports[0]
	if e := os.Chmod(x.root, 0700); e != nil {
		t.Fatal(e)
	}
	x.s.RootDirectory = x.root
	x.exec(t, `UPDATE model_imports SET state='uploading',uploaded_bytes=100 WHERE id=$1`, `UPDATE model_imports SET state='uploading',uploaded_bytes=100 WHERE id=?`, x.id)
	x.chunk = filepath.Join(x.root, "imports", x.id, "chunk-000000000000")
	if e := os.MkdirAll(filepath.Dir(x.chunk), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(x.chunk, bytes.Repeat([]byte("x"), 100), 0600); e != nil {
		t.Fatal(e)
	}
	return x
}

func (x cancellationFixture) cancel(t *testing.T) models.Import {
	t.Helper()
	v, e := x.s.CancelImport(x.ctx, x.admin, x.id, "Discard isolated synthetic upload", uuid.NewString())
	if e != nil || v.ID != x.id || v.State != "cancelled" || v.ErrorCode != nil {
		t.Fatalf("actual cancellation: %+v %v", v, e)
	}
	return v
}

func (x cancellationFixture) refused(t *testing.T, code string) {
	t.Helper()
	v, e := x.s.CancelImport(x.ctx, x.admin, x.id, "Refused isolated synthetic cancellation", uuid.NewString())
	modelReadError(t, e, code)
	if v.ID != "" || v.ErrorCode != nil || v.State != "" {
		t.Fatal("refused cancellation published a DTO")
	}
}

func (x cancellationFixture) durable(t *testing.T, state string, pending bool) {
	t.Helper()
	var got string
	var marker *string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT state,error_code FROM model_imports WHERE id=$1`, `SELECT state,error_code FROM model_imports WHERE id=?`, x.id)).Scan(&got, &marker); e != nil {
		t.Fatal(e)
	}
	if got != state || (marker != nil && *marker == cancellationPending) != pending {
		t.Fatalf("durable cancellation state: %s %v", got, marker)
	}
}

func (x cancellationFixture) preserved(t *testing.T) {
	t.Helper()
	raw, e := os.ReadFile(x.chunk)
	if e != nil || !bytes.Equal(raw, bytes.Repeat([]byte("x"), 100)) {
		t.Fatal("original upload was not preserved", e)
	}
}

func (x cancellationFixture) auditCount(t *testing.T, requested, completed int) {
	t.Helper()
	for action, expected := range map[string]int{"model.import_cancellation_requested": requested, "model.import_cancelled": completed} {
		var n int
		if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE action=$1 AND target_id=$2`, `SELECT COUNT(*) FROM audit_events WHERE action=? AND target_id=?`, action, x.id)).Scan(&n); e != nil || n != expected {
			t.Fatalf("actual cancellation audit %s count=%d expected=%d: %v", action, n, expected, e)
		}
	}
}

// These faults are actual isolated SQL triggers, never production fault switches.
func (x cancellationFixture) auditFault(t *testing.T, action string) func() {
	t.Helper()
	pg := fmt.Sprintf(`CREATE FUNCTION cancellation_audit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='%s' THEN RAISE EXCEPTION 'synthetic cancellation audit refusal'; END IF; RETURN NEW; END $$`, action)
	my := fmt.Sprintf(`CREATE TRIGGER cancellation_audit_fault BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='%s' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic cancellation audit refusal'; END IF; END`, action)
	x.exec(t, pg, my)
	if x.f.Store.Dialect() == transaction.PostgreSQL {
		x.exec(t, `CREATE TRIGGER cancellation_audit_fault BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION cancellation_audit_fault()`, "")
	}
	return func() {
		x.exec(t, `DROP TRIGGER cancellation_audit_fault ON audit_events`, `DROP TRIGGER cancellation_audit_fault`)
		if x.f.Store.Dialect() == transaction.PostgreSQL {
			x.exec(t, `DROP FUNCTION cancellation_audit_fault()`, "")
		}
	}
}

// Explicitly inject a lost outcome AFTER a real vendor commit. Actual wire
// loss is a separate required gate; this double tests safe DTO/file behavior.
type cancellationUnknownStore struct {
	transaction.Store
	phase int
}

type cancellationUnknownSession struct {
	transaction.Store
	phase, calls int
}

func (s cancellationUnknownStore) WithSession(ctx context.Context, fn func(context.Context, transaction.Store) error) error {
	return s.Store.(transaction.SessionStore).WithSession(ctx, func(ctx context.Context, q transaction.Store) error {
		return fn(ctx, &cancellationUnknownSession{Store: q, phase: s.phase})
	})
}

func (s *cancellationUnknownSession) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, readOnly, fn); e != nil {
		return e
	}
	s.calls++
	if s.calls == s.phase {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

func TestActualModelImportCancellation(t *testing.T) {
	for _, name := range []string{"ordinary_replay", "portal_and_revocation", "disabled_workspace", "ready_refusal", "unknown_entry", "hardlink_preservation", "intent_audit_failure", "final_audit_recovery", "pending_admission", "file_lock_busy", "database_lock_busy", "cancelled_context", "unknown_intent", "unknown_final"} {
		t.Run(name, func(t *testing.T) {
			x := cancellationSetup(t)
			switch name {
			case "ordinary_replay":
				objects := filepath.Join(x.root, "objects")
				if e := os.Mkdir(objects, 0700); e != nil {
					t.Fatal(e)
				}
				sentinel := filepath.Join(objects, "immutable-sentinel")
				if e := os.WriteFile(sentinel, []byte("PRESERVE_IMMUTABLE"), 0600); e != nil {
					t.Fatal(e)
				}
				x.cancel(t)
				x.cancel(t)
				x.durable(t, "cancelled", false)
				x.auditCount(t, 1, 1)
				if _, e := os.Lstat(filepath.Dir(x.chunk)); !os.IsNotExist(e) {
					t.Fatal("upload scope remains", e)
				}
				if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "PRESERVE_IMMUTABLE" {
					t.Fatal("immutable object changed", e)
				}
			case "portal_and_revocation":
				v, e := x.s.CancelImport(x.ctx, x.portal, x.id, "Denied Portal cancellation", uuid.NewString())
				modelReadError(t, e, "ACCESS_DENIED")
				if v.ID != "" {
					t.Fatal("Portal cancellation DTO")
				}
				x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_cancel' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_cancel' WHERE id=?`, x.admin.SessionID)
				x.refused(t, "AUTH_SESSION_EXPIRED")
				x.preserved(t)
				x.durable(t, "uploading", false)
				x.auditCount(t, 0, 0)
			case "disabled_workspace":
				x.exec(t, `UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, x.f.Workspace)
				x.refused(t, "ACCESS_DENIED")
				x.preserved(t)
				x.auditCount(t, 0, 0)
			case "ready_refusal":
				x.exec(t, `UPDATE model_imports SET state='ready',model_id=$1 WHERE id=$2`, `UPDATE model_imports SET state='ready',model_id=? WHERE id=?`, x.model, x.id)
				x.refused(t, "MODEL_IMPORT_STATE_CONFLICT")
				x.preserved(t)
				x.auditCount(t, 0, 0)
			case "unknown_entry", "hardlink_preservation":
				unknown := filepath.Join(filepath.Dir(x.chunk), "unknown-sentinel")
				if name == "unknown_entry" {
					if e := os.WriteFile(unknown, []byte("PRESERVE_UNKNOWN"), 0600); e != nil {
						t.Fatal(e)
					}
				} else {
					unknown = filepath.Join(x.root, "unowned-hardlink")
					if e := os.Link(x.chunk, unknown); e != nil {
						t.Fatal(e)
					}
				}
				x.refused(t, "MODEL_CLEANUP_CONFLICT")
				x.preserved(t)
				x.durable(t, "uploading", true)
				x.auditCount(t, 1, 0)
				if _, e := os.Lstat(unknown); e != nil {
					t.Fatal("unknown/link lost", e)
				}
				if e := os.Remove(unknown); e != nil {
					t.Fatal(e)
				}
				x.cancel(t)
				x.auditCount(t, 1, 1)
			case "intent_audit_failure":
				repair := x.auditFault(t, "model.import_cancellation_requested")
				x.refused(t, "STORAGE_UNAVAILABLE")
				x.preserved(t)
				x.durable(t, "uploading", false)
				x.auditCount(t, 0, 0)
				repair()
				x.cancel(t)
				x.auditCount(t, 1, 1)
			case "final_audit_recovery":
				repair := x.auditFault(t, "model.import_cancelled")
				x.refused(t, "STORAGE_UNAVAILABLE")
				x.durable(t, "uploading", true)
				x.auditCount(t, 1, 0)
				if _, e := os.Lstat(filepath.Dir(x.chunk)); !os.IsNotExist(e) {
					t.Fatal("confirmed intent did not remove owned scope", e)
				}
				repair()
				x.cancel(t)
				x.auditCount(t, 1, 1)
			case "pending_admission":
				x.exec(t, `UPDATE model_imports SET state='queued',error_code=$1 WHERE id=$2`, `UPDATE model_imports SET state='queued',error_code=? WHERE id=?`, cancellationPending, x.id)
				v, e := x.s.UploadChunk(x.ctx, x.admin, x.id, 0, 100, "", bytes.NewReader(nil))
				modelReadError(t, e, "MODEL_IMPORT_STATE_CONFLICT")
				if v.ID != "" {
					t.Fatal("pending upload DTO")
				}
				v, e = x.s.QueueImport(x.ctx, x.admin, x.id, "Refused pending retry", uuid.NewString())
				modelReadError(t, e, "MODEL_IMPORT_STATE_CONFLICT")
				if v.ID != "" {
					t.Fatal("pending queue DTO")
				}
				if worked, e := x.s.ProcessNext(x.ctx); e != nil || worked {
					t.Fatal("pending paired worker admitted import", e)
				}
				x.preserved(t)
				x.durable(t, "queued", true)
				x.auditCount(t, 0, 0)
			case "file_lock_busy":
				parent := filepath.Join(x.root, "import-locks")
				if e := os.Mkdir(parent, 0700); e != nil {
					t.Fatal(e)
				}
				file, e := os.OpenFile(filepath.Join(parent, ".lock-"+x.id), os.O_CREATE|os.O_RDWR, 0600)
				if e != nil {
					t.Fatal(e)
				}
				defer file.Close()
				if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
					t.Fatal(e)
				}
				x.refused(t, "MODEL_IMPORT_BUSY")
				x.preserved(t)
				x.durable(t, "uploading", false)
				x.auditCount(t, 0, 0)
				if e = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); e != nil {
					t.Fatal(e)
				}
				x.cancel(t)
			case "database_lock_busy":
				e := x.f.Store.(transaction.SessionStore).WithSession(x.ctx, func(ctx context.Context, q transaction.Store) error {
					var key any
					if q.Dialect() == transaction.MySQL {
						var database string
						if e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT DATABASE()`)).Scan(&database); e != nil {
							return e
						}
						digest := sha256.Sum256([]byte(database + "\x00" + x.id))
						key = "projectone:mi:v1:" + hex.EncodeToString(digest[:20])
					} else {
						digest := sha256.Sum256([]byte("model-import:v1:" + x.id))
						key = int64(binary.LittleEndian.Uint64(digest[:8]))
					}
					var locked bool
					if e := q.QueryRow(ctx, x.f.Statement(`SELECT pg_try_advisory_lock($1)`, `SELECT GET_LOCK(?,0)`, key)).Scan(&locked); e != nil || !locked {
						t.Fatal("actual competing named lock", e)
					}
					x.refused(t, "MODEL_IMPORT_BUSY")
					x.preserved(t)
					x.durable(t, "uploading", false)
					x.auditCount(t, 0, 0)
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
				x.cancel(t)
			case "cancelled_context":
				ctx, cancel := context.WithCancel(x.ctx)
				cancel()
				v, e := x.s.CancelImport(ctx, x.admin, x.id, "Cancelled synthetic caller", uuid.NewString())
				modelReadError(t, e, string(transaction.Cancelled))
				if v.ID != "" {
					t.Fatal("cancelled caller DTO")
				}
				x.preserved(t)
				x.durable(t, "uploading", false)
				x.cancel(t)
			case "unknown_intent", "unknown_final":
				phase := 1
				if name == "unknown_final" {
					phase = 2
				}
				x.owner.Storage = cancellationUnknownStore{Store: x.f.Store, phase: phase}
				x.refused(t, string(transaction.CommitUnknown))
				x.owner.Storage = x.f.Store
				if phase == 1 {
					x.preserved(t)
					x.durable(t, "uploading", true)
					x.auditCount(t, 1, 0)
				} else {
					x.durable(t, "cancelled", false)
					x.auditCount(t, 1, 1)
				}
				x.cancel(t)
				x.auditCount(t, 1, 1)
			}
		})
	}
}
