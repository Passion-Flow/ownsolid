//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func archiveFile(t *testing.T, path string) *os.File {
	t.Helper()
	return archiveInputFile(t, path, "")
}

func archiveInputFile(t *testing.T, path, sharedAlias string) *os.File {
	t.Helper()
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || actual != path {
		t.Fatal("canonical approved archive input required")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		t.Fatal(e)
	}
	info, e := f.Stat()
	if e != nil {
		f.Close()
		t.Fatal(e)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0111 != 0 {
		f.Close()
		t.Fatal("owned regular approved archive input required")
	}
	if owner.Nlink != 1 {
		alias, e := os.Lstat(sharedAlias)
		canonical, resolveError := filepath.EvalSymlinks(sharedAlias)
		if e != nil || resolveError != nil || canonical != sharedAlias || !alias.Mode().IsRegular() || !os.SameFile(alias, info) || owner.Nlink != 2 || info.Mode().Perm() != 0444 {
			f.Close()
			t.Fatal("exact two read-only approved cache aliases required")
		}
	}
	return f
}

func archiveSetup(t *testing.T) humanImportFixture {
	t.Helper()
	x := humanSetup(t)
	// The enclosing executable case owns its configured timeout. The metadata
	// fixture's shorter context must not truncate actual model I/O acceptance.
	deadline, bounded := t.Deadline()
	if !bounded {
		t.Fatal("configured executable case deadline required")
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	x.ctx = ctx
	file := archiveFile(t, os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_ARCHIVE_FILE"))
	info, e := file.Stat()
	file.Close()
	if e != nil || info.Size() < modelartifact.MinimumArchiveBytes() || info.Size() > models.MaximumBundleBytes {
		t.Fatal("complete approved archive size required", e)
	}
	x.input.Name = "Actual approved archive publication"
	x.input.ExpectedBytes = info.Size()
	return x
}

func uploadArchive(t *testing.T, x humanImportFixture, id string) {
	t.Helper()
	file := archiveFile(t, os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_ARCHIVE_FILE"))
	defer file.Close()
	buffer := make([]byte, models.ChunkBytes)
	count := 0
	for offset := int64(0); offset < x.input.ExpectedBytes; {
		n, e := io.ReadFull(file, buffer[:min(models.ChunkBytes, x.input.ExpectedBytes-offset)])
		if e != nil {
			t.Fatal("actual approved archive read", e)
		}
		hash := sha256.Sum256(buffer[:n])
		v, e := x.s.UploadChunk(x.ctx, x.admin, id, offset, int64(n), hex.EncodeToString(hash[:]), bytes.NewReader(buffer[:n]))
		if e != nil || v.UploadedBytes != offset+int64(n) {
			t.Fatal("actual approved archive chunk upload", e)
		}
		offset += int64(n)
		count++
	}
	var n int
	var size int64
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*),SUM(size_bytes) FROM model_import_chunks WHERE import_id=$1`, `SELECT COUNT(*),SUM(size_bytes) FROM model_import_chunks WHERE import_id=?`, id)).Scan(&n, &size); e != nil || n != count || size != x.input.ExpectedBytes {
		t.Fatal("durable complete archive chunk inventory", e)
	}
	t.Logf("Actual approved archive uploaded: bytes=%d chunks=%d", size, n)
}

// These are explicit verified-object recovery preconditions, not a claim that
// this case uploaded chunks or reached publication through an earlier worker.
func archiveRecoveryObjects(t *testing.T, x humanImportFixture, id string) {
	t.Helper()
	parent := filepath.Join(x.root, "objects", id)
	if e := os.MkdirAll(parent, 0700); e != nil {
		t.Fatal(e)
	}
	directory := os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY")
	for _, expected := range wire.ApprovedModel().Files {
		source := archiveInputFile(t, filepath.Join(directory, expected.Path), filepath.Join(filepath.Dir(directory), expected.Path))
		destination, e := os.OpenFile(filepath.Join(parent, expected.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			source.Close()
			t.Fatal(e)
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(destination, hash), source)
		source.Close()
		if e == nil {
			e = destination.Sync()
		}
		if e == nil {
			e = destination.Chmod(0444)
		}
		closeError := destination.Close()
		if e != nil || closeError != nil || n != expected.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			t.Fatal("approved recovery object copy", e, closeError)
		}
	}
	if e := modelartifact.VerifyDirectory(x.ctx, parent); e != nil {
		t.Fatal("actual approved recovery precondition", e)
	}
	x.exec(t, `UPDATE model_imports SET uploaded_bytes=expected_bytes WHERE id=$1`, `UPDATE model_imports SET uploaded_bytes=expected_bytes WHERE id=?`, id)
}

func archiveQueue(t *testing.T, x humanImportFixture, id string) {
	t.Helper()
	v, e := x.s.QueueImport(x.ctx, x.admin, id, "Explicit approved publication acceptance", uuid.NewString())
	if e != nil || v.State != "queued" {
		t.Fatal("actual queue or explicit retry", e)
	}
}

func archiveNoPublication(t *testing.T, x humanImportFixture, id string) {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*) FROM model_versions WHERE id=$1`, `SELECT COUNT(*) FROM model_versions WHERE id=?`, id)).Scan(&n); e != nil || n != 0 || x.count(t, "model.import_ready", id) != 0 {
		t.Fatal("refused publication left model or ready audit", e)
	}
	if e := modelartifact.VerifyDirectory(x.ctx, filepath.Join(x.root, "objects", id)); e != nil {
		t.Fatal("verified recovery files were lost", e)
	}
}

func archiveReady(t *testing.T, x humanImportFixture, id string, attempts int) {
	t.Helper()
	v, e := x.s.GetImport(x.ctx, x.admin, id)
	if e != nil || v.State != "ready" || v.ModelID == nil || *v.ModelID != id || v.Attempts != attempts || v.ErrorCode != nil {
		t.Fatal("exact durable publication import identity", e)
	}
	for _, actor := range []identity.Auth{x.admin, x.portal} {
		list, e := x.s.List(x.ctx, actor, x.f.Workspace)
		if e != nil || len(list) != 1 || list[0].ID != id || list[0].Kind != "approved_base" || list[0].State != "ready" || list[0].CatalogDigest != wire.ModelCatalogDigest() {
			t.Fatal("actual Admin/Portal model inventory", e)
		}
		var got wire.ModelCatalog
		if e = json.Unmarshal(list[0].Manifest, &got); e != nil || !reflect.DeepEqual(got, wire.ApprovedModel()) {
			t.Fatal("exact approved typed model manifest", e)
		}
	}
	if x.count(t, "model.import_ready", id) != 1 || x.count(t, "model.import_verification_started", id) != attempts {
		t.Fatal("duplicate or missing ready/claim audit")
	}
	if e = modelartifact.VerifyDirectory(x.ctx, filepath.Join(x.root, "objects", id)); e != nil {
		t.Fatal("published object verification", e)
	}
}

// Only scheduling changes: each transaction executes against the actual vendor.
// Pause after a confirmed real claim, retaining both actual worker locks.
type archiveGateOwner struct {
	transaction.Store
	claimed, release chan struct{}
	once             sync.Once
}
type archiveGateSession struct {
	transaction.Store
	owner *archiveGateOwner
}

func (s *archiveGateOwner) WithSession(ctx context.Context, fn func(context.Context, transaction.Store) error) error {
	return s.Store.(transaction.SessionStore).WithSession(ctx, func(ctx context.Context, q transaction.Store) error {
		return fn(ctx, &archiveGateSession{Store: q, owner: s})
	})
}
func (s *archiveGateSession) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, ro, fn); e != nil {
		return e
	}
	var wait bool
	s.owner.once.Do(func() { wait = true; close(s.owner.claimed) })
	if wait {
		select {
		case <-s.owner.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func archiveRefreshSession(t *testing.T, x humanImportFixture, audience string) identity.Auth {
	t.Helper()
	var epoch int64
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT auth_epoch FROM users WHERE id=$1`, `SELECT auth_epoch FROM users WHERE id=?`, x.f.User)).Scan(&epoch); e != nil {
		t.Fatal(e)
	}
	var issued identity.IssuedSession
	if e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, x.f.User, epoch, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	result, e := x.owner.Authenticate(x.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return result
}

func TestActualModelImportArchive(t *testing.T) {
	for _, name := range []string{"archive_audit_recovery", "interphase_epoch", "interphase_workspace"} {
		t.Run(name, func(t *testing.T) {
			x := archiveSetup(t)
			v := x.start(t)
			if name == "archive_audit_recovery" {
				uploadArchive(t, x, v.ID)
				archiveQueue(t, x, v.ID)
				repair := (cancellationFixture{modelReadFixture: x.modelReadFixture}).auditFault(t, "model.import_ready")
				workerResult(t, x, "STORAGE_UNAVAILABLE")
				workerState(t, x, v.ID, "failed", 1, "MODEL_IMPORT_FAILED")
				archiveNoPublication(t, x, v.ID)
				if x.count(t, "model.import_failed", v.ID) != 1 {
					t.Fatal("publication failure bookkeeping absent")
				}
				repair()
				archiveQueue(t, x, v.ID)
			} else {
				archiveRecoveryObjects(t, x, v.ID)
				archiveQueue(t, x, v.ID)
				gate := &archiveGateOwner{Store: x.f.Store, claimed: make(chan struct{}), release: make(chan struct{})}
				owner := *x.owner
				owner.Storage = gate
				worker := *x.s
				worker.Identity = &owner
				workspaceService := *x.s.Workspaces
				workspaceService.Identity = &owner
				worker.Workspaces = &workspaceService
				type result struct {
					owned bool
					err   error
				}
				done := make(chan result, 1)
				stopped := make(chan struct{})
				go func() { defer close(stopped); owned, e := worker.ProcessNext(x.ctx); done <- result{owned, e} }()
				var release sync.Once
				unblock := func() { release.Do(func() { close(gate.release) }) }
				t.Cleanup(func() {
					unblock()
					timer := time.NewTimer(config.Seconds(owner.Config.Policy().Models.CleanupSeconds))
					defer timer.Stop()
					select {
					case <-stopped:
					case <-timer.C:
						t.Error("owned interphase worker cleanup deadline")
					}
				})
				select {
				case <-gate.claimed:
				case got := <-done:
					t.Fatal("worker exited before actual claim gate", got.err)
				case <-x.ctx.Done():
					t.Fatal("actual claim gate deadline")
				}
				workerState(t, x, v.ID, "verifying", 1, "")
				refused, e := x.s.CancelImport(x.ctx, x.admin, v.ID, "Competing isolated cancellation", uuid.NewString())
				humanRefused(t, refused, e, "MODEL_IMPORT_BUSY")
				if name == "interphase_epoch" {
					x.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, x.f.User)
				} else {
					x.exec(t, `UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, x.f.Workspace)
				}
				unblock()
				select {
				case got := <-done:
					if !got.owned {
						t.Fatal("claimed worker lost ownership result")
					}
					modelReadError(t, got.err, "MODEL_ACTOR_UNAVAILABLE")
				case <-x.ctx.Done():
					t.Fatal("actual interphase worker deadline")
				}
				workerState(t, x, v.ID, "failed", 1, "MODEL_ACTOR_UNAVAILABLE")
				archiveNoPublication(t, x, v.ID)
				if x.count(t, "model.import_failed", v.ID) != 1 || x.count(t, "model.import_cancellation_requested", v.ID) != 0 {
					t.Fatal("interphase failure/cancellation audit identity")
				}
				if name == "interphase_epoch" {
					stale, e := x.s.QueueImport(x.ctx, x.admin, v.ID, "Stale epoch must not recover", uuid.NewString())
					if e == nil || stale.ID != "" {
						t.Fatal("stale epoch published retry DTO")
					}
					x.admin = archiveRefreshSession(t, x, "admin")
					x.portal = archiveRefreshSession(t, x, "portal")
				} else {
					x.exec(t, `UPDATE workspaces SET status='active' WHERE id=$1`, `UPDATE workspaces SET status='active' WHERE id=?`, x.f.Workspace)
				}
				archiveQueue(t, x, v.ID)
			}
			// A newly constructed service instance, not a process restart.
			fresh := *x.s
			x.s = &fresh
			owned, e := x.s.ProcessNext(x.ctx)
			if e != nil || !owned {
				t.Fatal("fresh service publication recovery", e)
			}
			archiveReady(t, x, v.ID, 2)
			before := x.audits(t)
			refused, e := x.s.CancelImport(x.ctx, x.admin, v.ID, "Ready cancellation refusal", uuid.NewString())
			humanRefused(t, refused, e, "MODEL_IMPORT_STATE_CONFLICT")
			if x.audits(t) != before {
				t.Fatal("ready cancellation changed audit")
			}
			if e = modelartifact.VerifyDirectory(x.ctx, filepath.Join(x.root, "objects", v.ID)); e != nil {
				t.Fatal(e)
			}
			t.Log("Actual fixed approved model/files and real vendor transactions; no process-restart, wire, browser or GPU qualification.")
		})
	}
}
