//go:build repository_integration

package models_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func workerQueued(t *testing.T, x humanImportFixture, state string, attempts int) models.Import {
	t.Helper()
	v := x.small(t)
	x.exec(t, `UPDATE model_imports SET state=$1,attempts=$2,uploaded_bytes=expected_bytes WHERE id=$3`, `UPDATE model_imports SET state=?,attempts=?,uploaded_bytes=expected_bytes WHERE id=?`, state, attempts, v.ID)
	return v
}
func workerState(t *testing.T, x humanImportFixture, id, state string, attempts int, code string) {
	t.Helper()
	var got string
	var n int
	var marker *string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT state,attempts,error_code FROM model_imports WHERE id=$1`, `SELECT state,attempts,error_code FROM model_imports WHERE id=?`, id)).Scan(&got, &n, &marker); e != nil {
		t.Fatal(e)
	}
	if got != state || n != attempts || code == "" && marker != nil || code != "" && (marker == nil || *marker != code) {
		t.Fatalf("durable worker state=%s attempts=%d marker=%v", got, n, marker)
	}
}
func workerResult(t *testing.T, x humanImportFixture, code string) {
	t.Helper()
	owned, e := x.s.ProcessNext(x.ctx)
	if !owned {
		t.Fatal("worker did not own selected import", e)
	}
	modelReadError(t, e, code)
}

// Explicit lost-outcome double after a real claim transaction. Wire faults are
// separately required; this double does not emulate the SQL/filesystem engine.
type workerUnknownOwner struct{ transaction.Store }
type workerUnknownSession struct {
	transaction.Store
	calls int
}

func (s workerUnknownOwner) WithSession(ctx context.Context, fn func(context.Context, transaction.Store) error) error {
	return s.Store.(transaction.SessionStore).WithSession(ctx, func(ctx context.Context, q transaction.Store) error { return fn(ctx, &workerUnknownSession{Store: q}) })
}
func (s *workerUnknownSession) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, ro, fn); e != nil {
		return e
	}
	s.calls++
	if s.calls == 1 {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

func TestModelWorkerFileLockChild(t *testing.T) {
	if os.Getenv("PROJECT_ONE_MODEL_LOCK_CHILD") != "1" {
		return
	}
	root, id := os.Getenv("PROJECT_ONE_MODEL_LOCK_ROOT"), os.Getenv("PROJECT_ONE_MODEL_LOCK_ID")
	parsed, e := uuid.Parse(id)
	resolved, re := filepath.EvalSymlinks(root)
	info, se := os.Lstat(root)
	if e != nil || parsed == uuid.Nil || parsed.String() != id || re != nil || resolved != root || se != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("private child lock input")
	}
	f, e := os.OpenFile(filepath.Join(root, "import-locks", ".lock-"+id), os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	fmt.Fprintln(os.Stdout, "MODEL_WORKER_LOCK_READY")
	if _, e = io.Copy(io.Discard, os.Stdin); e != nil {
		t.Fatal(e)
	}
}

func TestActualModelImportWorker(t *testing.T) {
	for _, name := range []string{"idle", "pending_marker", "invalid_total_failure", "disabled_user", "demoted_admin", "changed_epoch", "disabled_workspace", "inactive_tenant", "foreign_selector", "verifying_attempt_reuse", "exhausted_queue", "orphan_ready_audit", "orphan_model", "claim_audit_rollback", "failure_audit_rollback", "partial_stage_retained", "cross_process_file_busy", "database_lock_busy", "unknown_claim_double", "cancelled_context"} {
		t.Run(name, func(t *testing.T) {
			x := humanSetup(t)
			switch name {
			case "idle":
				before := x.audits(t)
				if owned, e := x.s.ProcessNext(x.ctx); e != nil || owned {
					t.Fatal("idle worker", e)
				}
				if x.audits(t) != before {
					t.Fatal("idle worker wrote audit")
				}
			case "pending_marker":
				v := workerQueued(t, x, "queued", 0)
				x.exec(t, `UPDATE model_imports SET error_code='MODEL_CANCELLATION_PENDING' WHERE id=$1`, `UPDATE model_imports SET error_code='MODEL_CANCELLATION_PENDING' WHERE id=?`, v.ID)
				if owned, e := x.s.ProcessNext(x.ctx); e != nil || owned {
					t.Fatal("pending cancellation admitted", e)
				}
				workerState(t, x, v.ID, "queued", 0, cancellationPending)
			case "invalid_total_failure":
				v := workerQueued(t, x, "queued", 0)
				workerResult(t, x, "MODEL_IMPORT_INCOMPLETE")
				workerState(t, x, v.ID, "failed", 1, "MODEL_IMPORT_INCOMPLETE")
				if x.count(t, "model.import_verification_started", v.ID) != 1 || x.count(t, "model.import_failed", v.ID) != 1 {
					t.Fatal("claim/failure atomic audits")
				}
			case "disabled_user", "demoted_admin", "changed_epoch", "disabled_workspace", "inactive_tenant":
				v := workerQueued(t, x, "queued", 0)
				switch name {
				case "disabled_user":
					x.exec(t, `UPDATE users SET status='disabled' WHERE id=$1`, `UPDATE users SET status='disabled' WHERE id=?`, x.f.User)
				case "demoted_admin":
					x.exec(t, `UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, x.f.User)
				case "changed_epoch":
					x.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, x.f.User)
				case "disabled_workspace":
					x.exec(t, `UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, x.f.Workspace)
				case "inactive_tenant":
					x.exec(t, `UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, x.f.Tenant)
				}
				workerResult(t, x, "MODEL_ACTOR_UNAVAILABLE")
				workerState(t, x, v.ID, "failed", 0, "MODEL_ACTOR_UNAVAILABLE")
				if x.count(t, "model.import_verification_started", v.ID) != 0 || x.count(t, "model.import_failed", v.ID) != 1 {
					t.Fatal("unavailable authority mutated claim")
				}
			case "foreign_selector":
				v := workerQueued(t, x, "queued", 0)
				tenant, workspace := uuid.NewString(), uuid.NewString()
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign worker','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign worker','active')`, tenant)
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign worker','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign worker','active')`, workspace, tenant)
				x.exec(t, `UPDATE model_imports SET tenant_id=$1,workspace_id=$2 WHERE id=$3`, `UPDATE model_imports SET tenant_id=?,workspace_id=? WHERE id=?`, tenant, workspace, v.ID)
				if owned, e := x.s.ProcessNext(x.ctx); e != nil || owned {
					t.Fatal("foreign installed scope admitted", e)
				}
				workerState(t, x, v.ID, "queued", 0, "")
			case "verifying_attempt_reuse":
				v := workerQueued(t, x, "verifying", 2)
				p := config.DefaultRuntimePolicy()
				p.Models.MaximumAttempts = 1
				x.owner.Config.Runtime = &p
				workerResult(t, x, "MODEL_IMPORT_INCOMPLETE")
				workerState(t, x, v.ID, "failed", 2, "MODEL_IMPORT_INCOMPLETE")
				if x.count(t, "model.import_verification_started", v.ID) != 0 {
					t.Fatal("recovery consumed another attempt")
				}
			case "exhausted_queue":
				v := workerQueued(t, x, "queued", 3)
				workerResult(t, x, "MODEL_RECOVERY_REQUIRED")
				workerState(t, x, v.ID, "failed", 3, "MODEL_RECOVERY_REQUIRED")
			case "orphan_ready_audit":
				v := workerQueued(t, x, "verifying", 1)
				x.exec(t, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin','model.import_ready','success','model_import',$5,'Synthetic orphan ready audit',$6)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin','model.import_ready','success','model_import',?,'Synthetic orphan ready audit',?)`, uuid.NewString(), x.f.User, x.f.Tenant, x.f.Workspace, v.ID, uuid.NewString())
				workerResult(t, x, "MODEL_PUBLICATION_CONFLICT")
				workerState(t, x, v.ID, "verifying", 1, "")
			case "orphan_model":
				v := workerQueued(t, x, "verifying", 1)
				manifest, _ := json.Marshal(wire.ApprovedModel())
				x.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,$4,'qwen3-0.6b',$5,$6::jsonb,'ready',$7)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,?,'qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, v.ID, x.f.Tenant, x.f.Workspace, v.Name, wire.ModelCatalogDigest(), manifest, x.f.User)
				workerResult(t, x, "MODEL_PUBLICATION_CONFLICT")
				workerState(t, x, v.ID, "verifying", 1, "")
			case "claim_audit_rollback":
				v := workerQueued(t, x, "queued", 0)
				repair := (cancellationFixture{modelReadFixture: x.modelReadFixture}).auditFault(t, "model.import_verification_started")
				workerResult(t, x, "STORAGE_UNAVAILABLE")
				workerState(t, x, v.ID, "failed", 0, "MODEL_IMPORT_FAILED")
				if x.count(t, "model.import_verification_started", v.ID) != 0 {
					t.Fatal("failed claim audit survived")
				}
				repair()
			case "failure_audit_rollback":
				v := workerQueued(t, x, "queued", 0)
				repair := (cancellationFixture{modelReadFixture: x.modelReadFixture}).auditFault(t, "model.import_failed")
				workerResult(t, x, "STORAGE_UNAVAILABLE")
				workerState(t, x, v.ID, "verifying", 1, "")
				repair()
				workerResult(t, x, "MODEL_IMPORT_INCOMPLETE")
				workerState(t, x, v.ID, "failed", 1, "MODEL_IMPORT_INCOMPLETE")
				if x.count(t, "model.import_verification_started", v.ID) != 1 || x.count(t, "model.import_failed", v.ID) != 1 {
					t.Fatal("failure recovery duplicated attempt/audit")
				}
			case "partial_stage_retained":
				v := workerQueued(t, x, "verifying", 1)
				x.exec(t, `UPDATE model_imports SET expected_bytes=$1,uploaded_bytes=$2 WHERE id=$3`, `UPDATE model_imports SET expected_bytes=?,uploaded_bytes=? WHERE id=?`, modelartifact.MinimumArchiveBytes(), modelartifact.MinimumArchiveBytes(), v.ID)
				path := filepath.Join(x.root, "imports", v.ID, "stage-1")
				if e := os.MkdirAll(path, 0700); e != nil {
					t.Fatal(e)
				}
				sentinel := filepath.Join(path, "unknown-sentinel")
				if e := os.WriteFile(sentinel, []byte("PRESERVE_PARTIAL_STAGE"), 0600); e != nil {
					t.Fatal(e)
				}
				workerResult(t, x, "MODEL_STAGE_INVALID")
				workerState(t, x, v.ID, "failed", 1, "MODEL_STAGE_INVALID")
				if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "PRESERVE_PARTIAL_STAGE" {
					t.Fatal("partial stage was changed", e)
				}
			case "cross_process_file_busy":
				v := workerQueued(t, x, "queued", 0)
				parent := filepath.Join(x.root, "import-locks")
				if e := os.Mkdir(parent, 0700); e != nil {
					t.Fatal(e)
				}
				path := filepath.Join(parent, ".lock-"+v.ID)
				if e := os.WriteFile(path, nil, 0600); e != nil {
					t.Fatal(e)
				}
				before, e := os.Stat(path)
				if e != nil {
					t.Fatal(e)
				}
				binary, e := os.Executable()
				if e != nil {
					t.Fatal(e)
				}
				child := exec.CommandContext(x.ctx, binary, "-test.run=^TestModelWorkerFileLockChild$")
				child.Env = append(os.Environ(), "PROJECT_ONE_MODEL_LOCK_CHILD=1", "PROJECT_ONE_MODEL_LOCK_ROOT="+x.root, "PROJECT_ONE_MODEL_LOCK_ID="+v.ID)
				input, e := child.StdinPipe()
				if e != nil {
					t.Fatal(e)
				}
				output, e := child.StdoutPipe()
				if e != nil {
					t.Fatal(e)
				}
				var diagnostic bytes.Buffer
				child.Stderr = &diagnostic
				if e = child.Start(); e != nil {
					t.Fatal(e)
				}
				joined := false
				defer func() {
					input.Close()
					if !joined {
						child.Wait()
					}
				}()
				line, e := bufio.NewReader(output).ReadString('\n')
				if e != nil || line != "MODEL_WORKER_LOCK_READY\n" {
					t.Fatal("actual child ownership", e, diagnostic.String())
				}
				if owned, e := x.s.ProcessNext(x.ctx); e != nil || owned {
					t.Fatal("separate process lock was bypassed", e)
				}
				workerState(t, x, v.ID, "queued", 0, "")
				input.Close()
				_, _ = io.Copy(io.Discard, output)
				if e = child.Wait(); e != nil {
					t.Fatal("owned child cleanup", e, diagnostic.String())
				}
				joined = true
				after, e := os.Stat(path)
				if e != nil || !os.SameFile(before, after) {
					t.Fatal("live lock inode was replaced", e)
				}
				t.Log("Actual separate process held the same private inode; worker refused, child exited and inode remained.")
			case "database_lock_busy":
				v := workerQueued(t, x, "queued", 0)
				e := x.f.Store.(transaction.SessionStore).WithSession(x.ctx, func(ctx context.Context, q transaction.Store) error {
					var key any
					if q.Dialect() == transaction.MySQL {
						var name string
						if e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT DATABASE()`)).Scan(&name); e != nil {
							return e
						}
						digest := sha256.Sum256([]byte(name + "\x00" + v.ID))
						key = "projectone:mi:v1:" + hex.EncodeToString(digest[:20])
					} else {
						digest := sha256.Sum256([]byte("model-import:v1:" + v.ID))
						key = int64(binary.LittleEndian.Uint64(digest[:8]))
					}
					var locked bool
					if e := q.QueryRow(ctx, x.f.Statement(`SELECT pg_try_advisory_lock($1)`, `SELECT GET_LOCK(?,0)`, key)).Scan(&locked); e != nil || !locked {
						t.Fatal("competing worker session lock", e)
					}
					if owned, e := x.s.ProcessNext(ctx); e != nil || owned {
						t.Fatal("session ownership bypassed", e)
					}
					workerState(t, x, v.ID, "queued", 0, "")
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			case "unknown_claim_double":
				v := workerQueued(t, x, "queued", 0)
				x.owner.Storage = workerUnknownOwner{Store: x.f.Store}
				workerResult(t, x, string(transaction.CommitUnknown))
				x.owner.Storage = x.f.Store
				workerState(t, x, v.ID, "verifying", 1, "")
				if _, e := os.Lstat(filepath.Join(x.root, "objects")); !os.IsNotExist(e) {
					t.Fatal("unconfirmed claim changed artifact payload", e)
				}
				workerResult(t, x, "MODEL_IMPORT_INCOMPLETE")
				workerState(t, x, v.ID, "failed", 1, "MODEL_IMPORT_INCOMPLETE")
				if x.count(t, "model.import_verification_started", v.ID) != 1 {
					t.Fatal("unknown claim recovery consumed another attempt")
				}
			case "cancelled_context":
				ctx, cancel := context.WithCancel(x.ctx)
				cancel()
				owned, e := x.s.ProcessNext(ctx)
				modelReadError(t, e, string(transaction.Cancelled))
				if owned {
					t.Fatal("cancelled worker claimed ownership")
				}
			}
		})
	}
}
