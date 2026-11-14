//go:build repository_integration

package transaction_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualWorkerSessionBoundary(t *testing.T) {
	for _, name := range []string{"identity_and_commit", "domain_rollback", "cancelled_transaction", "lock_cleanup", "panic_cleanup", "invalid_boundaries"} {
		t.Run(name, func(t *testing.T) {
			f := repositoryfixture.New(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			owner, ok := f.Store.(transaction.SessionStore)
			if !ok {
				t.Fatal("actual vendor store lacks physical session capability")
			}
			queryID := func(q transaction.Executor) int64 {
				t.Helper()
				var id int64
				if e := q.QueryRow(ctx, f.Statement(`SELECT pg_backend_pid()`, `SELECT CONNECTION_ID()`)).Scan(&id); e != nil || id <= 0 {
					t.Fatal("actual physical connection identity required", e)
				}
				return id
			}
			readName := func() string {
				t.Helper()
				var value string
				if e := f.Store.QueryRow(ctx, f.Statement(`SELECT display_name FROM users WHERE id=$1`, `SELECT display_name FROM users WHERE id=?`, f.User)).Scan(&value); e != nil {
					t.Fatal(e)
				}
				return value
			}
			before := readName()
			change := func(ctx context.Context, q transaction.Executor) error {
				_, e := q.Exec(ctx, f.Statement(`UPDATE users SET display_name='Synthetic session commit' WHERE id=$1`, `UPDATE users SET display_name='Synthetic session commit' WHERE id=?`, f.User))
				return e
			}
			key := "projectone-session-test:" + uuid.NewString()
			keyHash := sha256.Sum256([]byte(key))
			postgresKey := int64(binary.LittleEndian.Uint64(keyHash[:8]))
			acquire := func(q transaction.Executor) error {
				var acquired bool
				if e := q.QueryRow(ctx, f.Statement(`SELECT pg_try_advisory_lock($1)`, `SELECT GET_LOCK(?,0)`, func() any {
					if q.Dialect() == transaction.MySQL {
						return key
					}
					return postgresKey
				}())).Scan(&acquired); e != nil {
					return e
				}
				if !acquired {
					return errors.New("IDENTIFIED_TEST_SESSION_LOCK_REMAINS")
				}
				return nil
			}
			switch name {
			case "identity_and_commit":
				var physical int64
				var borrowed transaction.Store
				e := owner.WithSession(ctx, func(ctx context.Context, session transaction.Store) error {
					borrowed = session
					physical = queryID(session)
					if e := session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
						if queryID(q) != physical {
							t.Fatal("session transaction switched physical connections")
						}
						return change(ctx, q)
					}); e != nil {
						return e
					}
					if queryID(session) != physical {
						t.Fatal("physical session changed after commit")
					}
					return nil
				})
				if e != nil || readName() != "Synthetic session commit" || queryID(f.Store) == physical {
					t.Fatal("committed data or physical disposal failed", e)
				}
				var count int
				if e := borrowed.QueryRow(ctx, f.Statement(`SELECT 1`, `SELECT 1`)).Scan(&count); e == nil {
					t.Fatal("expired borrowed session remained usable")
				}
			case "domain_rollback":
				domain := errors.New("IDENTIFIED_TEST_DOMAIN_REFUSAL")
				e := owner.WithSession(ctx, func(ctx context.Context, session transaction.Store) error {
					return session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
						if e := change(ctx, q); e != nil {
							return e
						}
						return domain
					})
				})
				if e != domain || readName() != before {
					t.Fatal("session changed domain error or committed refused data", e)
				}
			case "cancelled_transaction":
				e := owner.WithSession(ctx, func(ctx context.Context, session transaction.Store) error {
					bounded, cancel := context.WithCancel(ctx)
					defer cancel()
					return session.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
						if e := change(ctx, q); e != nil {
							return e
						}
						cancel()
						return nil
					})
				})
				if !errors.Is(e, context.Canceled) || readName() != before {
					t.Fatal("cancelled session transaction committed or wrong safe error", e)
				}
			case "lock_cleanup", "panic_cleanup":
				if name == "lock_cleanup" {
					if e := owner.WithSession(ctx, func(_ context.Context, session transaction.Store) error { return acquire(session) }); e != nil {
						t.Fatal(e)
					}
				} else {
					panicked := false
					func() {
						defer func() { panicked = recover() == "IDENTIFIED_TEST_SESSION_PANIC" }()
						_ = owner.WithSession(ctx, func(_ context.Context, session transaction.Store) error {
							if e := acquire(session); e != nil {
								t.Fatal(e)
							}
							panic("IDENTIFIED_TEST_SESSION_PANIC")
						})
					}()
					if !panicked {
						t.Fatal("identified callback panic was hidden")
					}
				}
				if e := owner.WithSession(ctx, func(_ context.Context, session transaction.Store) error { return acquire(session) }); e != nil {
					t.Fatal("closed session retained its advisory lock", e)
				}
				if readName() != before {
					t.Fatal("session lock operation changed product data")
				}
			case "invalid_boundaries":
				calls := 0
				fn := func(context.Context, transaction.Store) error { calls++; return nil }
				if e := owner.WithSession(context.Background(), fn); e == nil || e.Error() != "REPOSITORY_TRANSACTION_DEADLINE_REQUIRED" {
					t.Fatal("missing session deadline accepted", e)
				}
				if e := owner.WithSession(nil, fn); e == nil || e.Error() != "REPOSITORY_SESSION_CONTEXT_REQUIRED" {
					t.Fatal("nil session context accepted", e)
				}
				if e := owner.WithSession(ctx, nil); e == nil || e.Error() != "REPOSITORY_SESSION_OPERATION_REQUIRED" {
					t.Fatal("nil session operation accepted", e)
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if e := owner.WithSession(cancelled, fn); !errors.Is(e, context.Canceled) || calls != 0 || readName() != before {
					t.Fatal("cancelled session entered callback or changed data", e)
				}
			}
		})
	}
}
