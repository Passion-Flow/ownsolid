//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

type objectWorkerLoop struct {
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan error
	report        chan error
	joined        bool
	fatalExpected bool
	result        error
}

func (g *objectStory) startWorkerLoop(t *testing.T, fatalExpected bool) *objectWorkerLoop {
	t.Helper()
	ctx, cancel := context.WithTimeout(g.ctx, 20*time.Second)
	l := &objectWorkerLoop{ctx: ctx, cancel: cancel, done: make(chan error, 1), report: make(chan error, 1), fatalExpected: fatalExpected}
	go func() {
		l.done <- g.s.Run(ctx, func(e error) {
			select {
			case l.report <- e:
			default:
			}
			cancel()
		})
	}()
	t.Cleanup(func() { l.join(t) })
	return l
}

func (l *objectWorkerLoop) join(t *testing.T) {
	t.Helper()
	l.cancel()
	if l.joined {
		return
	}
	select {
	case err := <-l.done:
		l.joined = true
		l.result = err
		if err == nil || (!l.fatalExpected && !transaction.IsCancellationOnly(err)) {
			t.Error("worker did not return its bounded cancellation outcome", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned worker did not join before fixture cleanup")
	}
}

func (g *objectStory) workerContinuity(t *testing.T, leaf string, source []byte) {
	t.Helper()
	old, _, _ := g.create(t, source)
	g.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_worker_revoke' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_worker_revoke' WHERE id=?", `UPDATE "SESSIONS" SET "REVOKED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),"REVOKE_REASON"='synthetic_worker_revoke' WHERE "ID"=?`, g.portal.SessionID)
	var valid Object
	if leaf == "worker_loop_quarantines_revoked_session" {
		g.portal = g.session(t, g.f.User, "portal")
		valid, _, _ = g.create(t, source)
	} else {
		g.owner.Storage = &objectQuarantineOutcomeStore{Store: g.f.Store, object: old.ID,
			rollback: leaf == "worker_quarantine_rollback_remains_fatal", unknown: leaf == "worker_quarantine_unknown_remains_fatal"}
	}
	loop := g.startWorkerLoop(t, leaf != "worker_loop_quarantines_revoked_session")
	// Registered after the join cleanup so the loop never races a storage swap.
	defer func() { loop.join(t); g.owner.Storage = g.f.Store }()
	if leaf == "worker_loop_quarantines_revoked_session" {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
	wait:
		for {
			select {
			case err := <-loop.report:
				t.Fatal("durably quarantined job stopped the control service", err)
			case <-loop.ctx.Done():
				t.Fatal("valid independent job made no bounded progress")
			case <-ticker.C:
				value, err := g.s.Get(g.ctx, g.portal, g.f.Workspace, valid.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				if value.State == "uploading" {
					break wait
				}
			}
		}
		loop.join(t)
		select {
		case err := <-loop.report:
			t.Fatal("normal worker cancellation invoked fatal callback", err)
		default:
		}
	} else {
		var reported error
		select {
		case reported = <-loop.report:
		case <-loop.ctx.Done():
			select {
			case reported = <-loop.report:
			default:
				t.Fatal("failed annotation did not invoke the fatal callback")
			}
		}
		if reported == nil || workerJobQuarantined(reported) ||
			(leaf == "worker_quarantine_unknown_remains_fatal" && !containsOutcome(reported, transaction.CommitUnknown, 0)) {
			t.Fatal("failed annotation was treated as durably quarantined", reported)
		}
		loop.join(t)
		if loop.result == nil || safe(loop.result).Error() != safe(reported).Error() {
			t.Fatal("shutdown discarded the fatal worker outcome", loop.result)
		}
		g.owner.Storage = g.f.Store
	}
	var phase string
	var code *string
	if err := g.f.Store.QueryRow(g.ctx, statement(g.f.Store, "SELECT operation_phase,error_code FROM artifact_objects WHERE id=$1", "SELECT operation_phase,error_code FROM artifact_objects WHERE id=?", `SELECT "OPERATION_PHASE","ERROR_CODE" FROM "ARTIFACT_OBJECTS" WHERE "ID"=?`, old.ID)).Scan(&phase, &code); err != nil || phase != "prepared" {
		t.Fatal("failed job lost its exact unissued preparation", err)
	}
	if leaf == "worker_quarantine_rollback_remains_fatal" {
		if code != nil {
			t.Fatal("rolled-back annotation became durable")
		}
	} else if code == nil || *code != "AUTH_SESSION_EXPIRED" {
		t.Fatal("exact committed job annotation missing")
	}
	uploads, err := g.s.Transport.FindUploads(g.ctx, objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: old.ID})
	if err != nil || len(uploads) != 0 {
		t.Fatal("revoked job reached external create", err)
	}
	if valid.ID != "" {
		uploads, err = g.s.Transport.FindUploads(g.ctx, objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: valid.ID})
		if err != nil || len(uploads) != 1 {
			t.Fatal("independent valid job did not create exactly one upload", err)
		}
	}
}

// Explicit SQL outcome fixture: only a real error-code transition for this exact
// object triggers a rollback/unknown acknowledgment; no SQL strings are inspected.
type objectQuarantineOutcomeStore struct {
	transaction.Store
	object            string
	rollback, unknown bool
}

func (s *objectQuarantineOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	changed := false
	err := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		read := func() (*string, error) {
			var code *string
			e := q.QueryRow(ctx, statement(q, "SELECT error_code FROM artifact_objects WHERE id=$1",
				"SELECT error_code FROM artifact_objects WHERE id=?", `SELECT "ERROR_CODE" FROM "ARTIFACT_OBJECTS" WHERE "ID"=?`, s.object)).Scan(&code)
			return code, e
		}
		before, err := read()
		if err != nil {
			return err
		}
		if err = fn(ctx, q); err != nil {
			return err
		}
		after, err := read()
		if err != nil {
			return err
		}
		changed = !ro && before == nil && after != nil && *after == "AUTH_SESSION_EXPIRED"
		if changed && s.rollback {
			return errors.New("EXPLICIT_QUARANTINE_ANNOTATION_ROLLBACK")
		}
		return nil
	})
	if err == nil && changed && s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return err
}
