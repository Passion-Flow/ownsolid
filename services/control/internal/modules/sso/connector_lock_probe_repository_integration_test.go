//go:build repository_integration

package sso

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This isolated SQL diagnostic compares real InnoDB foreign-key index locks.
// It never modifies product tables or qualifies a migration/connector fix.
func TestActualSSOConnectorMySQLRevocationForeignKeyIndexProbe(t *testing.T) {
	f := catalogRepositoryFixture(t)
	if f.f.Store.Dialect() != transaction.MySQL {
		t.Fatal("explicit actual MySQL diagnostic required")
	}
	for _, order := range []string{"live_index_only", "dedicated_added_last", "dedicated_before_live"} {
		t.Run(order, func(t *testing.T) {
			table := "sso_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			f.exec(t, transaction.SQL(transaction.MySQL, `CREATE TABLE `+table+`(id INTEGER PRIMARY KEY,owner_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,value INTEGER NOT NULL,KEY owner_live(owner_id,value),FOREIGN KEY(owner_id) REFERENCES users(id)) ENGINE=InnoDB`))
			t.Cleanup(func() {
				if _, e := f.f.Store.Exec(f.ctx, transaction.SQL(transaction.MySQL, `DROP TABLE `+table)); e != nil {
					t.Error("exclusive diagnostic table cleanup", e)
				}
			})
			f.exec(t, transaction.SQL(transaction.MySQL, `INSERT INTO `+table+`(id,owner_id,value) VALUES(1,?,0)`, f.f.User))
			if order == "dedicated_added_last" {
				f.exec(t, transaction.SQL(transaction.MySQL, `ALTER TABLE `+table+` ADD KEY owner_fk(owner_id),ALGORITHM=INPLACE,LOCK=NONE`))
			}
			if order == "dedicated_before_live" {
				// A single DROP/ADD of the same name can retain the original
				// physical index. Separate DDL establishes the FK-only predecessor.
				f.exec(t, transaction.SQL(transaction.MySQL, `ALTER TABLE `+table+` ADD KEY owner_fk(owner_id),DROP INDEX owner_live,ALGORITHM=INPLACE,LOCK=NONE`))
				f.exec(t, transaction.SQL(transaction.MySQL, `ALTER TABLE `+table+` ADD KEY owner_live(owner_id,value),ALGORITHM=INPLACE,LOCK=NONE`))
			}
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			holder, stop := context.WithTimeout(f.ctx, 4*time.Second)
			defer stop()
			defer close(release)
			go func() {
				done <- f.f.Store.Within(holder, false, func(ctx context.Context, q transaction.Executor) error {
					var id string
					if e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT id FROM users WHERE id=? FOR UPDATE`, f.f.User)).Scan(&id); e != nil {
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
			}()
			select {
			case <-ready:
			case e := <-done:
				t.Fatal("diagnostic parent holder", e)
			case <-time.After(time.Second):
				t.Fatal("diagnostic parent holder deadline")
			}
			ctx, cancel := context.WithTimeout(f.ctx, 350*time.Millisecond)
			e := f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				_, e := q.Exec(ctx, transaction.SQL(transaction.MySQL, `UPDATE `+table+` SET value=1 WHERE id=1`))
				return e
			})
			cancel()
			if order == "live_index_only" {
				if e == nil || !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal("mutable composite FK-index parent-lock hypothesis not established", e)
				}
			} else if order == "dedicated_before_live" && e != nil {
				t.Fatal("stable FK-only index must avoid unchanged owner parent wait", e)
			}
			stop()
			select {
			case e := <-done:
				if e != nil && !errors.Is(e, context.Canceled) {
					t.Fatal("diagnostic holder cleanup", e)
				}
			case <-time.After(time.Second):
				t.Fatal("diagnostic holder cleanup deadline")
			}
			t.Logf("actual isolated index order=%s non-FK update waited_on_locked_owner=%t", order, e != nil)
		})
	}
}
