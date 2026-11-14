//go:build repository_integration

package nodes_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func nodeJoinAttempt(t *testing.T, x nodeReadActual, ctx context.Context, operation, token string) error {
	t.Helper()
	if operation == "mint" {
		v, e := x.s.Mint(ctx, x.admin, x.f.Workspace, "Synthetic failure join", uuid.NewString())
		if e != nil && !reflect.DeepEqual(v, nodes.MintedToken{}) {
			t.Error("failed actual mint exposed private token/DTO")
		}
		return e
	}
	v, e := x.s.RevokeToken(ctx, x.admin, token, "Synthetic failure revoke", uuid.NewString())
	if e != nil && !reflect.DeepEqual(v, nodes.JoinToken{}) {
		t.Error("failed actual revoke exposed private DTO")
	}
	return e
}

func nodeJoinLongMySQLPool(t *testing.T, x nodeReadActual) {
	t.Helper()
	if x.f.Store.Dialect() != transaction.MySQL {
		return
	}
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil {
		t.Fatal(e)
	}
	c.Policy.LockSeconds, c.Policy.StatementSeconds = 5, 5
	c.Policy.ReadSeconds, c.Policy.TransactionSeconds = 5, 5
	kernel, observation, e := mysqlstore.Open(x.ctx, c)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("isolated actual longer deadline TLS pool", e)
	}
	t.Cleanup(func() {
		if e := kernel.Close(); e != nil {
			t.Error("isolated actual longer deadline pool cleanup", e)
		}
	})
	var seconds int
	if e = kernel.Pool().QueryRowContext(x.ctx, "SELECT @@session.innodb_lock_wait_timeout").Scan(&seconds); e != nil || seconds != 5 {
		t.Fatal("actual longer lock prerequisite missing", e)
	}
	x.owner.Storage, e = transaction.FromMySQL(kernel)
	if e != nil {
		t.Fatal(e)
	}
}

