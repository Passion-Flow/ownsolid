//go:build knowledge_database_integration && knowledge_encoder_integration

package knowledge

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A separately named two-connection pool lets the test observe both callers
// actually waiting on the same PostgreSQL authority lock before releasing it.
func lockedPair(t *testing.T, ctx context.Context, s *Service, user string, actions [2]func(*Service) error) [2]error {
	t.Helper()
	c := s.DB.Config().Copy()
	c.MaxConns = 2
	application := "m12-pair-" + uuid.NewString()
	c.ConnConfig.RuntimeParams["application_name"] = application
	pool, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	isolated := *s
	isolated.DB = pool
	accounts := *s.Identity
	accounts.DB, accounts.Storage = pool, nil
	workspace := *s.Workspaces
	workspace.DB, workspace.Identity = pool, &accounts
	isolated.Identity, isolated.Workspaces = &accounts, &workspace
	block, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer block.Rollback(context.Background())
	if _, e = block.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", user); e != nil {
		t.Fatal(e)
	}
	type result struct {
		index int
		err   error
	}
	done := make(chan result, 2)
	for index, action := range actions {
		go func(index int, action func(*Service) error) { done <- result{index, action(&isolated)} }(index, action)
	}
	until := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND datname=current_database() AND wait_event_type='Lock'`, application).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("both actual PostgreSQL callers did not reach the controlled lock boundary", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = block.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	var out [2]error
	for range actions {
		select {
		case value := <-done:
			out[value.index] = value.err
		case <-ctx.Done():
			t.Fatal("actual concurrent operations did not finish")
		}
	}
	return out
}

func TestM12ActualConcurrentDuplicateAdmissionAndRevisionWritesSerialize(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w := a.Me.Workspace.ID
	base := createFacts(t, s, p, w)
	text := "Concurrent source remains one immutable version."
	input := UploadDocument{Name: "concurrent.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Controlled actual duplicate admission"}
	key := uuid.NewString()
	var documents [2]Document
	errors := lockedPair(t, ctx, s, p.Me.User.ID, [2]func(*Service) error{
		func(service *Service) error {
			var e error
			documents[0], e = service.Upload(ctx, p, w, base.ID, input, key, uuid.NewString())
			return e
		},
		func(service *Service) error {
			var e error
			documents[1], e = service.Upload(ctx, p, w, base.ID, input, key, uuid.NewString())
			return e
		},
	})
	if errors[0] != nil || errors[1] != nil || documents[0].ID == "" || documents[0].ID != documents[1].ID || documents[0].PendingSource.ID != documents[1].PendingSource.ID {
		t.Fatal("concurrent duplicate admission diverged", errors)
	}
	var sources, attempts, audits int
	if e := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM knowledge_source_versions WHERE document_id=$1),(SELECT count(*) FROM knowledge_attempts WHERE document_id=$1),(SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='knowledge.document_uploaded')`, documents[0].ID).Scan(&sources, &attempts, &audits); e != nil || sources != 1 || attempts != 1 || audits != 1 {
		t.Fatal("duplicate upload created repeated durable state", e, sources, attempts, audits)
	}
	var updated [2]Base
	errors = lockedPair(t, ctx, s, p.Me.User.ID, [2]func(*Service) error{
		func(service *Service) error {
			var e error
			updated[0], e = service.Update(ctx, p, w, base.ID, UpdateBase{Name: "Concurrent winner A", Enabled: true, ExpectedRevision: base.Revision, Reason: "Controlled revision A"}, uuid.NewString())
			return e
		},
		func(service *Service) error {
			var e error
			updated[1], e = service.Update(ctx, p, w, base.ID, UpdateBase{Name: "Concurrent winner B", Enabled: true, ExpectedRevision: base.Revision, Reason: "Controlled revision B"}, uuid.NewString())
			return e
		},
	})
	winner := -1
	for index, e := range errors {
		if e == nil {
			if winner != -1 {
				t.Fatal("both stale revision writes committed")
			}
			winner = index
		} else {
			requireCode(t, e, "KNOWLEDGE_REVISION_CONFLICT")
		}
	}
	if winner < 0 {
		t.Fatal("neither revision writer committed")
	}
	current, e := s.GetBase(ctx, p, w, base.ID)
	if e != nil || current.Revision != base.Revision+1 || current.Name != updated[winner].Name {
		t.Fatal("revision winner not durable", e)
	}
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='knowledge.base_updated'`, base.ID).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("revision failure leaked audit", e, audits)
	}
}

func TestM12ActualConcurrentExplicitRetryCreatesOneNewAttempt(t *testing.T) {
	s, a, p := serviceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	w := a.Me.Workspace.ID
	base := createFacts(t, s, p, w)
	text := strings.Repeat("x ", s.policy().SourceTokens+1)
	document, e := s.Upload(ctx, p, w, base.ID, UploadDocument{Name: "token-limit.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Actual CPU failure before concurrent retry"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	processOne(t, s)
	document, e = s.GetDocument(ctx, p, w, base.ID, document.ID)
	if e != nil || document.State != "failed" || document.CurrentAttempt.ErrorCode == nil || *document.CurrentAttempt.ErrorCode != "KNOWLEDGE_SOURCE_LIMIT_EXCEEDED" {
		t.Fatal("actual CPU token limit did not produce the required failed source", e)
	}
	key := uuid.NewString()
	input := RevisionReason{ExpectedRevision: document.Revision, Reason: "Controlled concurrent explicit new attempt"}
	var results [2]Document
	errors := lockedPair(t, ctx, s, p.Me.User.ID, [2]func(*Service) error{
		func(service *Service) error {
			var e error
			results[0], e = service.Retry(ctx, p, w, base.ID, document.ID, input, key, uuid.NewString())
			return e
		},
		func(service *Service) error {
			var e error
			results[1], e = service.Retry(ctx, p, w, base.ID, document.ID, input, key, uuid.NewString())
			return e
		},
	})
	if errors[0] != nil || errors[1] != nil || results[0].CurrentAttempt.ID != results[1].CurrentAttempt.ID || results[0].CurrentAttempt.AttemptNumber != 2 || results[0].Revision != document.Revision+1 {
		t.Fatal("concurrent explicit retry diverged", errors)
	}
	var attempts, sources, audits int
	if e = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM knowledge_attempts WHERE document_id=$1),(SELECT count(*) FROM knowledge_source_versions WHERE document_id=$1),(SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='knowledge.document_retried')`, document.ID).Scan(&attempts, &sources, &audits); e != nil || attempts != 2 || sources != 1 || audits != 1 {
		t.Fatal("concurrent retry duplicated attempts/sources/audit", e, attempts, sources, audits)
	}
}

