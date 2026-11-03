//go:build cgo && odbc_manager && dameng_session_integration

package transaction_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Opt-in target test: config is a protected absolute file reference, never a
// credential literal. It creates no table/data, but explicitly takes session
// locks and read-only/read-write transactions on the admitted native target.
func TestActualDamengPinnedSession(t *testing.T) {
	c, e := dameng.LoadConnectionConfiguration(os.Getenv("PROJECTONE_DAMENG_SESSION_CONFIG"), "")
	if e != nil {
		t.Fatal("protected native session test configuration required")
	}
	if c.Policy.MaximumOpenConnections < 2 || c.Policy.MaximumPhysicalConnections < 2 {
		t.Fatal("native session test requires two admitted connections")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(c.TransactionSeconds*4)*time.Second)
	defer cancel()
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		defer func() {
			clean, stop := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer stop()
			s, e := kernel.Close(clean)
			if e != nil || s.State != "closed" || s.PhysicalOwners != 0 || s.CleanupJobs != 0 {
				t.Error("native cleanup remains pending; retain owner and target evidence")
			}
		}()
	}
	if e != nil {
		t.Fatal("native target unavailable")
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil {
		t.Fatal(e)
	}
	owner, ok := store.(transaction.SessionStore)
	if !ok {
		t.Fatal("pinned capability missing")
	}
	query := func(ctx context.Context, q transaction.Executor) error {
		var value int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT 1 FROM DUAL")).Scan(&value); e != nil {
			return e
		}
		if value != 1 {
			return errors.New("session read mismatch")
		}
		return nil
	}
	var entropy [4]byte
	if _, e = rand.Read(entropy[:]); e != nil {
		t.Fatal(e)
	}
	key := int64(binary.BigEndian.Uint32(entropy[:]) & 0x3fffffff)
	acquire := func(ctx context.Context, q transaction.Executor) (int, error) {
		var code int
		e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT DBMS_LOCK.REQUEST(?,6,0,FALSE) FROM DUAL", key)).Scan(&code)
		return code, e
	}
	var escaped transaction.Store
	e = owner.WithSession(ctx, func(ctx context.Context, session transaction.Store) error {
		escaped = session
		initial := kernel.Status()
		if initial.PhysicalOwners != 1 || len(initial.Connections) != 1 {
			return errors.New("session physical owner missing")
		}
		id := initial.Connections[0].OwnerID
		if code, e := acquire(ctx, session); e != nil || code != 0 {
			return errors.New("session lock unavailable")
		}
		for _, readOnly := range []bool{true, false, true, false} {
			if e := session.Within(ctx, readOnly, query); e != nil {
				return e
			}
			if e := query(ctx, session); e != nil {
				return e
			}
			s := kernel.Status()
			if s.PhysicalOwners != 1 || len(s.Connections) != 1 || s.Connections[0].OwnerID != id {
				return errors.New("physical session changed across commit")
			}
		}
		refusal := errors.New("intentional domain rollback")
		if e := session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			if e := query(ctx, q); e != nil {
				return e
			}
			return refusal
		}); e != refusal {
			return errors.New("domain rollback outcome changed")
		}
		if e := query(ctx, session); e != nil {
			return e
		}
		return owner.WithSession(ctx, func(ctx context.Context, competitor transaction.Store) error {
			code, e := acquire(ctx, competitor)
			if e != nil || code != 1 {
				return errors.New("lock did not survive transaction completion")
			}
			return nil
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = query(ctx, escaped); e == nil {
		t.Fatal("expired session remains usable")
	}
	if e = owner.WithSession(ctx, func(ctx context.Context, s transaction.Store) error {
		code, e := acquire(ctx, s)
		if e != nil || code != 0 {
			return errors.New("disposed physical session retained lock")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
