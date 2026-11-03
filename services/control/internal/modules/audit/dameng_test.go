package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type auditWriteBoundary struct {
	transaction.Executor
	rows  int64
	err   error
	calls int
}

func (*auditWriteBoundary) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *auditWriteBoundary) Exec(context.Context, transaction.Statement) (int64, error) {
	q.calls++
	return q.rows, q.err
}
func TestDamengAuditRequiresOneConfirmedWrite(t *testing.T) {
	for _, anonymous := range []bool{false, true} {
		for _, count := range []int64{0, 1, 2} {
			q := &auditWriteBoundary{rows: count}
			var e error
			if anonymous {
				e = WriteAnonymousWithin(context.Background(), q, "portal", "auth.login_failed", "failure", "", make([]byte, 32))
			} else {
				e = WriteWithin(context.Background(), q, "", "", "system", "mail.failed", "failure", "mail_delivery", "", "", "")
			}
			if q.calls != 1 || (e == nil) != (count == 1) {
				t.Fatal("unconfirmed audit write accepted or replayed")
			}
		}
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	q := &auditWriteBoundary{err: unknown}
	if e := WriteAnonymousWithin(context.Background(), q, "portal", "auth.login_failed", "failure", "", make([]byte, 32)); !errors.Is(e, unknown) || q.calls != 1 {
		t.Fatal("storage error reclassified or replayed")
	}
}
