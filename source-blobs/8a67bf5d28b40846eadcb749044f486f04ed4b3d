package identity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// A behavioral lock control, not a SQL/source-text assertion. The executor owns
// one session lock and the SourceGuard owns one connector lock. The revoker takes
// connector -> session, exactly the dependency that an ordinary recheck shares.
// The explicit old-order control must time out under the identical schedule.
func TestSessionRecheckLockOrderAgainstConnectorRevocation(t *testing.T) {
	for _, entry := range []string{"current", "compatibility", "old_order_control"} {
		t.Run(entry, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			f := &recheckLockFixture{session: make(chan struct{}, 1), connector: make(chan struct{}, 1), entering: make(chan struct{}), actor: Auth{Me: Me{User: User{ID: uuid.NewString()}, Audience: "portal"}, SessionID: uuid.NewString(), TokenHash: make([]byte, 32), Epoch: 1}, source: SessionSource{TenantID: uuid.NewString(), ConnectorID: uuid.NewString(), BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}}
			f.session <- struct{}{}
			// The competing revoker already owns the connector.
			s := &Service{Storage: f, Sources: f}
			revoked := make(chan error, 1)
			go func() {
				defer func() { f.connector <- struct{}{} }()
				select {
				case <-f.entering:
				case <-ctx.Done():
					revoked <- ctx.Err()
					return
				}
				select {
				case <-f.session:
					f.mu.Lock()
					f.revoked = true
					f.mu.Unlock()
					f.session <- struct{}{}
					revoked <- nil
				case <-ctx.Done():
					revoked <- ctx.Err()
				}
			}()
			var e error
			switch entry {
			case "current":
				e = s.RecheckWithin(ctx, f, f.actor)
			case "compatibility":
				e = s.RecheckOrderedWithin(ctx, f, f.actor)
			default:
				// Negative control preserves the former session -> source order.
				f.userLocked = true
				var id string
				e = f.QueryRow(ctx, transaction.Statement{}).Scan(&id)
				if e == nil {
					e = s.checkSourceWithin(ctx, f, true, f.actor.SessionID, f.actor.Me.User.ID)
				}
			}
			// Join the timed-out revoker before releasing the old-order session.
			revokeError := <-revoked
			if f.sessionHeld {
				f.session <- struct{}{}
			}
			if entry == "old_order_control" {
				if !errors.Is(e, context.DeadlineExceeded) || !errors.Is(revokeError, context.DeadlineExceeded) {
					t.Fatal("old-order control did not expose the lock cycle", e, revokeError)
				}
			} else if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || revokeError != nil || f.sessionHeld {
				t.Fatal("revocation could not finish before sourced recheck", e, revokeError)
			}
		})
	}
}

type recheckLockFixture struct {
	mu                               sync.Mutex
	session, connector               chan struct{}
	entering                         chan struct{}
	actor                            Auth
	source                           SessionSource
	userLocked, sessionHeld, revoked bool
}

func (*recheckLockFixture) Dialect() transaction.Dialect { return transaction.Dameng }
func (*recheckLockFixture) Exec(context.Context, transaction.Statement) (int64, error) {
	return 0, errors.New("unexpected mutation")
}
func (*recheckLockFixture) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	return nil, errors.New("unexpected inventory")
}
func (*recheckLockFixture) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	return errors.New("recheck must borrow its caller transaction")
}
func (f *recheckLockFixture) QueryRow(ctx context.Context, _ transaction.Statement) transaction.Row {
	return recheckLockRow{f, ctx}
}
func (*recheckLockFixture) CheckSessionSource(context.Context, pgx.Tx, SessionSource, string) error {
	return errors.New("recheck must retain its source lock")
}
func (f *recheckLockFixture) CheckSessionSourceWithin(ctx context.Context, q transaction.Executor, source SessionSource, user string) error {
	if q != f || source != f.source || user != f.actor.Me.User.ID || !f.userLocked {
		return errors.New("source escaped borrowed user authority")
	}
	close(f.entering)
	select {
	case <-f.connector:
		defer func() { f.connector <- struct{}{} }()
		if e := ctx.Err(); e != nil {
			return e
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.revoked {
			return failure("AUTH_SESSION_EXPIRED")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type recheckLockRow struct {
	f   *recheckLockFixture
	ctx context.Context
}

func (r recheckLockRow) Scan(dest ...any) error {
	f := r.f
	switch len(dest) {
	case 3:
		*dest[0].(*int64), *dest[1].(*string), *dest[2].(*bool) = 1, "active", false
		f.userLocked = true
	case 5:
		*dest[0].(**string), *dest[1].(**string), *dest[2].(**string) = &f.source.TenantID, &f.source.ConnectorID, &f.source.BindingID
		*dest[3].(**int64), *dest[4].(**string) = &f.source.AuthVersion, &f.source.FlowID
	case 1:
		select {
		case <-f.session:
			f.sessionHeld = true
			*dest[0].(*string) = f.actor.SessionID
		case <-r.ctx.Done():
			return r.ctx.Err()
		}
	default:
		return errors.New("unexpected authority read")
	}
	return nil
}
