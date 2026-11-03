//go:build repository_integration

package nodes_test

import (
	"context"
	"encoding/binary"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func nodeEnrollmentAttempt(t *testing.T, x nodeReadActual, ctx context.Context, token string) error {
	t.Helper()
	value, e := x.s.Enroll(ctx, wire.Enrollment{JoinToken: token, Handshake: nodeEnrollmentInput(wire.ReadOnlyProfile)}, uuid.NewString())
	if e != nil && !reflect.DeepEqual(value, wire.Enrolled{}) {
		t.Error("failed actual enrollment exposed one-time credential")
	}
	return e
}

func TestActualNodeEnrollmentSafety(t *testing.T) {
	for _, kind := range []string{"node_row_failure", "token_row_failure", "audit_failure", "cancelled_caller",
		"shorter_caller_deadline", "owned_background_deadline", "actual_lock_timeout", "inflight_audit_cancellation"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			pending := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic failure enrollment", wire.ReadOnlyProfile)
			before := nodeReadStoredDigest(t, x)
			ctx, cancel := context.WithCancel(x.ctx)
			defer cancel()
			code := "STORAGE_UNAVAILABLE"
			var repair func()
			switch kind {
			case "node_row_failure", "token_row_failure", "audit_failure":
				table, event := "managed_nodes", "INSERT"
				if kind == "token_row_failure" {
					table, event = "node_join_tokens", "UPDATE"
				}
				if kind == "audit_failure" {
					table, event = "audit_events", "INSERT"
				}
				repair = nodeJoinFaultTrigger(t, x, table, event, "", 0)
			case "cancelled_caller":
				code = "STORAGE_CANCELLED"
				cancel()
			case "inflight_audit_cancellation":
				nodeJoinLongMySQLPool(t, x)
				marker := "projectone.enroll." + uuid.NewString()
				id := uuid.New()
				key := int64(binary.BigEndian.Uint32(id[:4]) & 0x3fffffff)
				repair = nodeJoinFaultTrigger(t, x, "audit_events", "INSERT", marker, key)
				done := make(chan error, 1)
				go func() { done <- nodeEnrollmentAttempt(t, x, ctx, pending.Token) }()
				observed, until := false, time.Now().Add(3*time.Second)
				for time.Now().Before(until) {
					if nodeJoinMarker(t, x, marker, key) {
						observed = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
				if !observed {
					t.Fatal("enrollment never reached actual inflight audit trigger")
				}
				select {
				case e := <-done:
					nodeReadWant(t, e, "STORAGE_CANCELLED")
				case <-time.After(5 * time.Second):
					t.Fatal("actual enrollment cancellation did not finish")
				}
				until = time.Now().Add(5 * time.Second)
				for nodeJoinMarker(t, x, marker, key) && time.Now().Before(until) {
					time.Sleep(20 * time.Millisecond)
				}
				if nodeJoinMarker(t, x, marker, key) {
					t.Fatal("actual enrollment advisory marker remained held")
				}
			default:
				repair = nodeReadHoldWorkspace(t, x, nil)
				code = "STORAGE_TIMEOUT"
				if kind == "shorter_caller_deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(x.ctx, 80*time.Millisecond)
					defer cancel()
				} else if kind == "owned_background_deadline" {
					policy := config.DefaultRuntimePolicy()
					policy.HTTP.RequestSeconds, policy.HTTP.ReadHeaderSeconds = 2, 1
					if e := policy.Validate(); e != nil {
						t.Fatal(e)
					}
					x.owner.Config.Runtime = &policy
					nodeJoinLongMySQLPool(t, x)
					ctx = context.Background()
				} else {
					code = "STORAGE_LOCK_TIMEOUT"
				}
			}
			if kind != "inflight_audit_cancellation" {
				started := time.Now()
				e := nodeEnrollmentAttempt(t, x, ctx, pending.Token)
				nodeReadWant(t, e, code)
				elapsed := time.Since(started)
				if kind == "shorter_caller_deadline" && elapsed > time.Second || kind == "owned_background_deadline" && (elapsed < 1500*time.Millisecond || elapsed > 4*time.Second) {
					t.Fatal("actual enrollment deadline did not bound the operation", elapsed)
				}
			}
			if repair != nil {
				repair()
			}
			if nodeReadStoredDigest(t, x) != before {
				t.Fatal("failed enrollment changed nodes/token/audit persistence")
			}
			if e := nodeEnrollmentAttempt(t, x, x.ctx, pending.Token); e != nil {
				t.Fatal("explicit enrollment after confirmed rollback did not recover", e)
			}
		})
	}
}

// The observer forwards real SQL and records execution count only. It neither
// substitutes a row nor changes the actual vendor transaction/COMMIT outcome.
type nodeEnrollmentTokenWaitStore struct {
	transaction.Store
	attempt    chan struct{}
	once       sync.Once
	executions atomic.Int32
}
type nodeEnrollmentTokenWaitExecutor struct {
	transaction.Executor
	owner *nodeEnrollmentTokenWaitStore
}
type nodeEnrollmentTokenWaitRow struct {
	ctx   context.Context
	q     transaction.Executor
	sql   transaction.Statement
	owner *nodeEnrollmentTokenWaitStore
}

func (s *nodeEnrollmentTokenWaitStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, nodeEnrollmentTokenWaitExecutor{q, s})
	})
}
func (q nodeEnrollmentTokenWaitExecutor) Exec(ctx context.Context, sql transaction.Statement) (int64, error) {
	q.owner.executions.Add(1)
	return q.Executor.Exec(ctx, sql)
}
func (q nodeEnrollmentTokenWaitExecutor) QueryRow(ctx context.Context, sql transaction.Statement) transaction.Row {
	return nodeEnrollmentTokenWaitRow{ctx, q.Executor, sql, q.owner}
}
func (r nodeEnrollmentTokenWaitRow) Scan(dest ...any) error {
	if len(dest) == 3 {
		r.owner.once.Do(func() { close(r.owner.attempt) })
	}
	return r.q.QueryRow(r.ctx, r.sql).Scan(dest...)
}

func TestActualNodeEnrollmentExpiryAfterTokenWait(t *testing.T) {
	x := newNodeReadActual(t)
	pending := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic wait-expiring enrollment", wire.ReadOnlyProfile)
	before := nodeReadStoredDigest(t, x)
	nodeJoinLongMySQLPool(t, x)
	base, e := x.owner.Repository()
	if e != nil {
		t.Fatal(e)
	}
	observer := &nodeEnrollmentTokenWaitStore{Store: base, attempt: make(chan struct{})}
	x.owner.Storage, x.owner.DB = observer, nil
	x.s.DB, x.s.Workspaces.DB = nil, nil
	ctx, cancel := context.WithTimeout(x.ctx, 8*time.Second)
	defer cancel()
	ready, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var held error
	go func() {
		held = x.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, x.f.Statement(`SELECT id::text FROM node_join_tokens WHERE id=$1 FOR UPDATE`, `SELECT id FROM node_join_tokens WHERE id=? FOR UPDATE`, pending.ID)).Scan(&id); e != nil {
				return e
			}
			if _, e := q.Exec(ctx, x.f.Statement(`UPDATE node_join_tokens SET expires_at=clock_timestamp()+interval '1 second' WHERE id=$1`, `UPDATE node_join_tokens SET expires_at=TIMESTAMPADD(SECOND,1,UTC_TIMESTAMP(6)) WHERE id=?`, pending.ID)); e != nil {
				return e
			}
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		close(finished)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(release)
			<-finished
			if held != nil {
				t.Error("actual token wait holder cleanup", held)
			}
		})
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-finished:
		t.Fatal("actual token expiry holder failed", held)
	case <-ctx.Done():
		t.Fatal("actual token holder admission timed out")
	}
	done := make(chan error, 1)
	go func() { done <- nodeEnrollmentAttempt(t, x, x.ctx, pending.Token) }()
	select {
	case <-observer.attempt:
	case e := <-done:
		t.Fatal("enrollment did not reach the real token fence", e)
	case <-ctx.Done():
		t.Fatal("actual token fence admission timed out")
	}
	select {
	case e := <-done:
		t.Fatal("enrollment escaped held token fence", e)
	case <-time.After(1300 * time.Millisecond):
	}
	stop()
	select {
	case e := <-done:
		nodeReadWant(t, e, "TOKEN_UNUSABLE")
	case <-ctx.Done():
		t.Fatal("expired waiting enrollment did not finish")
	}
	if x.f.Store.Dialect() == transaction.MySQL && observer.executions.Load() != 2 {
		t.Fatal("MySQL fixture did not exercise insert then conditional-expiry rollback")
	}
	var expired, consumed, revoked bool
	var node *string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT expires_at<=clock_timestamp(),consumed_at IS NOT NULL,revoked_at IS NOT NULL,node_id::text FROM node_join_tokens WHERE id=$1`,
		`SELECT expires_at<=UTC_TIMESTAMP(6),consumed_at IS NOT NULL,revoked_at IS NOT NULL,node_id FROM node_join_tokens WHERE id=?`, pending.ID)).Scan(&expired, &consumed, &revoked, &node); e != nil || !expired || consumed || revoked || node != nil {
		t.Fatal("actual expired wait consumed or revoked a token", e)
	}
	// Restore only the synthetic timestamp to compare the full pre-attempt digest,
	// then expire it again. This is a test snapshot check, not product reactivation.
	x.exec(t, `UPDATE node_join_tokens SET expires_at=$1 WHERE id=$2`, `UPDATE node_join_tokens SET expires_at=? WHERE id=?`, pending.ExpiresAt, pending.ID)
	if nodeReadStoredDigest(t, x) != before {
		t.Fatal("expired conditional consumption left node/token/audit changes")
	}
	x.exec(t, `UPDATE node_join_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, `UPDATE node_join_tokens SET expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, pending.ID)
	fresh := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic explicit fresh enrollment after expiry", wire.ReadOnlyProfile)
	if e := nodeEnrollmentAttempt(t, x, x.ctx, fresh.Token); e != nil {
		t.Fatal("fresh enrollment after confirmed expiry did not recover", e)
	}
}
