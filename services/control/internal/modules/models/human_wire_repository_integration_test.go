//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

type humanWireCommits struct {
	proxy    modelCommitProxy
	mode     commitfault.Mode
	armAfter int32
	complete atomic.Int32
}
type humanWireStore struct {
	transaction.Store
	commits *humanWireCommits
}

// Only arm the real protocol fault after actual preceding commits succeed.
// Ordinary and session-bound transactions share one count; no SQL result or
// transaction outcome is supplied by this test scheduling wrapper.
func (s *humanWireStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, ro, fn); e != nil {
		return e
	}
	completed := s.commits.complete.Add(1)
	if s.commits.mode != commitfault.Pass && completed == s.commits.armAfter {
		return s.commits.proxy.Arm(s.commits.mode)
	}
	return nil
}
func (s *humanWireStore) WithSession(ctx context.Context, fn func(context.Context, transaction.Store) error) error {
	return s.Store.(transaction.SessionStore).WithSession(ctx, func(ctx context.Context, q transaction.Store) error {
		return fn(ctx, &humanWireStore{Store: q, commits: s.commits})
	})
}

func humanWireChunk(t *testing.T, x humanImportFixture, id string, recorded int, exists bool) {
	t.Helper()
	var n int
	var total int64
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM model_import_chunks WHERE import_id=$1`, `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM model_import_chunks WHERE import_id=?`, id)).Scan(&n, &total); e != nil || n != recorded || total != int64(recorded*100) {
		t.Fatal("actual wire durable chunk metadata", e, n, total)
	}
	path := filepath.Join(x.root, "imports", id, "chunk-000000000000")
	raw, e := os.ReadFile(path)
	if !exists {
		if !os.IsNotExist(e) {
			t.Fatal("cancelled upload bytes remain", e)
		}
		return
	}
	payload, _ := humanPayload()
	if e != nil || !bytes.Equal(raw, payload) {
		t.Fatal("recoverable actual upload bytes changed", e)
	}
}

func humanWireState(t *testing.T, x humanImportFixture, id, state string, uploaded int64, pending bool) {
	t.Helper()
	v, e := x.s.GetImport(x.ctx, x.admin, id)
	if e != nil || v.State != state || v.UploadedBytes != uploaded || v.Attempts != 0 || v.ModelID != nil ||
		pending && (v.ErrorCode == nil || *v.ErrorCode != cancellationPending) || !pending && v.ErrorCode != nil {
		t.Fatal("actual independent human wire state", e, v.State, v.UploadedBytes, v.Attempts)
	}
}

func TestActualModelHumanImportWire(t *testing.T) {
	for _, name := range []string{
		"intent_normal", "intent_not_forwarded", "intent_acknowledgement_lost",
		"chunk_normal", "chunk_not_forwarded", "chunk_acknowledgement_lost",
		"queue_normal", "queue_not_forwarded", "queue_acknowledgement_lost",
		"cancellation_normal", "cancellation_intent_not_forwarded", "cancellation_intent_acknowledgement_lost",
		"cancellation_final_not_forwarded", "cancellation_final_acknowledgement_lost",
	} {
		t.Run(name, func(t *testing.T) {
			x := humanSetup(t)
			action := strings.Split(name, "_")[0]
			payload, digest := humanPayload()
			var initial models.Import
			if action != "intent" {
				initial = x.small(t)
				if action != "chunk" {
					if _, e := x.s.UploadChunk(x.ctx, x.admin, initial.ID, 0, int64(len(payload)), digest, bytes.NewReader(payload)); e != nil {
						t.Fatal("explicit synthetic complete upload precondition", e)
					}
				}
				if action == "cancellation" {
					archiveQueue(t, x, initial.ID)
				}
			}
			sentinel := filepath.Join(x.root, "unrelated-recovery-sentinel")
			if e := os.WriteFile(sentinel, []byte("PRESERVE_UNRELATED_SYNTHETIC_RECOVERY_DATA"), 0600); e != nil {
				t.Fatal(e)
			}
			store, proxy, shutdown := modelWireConnection(t, x)
			mode := commitfault.Pass
			if strings.HasSuffix(name, "not_forwarded") {
				mode = commitfault.DropBeforeCommit
			} else if strings.HasSuffix(name, "acknowledgement_lost") {
				mode = commitfault.DropCommitAcknowledgement
			}
			targetCommit := int32(2)
			if action == "intent" {
				targetCommit = 1
			} else if name == "cancellation_normal" || strings.HasPrefix(name, "cancellation_final_") {
				targetCommit = 3
			}
			commits := &humanWireCommits{proxy: proxy, mode: mode, armAfter: targetCommit - 1}
			owner := *x.owner
			owner.Storage = &humanWireStore{Store: store, commits: commits}
			service := *x.s
			service.Identity = &owner
			workspaceService := *x.s.Workspaces
			workspaceService.Identity = &owner
			service.Workspaces = &workspaceService
			if mode != commitfault.Pass && commits.armAfter == 0 {
				if e := proxy.Arm(mode); e != nil {
					t.Fatal(e)
				}
			}
			var out models.Import
			var e error
			switch action {
			case "intent":
				out, e = service.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
			case "chunk":
				out, e = service.UploadChunk(x.ctx, x.admin, initial.ID, 0, int64(len(payload)), digest, bytes.NewReader(payload))
			case "queue":
				out, e = service.QueueImport(x.ctx, x.admin, initial.ID, "Actual isolated queue wire evidence", uuid.NewString())
			case "cancellation":
				out, e = service.CancelImport(x.ctx, x.admin, initial.ID, "Actual isolated cancellation wire evidence", uuid.NewString())
			default:
				t.Fatal("unrecognized human wire action")
			}
			if mode == commitfault.Pass {
				if e != nil || out.ID == "" {
					t.Fatal("normal actual human wire forwarding", e)
				}
			} else {
				humanRefused(t, out, e, string(transaction.CommitUnknown))
				select {
				case observed := <-proxy.Outcomes():
					committed := mode == commitfault.DropCommitAcknowledgement
					if observed.Mode != mode || !observed.Suppressed || observed.Forwarded != committed || observed.ServerOK != committed {
						t.Fatal("actual human mutation COMMIT fault mismatched", observed)
					}
				case <-x.ctx.Done():
					t.Fatal("actual human wire observation missing")
				}
			}
			if proxy.Commits() != targetCommit {
				t.Fatal("wrong actual mutation target or automatic COMMIT replay", proxy.Commits(), targetCommit)
			}
			shutdown()
			committed := mode != commitfault.DropBeforeCommit
			resolvedID := ""
			for range 2 {
				switch action {
				case "intent":
					resolution, e := x.s.ResolveImport(x.ctx, x.admin, x.f.Workspace, x.key)
					if e != nil {
						t.Fatal("actual durable intent resolution", e)
					}
					if committed {
						if resolution.Status != models.ImportResolutionFound || resolution.Import == nil || resolution.Import.State != "uploading" || resolution.Import.ExpectedBytes != x.input.ExpectedBytes || resolvedID != "" && resolvedID != resolution.Import.ID {
							t.Fatal("exact durable intent absent or changed")
						}
						resolvedID = resolution.Import.ID
						if x.count(t, "model.import_started", resolvedID) != 1 {
							t.Fatal("intent/audit was not atomic")
						}
					} else if resolution.Status != models.ImportResolutionAbsent || resolution.Import != nil {
						t.Fatal("unforwarded intent reported committed")
					}
				case "chunk":
					recorded := 0
					if committed {
						recorded = 1
					}
					humanWireState(t, x, initial.ID, "uploading", int64(recorded*100), false)
					humanWireChunk(t, x, initial.ID, recorded, true)
				case "queue":
					state, audits := "uploading", 0
					if committed {
						state, audits = "queued", 1
					}
					humanWireState(t, x, initial.ID, state, 100, false)
					if x.count(t, "model.import_queued", initial.ID) != audits {
						t.Fatal("queue/audit was not atomic")
					}
					humanWireChunk(t, x, initial.ID, 1, true)
				case "cancellation":
					intentCommitted := name == "cancellation_normal" || targetCommit == 3 || committed
					finalCommitted := name == "cancellation_normal" || targetCommit == 3 && committed
					state := "queued"
					if finalCommitted {
						state = "cancelled"
					}
					humanWireState(t, x, initial.ID, state, 100, intentCommitted && !finalCommitted)
					humanWireChunk(t, x, initial.ID, 1, targetCommit == 2)
					requestAudits, finalAudits := 0, 0
					if intentCommitted {
						requestAudits = 1
					}
					if finalCommitted {
						finalAudits = 1
					}
					if x.count(t, "model.import_cancellation_requested", initial.ID) != requestAudits || x.count(t, "model.import_cancelled", initial.ID) != finalAudits {
						t.Fatal("cancellation durable phase/audit mismatch")
					}
				}
			}
			switch action {
			case "intent":
				recovered, e := x.s.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
				if e != nil || recovered.ID == "" || resolvedID != "" && recovered.ID != resolvedID || x.count(t, "model.import_started", recovered.ID) != 1 || mode == commitfault.Pass && recovered.ID != out.ID {
					t.Fatal("explicit original-key intent recovery", e)
				}
			case "chunk":
				recovered, e := x.s.UploadChunk(x.ctx, x.admin, initial.ID, 0, int64(len(payload)), digest, bytes.NewReader(payload))
				if e != nil || recovered.UploadedBytes != 100 {
					t.Fatal("explicit same-digest chunk reconciliation", e)
				}
				humanWireChunk(t, x, initial.ID, 1, true)
			case "queue":
				archiveQueue(t, x, initial.ID)
				if x.count(t, "model.import_queued", initial.ID) != 1 {
					t.Fatal("explicit queue recovery duplicated audit")
				}
			case "cancellation":
				recovered, e := x.s.CancelImport(x.ctx, x.admin, initial.ID, "Explicit durable cancellation resolution", uuid.NewString())
				if e != nil || recovered.State != "cancelled" || recovered.ErrorCode != nil || x.count(t, "model.import_cancellation_requested", initial.ID) != 1 || x.count(t, "model.import_cancelled", initial.ID) != 1 {
					t.Fatal("explicit durable cancellation recovery", e)
				}
				humanWireChunk(t, x, initial.ID, 1, false)
			}
			if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "PRESERVE_UNRELATED_SYNTHETIC_RECOVERY_DATA" {
				t.Fatal("wire recovery deleted unrelated data", e)
			}
			t.Logf("Actual human wire: mode=%d mutation_COMMIT=%d direct_checks=2; explicit recovery only; small byte fixtures are not an approved archive", mode, targetCommit)
		})
	}
}