// The worker locks mutable user authority without blocking independent FK/key-share
// readers. Parent identity is never changed here; M10 uses the same PostgreSQL rule.
func TestM12WorkerUserAuthorityLockPreservesForeignKeyCompatibility(t *testing.T) {
	s, a, _ := serviceFixture(t)
	base := createFacts(t, s, a, a.Me.Workspace.ID)
	_ = uploadFacts(t, s, a, a.Me.Workspace.ID, base.ID)
	c, e := s.nextCandidate(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	e = s.workerTransaction(t.Context(), c, func(ctx context.Context, q transaction.Executor, _ identity.Auth, _ workspaces.Workspace, state workerState) error {
		if !state.valid {
			t.Fatal("current delegated authority missing")
		}
		other, e := s.DB.Begin(ctx)
		if e != nil {
			return e
		}
		defer other.Rollback(ctx)
		var id string
		if e = other.QueryRow(ctx, "SELECT id::text FROM users WHERE id=$1 FOR KEY SHARE NOWAIT", a.Me.User.ID).Scan(&id); e != nil {
			return e
		}
		if id != a.Me.User.ID {
			t.Fatal("wrong locked user")
		}
		return nil
	})
	if e != nil {
		t.Fatal("worker blocked independent foreign-key authority", e)
	}
}
