package scheduling

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func waitForSchedulingBlocker(t *testing.T, ctx context.Context, pool *pgxpool.Pool, waiting, holding uint32, result <-chan error) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT $2::integer=ANY(pg_blocking_pids($1::integer))", int64(waiting), int64(holding)).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-result:
			t.Fatalf("writer bypassed the tenant admission fence: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgresTenantAdmissionFenceWaitsForOwnerCompletion(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			f := newQueuedFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			first, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Rollback(ctx)
			q := transaction.PostgreSQLTransaction(first)
			changed, err := ReserveQueuedWithin(ctx, q, f.first, borrowingIntent(f.first, 2), f.domain, f.read)
			if err != nil || !changed {
				t.Fatalf("queued head: %v, %v", changed, err)
			}
			if err := dequeueQueuedFixture(ctx, q, f.first); err != nil {
				t.Fatal(err)
			}

			pids := make(chan uint32, 1)
			result := make(chan error, 1)
			go func() {
				result <- pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
					pids <- tx.Conn().PgConn().PID()
					changed, err := ReserveWithin(ctx, transaction.PostgreSQLTransaction(tx), f.second, borrowingIntent(f.second, 2))
					if err == nil && !changed {
						return errors.New("ordinary writer did not reserve")
					}
					return err
				})
			}()
			var pid uint32
			select {
			case pid = <-pids:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			waitForSchedulingBlocker(t, ctx, f.pool, pid, first.Conn().PgConn().PID(), result)
			// A separate tenant must remain able to acquire both fence modes
			// while this tenant's queued head and ordinary writer are blocked.
			otherCtx, otherCancel := context.WithTimeout(ctx, 2*time.Second)
			defer otherCancel()
			if err := pgx.BeginFunc(otherCtx, f.pool, func(tx pgx.Tx) error {
				q := transaction.PostgreSQLTransaction(tx)
				otherTenant := uuid.NewString()
				if err := lockReservationTenant(otherCtx, q, otherTenant, true); err != nil {
					return err
				}
				return lockReservationTenant(otherCtx, q, otherTenant, false)
			}); err != nil {
				t.Fatalf("unrelated tenant was blocked: %v", err)
			}
			if commit {
				err = first.Commit(ctx)
			} else {
				err = first.Rollback(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if commit {
				f.assertState(t, f.first, "preparing", 2, 2)
			} else {
				f.assertState(t, f.first, "queued", 1, 1)
			}
		})
	}
}

func TestPostgresTenantSharedFencesPermitIndependentWorkspaceWriters(t *testing.T) {
	f := newQueuedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if changed, err := ReserveWithin(ctx, transaction.PostgreSQLTransaction(first), f.first, borrowingIntent(f.first, 1)); err != nil || !changed {
		t.Fatalf("first ordinary hold: %v, %v", changed, err)
	}
	// The first transaction remains open. An exclusive tenant lock would block
	// the different-workspace writer until the bounded context expires.
	if changed, err := reserveInTransaction(ctx, f.store, f.second, borrowingIntent(f.second, 1)); err != nil || !changed {
		t.Fatalf("shared writer could not finish independently: %v, %v", changed, err)
	}
	if err := first.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, f.first, "queued", 1, 1)
}

func TestPostgresQueuedReservationBoundsLocksAtWorkspacePolicyMaximum(t *testing.T) {
	f := newQueuedFixture(t)
	policy := config.DefaultWorkspacePolicy()
	policy.MaximumCount = 10000
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	domain := append([]string(nil), f.domain...)
	for len(domain) < policy.MaximumCount {
		id := uuid.NewString()
		domain = append(domain, id)
		f.policy = append(f.policy, QuotaNode{ID: id, TenantID: f.first.TenantID, ParentID: f.policy[2].ID, Level: WorkspaceQuota})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx, "INSERT INTO workspaces SELECT $1::uuid,id::uuid FROM unnest($2::text[]) AS id ON CONFLICT DO NOTHING", f.first.TenantID, domain); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(ctx, tx, 1, f.policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := ownerTransaction(ctx, f.store, func(ctx context.Context, q transaction.Executor) error {
		changed, err := ReserveQueuedWithin(ctx, q, f.first, borrowingIntent(f.first, 2), domain, f.read)
		if err != nil {
			return err
		}
		if !changed {
			return errors.New("maximum-domain head was not reserved")
		}
		var tags int
		if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
SELECT count(DISTINCT (classid,objid,objsubid)) FROM pg_locks
 WHERE pid=pg_backend_pid() AND locktype='advisory' AND granted`)).Scan(&tags); err != nil {
			return err
		}
		if tags != 2 {
			t.Fatalf("advisory lock tags=%d for %d workspaces, want tenant plus selected workspace", tags, len(domain))
		}
		return dequeueQueuedFixture(ctx, q, f.first)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.assertState(t, f.first, "preparing", 2, 1)
}