// The trigger and advisory marker exist only in this exclusive SQL fixture.
// The real driver executes every statement; no storage result is substituted.
func nodeJoinFaultTrigger(t *testing.T, x nodeReadActual, table, event, marker string, key int64) func() {
	t.Helper()
	const name = "node_join_synthetic_fault"
	pgBody := `RAISE EXCEPTION 'synthetic join write failure';`
	myBody := `SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic join write failure';`
	if marker != "" {
		pgBody = fmt.Sprintf("PERFORM pg_advisory_xact_lock(431,%d); PERFORM pg_sleep(2); RETURN NEW;", key)
		myBody = "DO GET_LOCK('" + marker + "',0); DO SLEEP(2); DO RELEASE_LOCK('" + marker + "');"
	}
	if x.f.Store.Dialect() == transaction.PostgreSQL {
		x.exec(t, "CREATE FUNCTION "+name+"() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN "+pgBody+" END$$", "")
	}
	x.exec(t, "CREATE TRIGGER "+name+" BEFORE "+event+" ON "+table+" FOR EACH ROW EXECUTE FUNCTION "+name+"()", "CREATE TRIGGER "+name+" BEFORE "+event+" ON "+table+" FOR EACH ROW BEGIN "+myBody+" END")
	dropped := false
	drop := func() {
		if dropped {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := x.f.Store.Exec(ctx, x.f.Statement("DROP TRIGGER "+name+" ON "+table, "DROP TRIGGER "+name)); e != nil {
			t.Fatal("owned actual failure trigger cleanup", e)
		}
		if x.f.Store.Dialect() == transaction.PostgreSQL {
			if _, e := x.f.Store.Exec(ctx, x.f.Statement("DROP FUNCTION "+name+"()", "")); e != nil {
				t.Fatal("owned actual trigger function cleanup", e)
			}
		}
		dropped = true
	}
	t.Cleanup(drop)
	return drop
}

func nodeJoinMarker(t *testing.T, x nodeReadActual, marker string, key int64) bool {
	t.Helper()
	var held bool
	e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND classid=431 AND objid=$1 AND granted)`, `SELECT IS_USED_LOCK(?) IS NOT NULL`, func() any {
		if x.f.Store.Dialect() == transaction.PostgreSQL {
			return key
		}
		return marker
	}())).Scan(&held)
	if e != nil {
		t.Fatal("actual SQL advisory observation", e)
	}
	return held
}

func TestActualNodeJoinSafety(t *testing.T) {
	for _, operation := range []string{"mint", "revoke"} {
		for _, kind := range []string{"token_row_failure", "audit_failure", "cancelled_caller", "shorter_caller_deadline", "owned_background_deadline", "actual_lock_timeout", "inflight_audit_cancellation"} {
			t.Run(operation+"_"+kind, func(t *testing.T) {
				x := newNodeReadActual(t)
				pending := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
				before := nodeReadStoredDigest(t, x)
				code := "STORAGE_UNAVAILABLE"
				ctx, cancel := context.WithCancel(x.ctx)
				defer cancel()
				var repair func()
				if kind == "token_row_failure" || kind == "audit_failure" {
					table, event := "node_join_tokens", "INSERT"
					if operation == "revoke" {
						event = "UPDATE"
					}
					if kind == "audit_failure" {
						table, event = "audit_events", "INSERT"
					}
					repair = nodeJoinFaultTrigger(t, x, table, event, "", 0)
				} else if kind == "cancelled_caller" {
					code = "STORAGE_CANCELLED"
					cancel()
				} else if kind == "inflight_audit_cancellation" {
					nodeJoinLongMySQLPool(t, x)
					marker := "projectone.join." + uuid.NewString()
					id := uuid.New()
					key := int64(binary.BigEndian.Uint32(id[:4]) & 0x3fffffff)
					repair = nodeJoinFaultTrigger(t, x, "audit_events", "INSERT", marker, key)
					done := make(chan error, 1)
					go func() { done <- nodeJoinAttempt(t, x, ctx, operation, pending.ID) }()
					observed := false
					until := time.Now().Add(3 * time.Second)
					for time.Now().Before(until) {
						if nodeJoinMarker(t, x, marker, key) {
							observed = true
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
					if !observed {
						t.Fatal("product never reached real inflight audit trigger")
					}
					select {
					case e := <-done:
						nodeReadWant(t, e, "STORAGE_CANCELLED")
					case <-time.After(5 * time.Second):
						t.Fatal("inflight real cancellation did not finish")
					}
					until = time.Now().Add(5 * time.Second)
					for nodeJoinMarker(t, x, marker, key) && time.Now().Before(until) {
						time.Sleep(20 * time.Millisecond)
					}
					if nodeJoinMarker(t, x, marker, key) {
						t.Fatal("owned actual advisory lock not released")
					}
				} else {
					repair = nodeReadHoldWorkspace(t, x, nil)
					code = "STORAGE_TIMEOUT"
					if kind == "shorter_caller_deadline" {
						cancel()
						ctx, cancel = context.WithTimeout(x.ctx, 80*time.Millisecond)
						defer cancel()
					} else if kind == "owned_background_deadline" {
						p := config.DefaultRuntimePolicy()
						p.HTTP.RequestSeconds, p.HTTP.ReadHeaderSeconds = 2, 1
						if e := p.Validate(); e != nil {
							t.Fatal(e)
						}
						x.owner.Config.Runtime = &p
						nodeJoinLongMySQLPool(t, x)
						ctx = context.Background()
					} else {
						code = "STORAGE_LOCK_TIMEOUT"
					}
				}
				if kind != "inflight_audit_cancellation" {
					started := time.Now()
					e := nodeJoinAttempt(t, x, ctx, operation, pending.ID)
					nodeReadWant(t, e, code)
					elapsed := time.Since(started)
					if strings.Contains(kind, "shorter_") && elapsed > time.Second || kind == "owned_background_deadline" && (elapsed < 1500*time.Millisecond || elapsed > 4*time.Second) {
						t.Fatal("actual join operation deadline mismatch", elapsed)
					}
				}
				if repair != nil {
					repair()
				}
				if nodeReadStoredDigest(t, x) != before {
					t.Fatal("failed real join write changed durable state/audit")
				}
				if e := nodeJoinAttempt(t, x, x.ctx, operation, pending.ID); e != nil {
					t.Fatal("explicit actual join write recovery failed", e)
				}
			})
		}
	}
}
