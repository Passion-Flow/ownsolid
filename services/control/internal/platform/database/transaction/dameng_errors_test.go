package transaction

import (
	"context"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

func TestDamengTransactionSignedErrorAndUnknownPrecedence(t *testing.T) {
	err := normalizeDameng(&dameng.Error{Kind: dameng.CheckConflict, SQLState: "23000", NativeCode: -70000}).(*Error)
	if err.Kind != CheckConflict || err.NativeCode != -70000 || err.Number != 0 {
		t.Fatal("DM code passed through unsigned MySQL number")
	}
	for _, e := range []error{errors.Join(context.Canceled, dameng.ErrCommitOutcomeUnknown), errors.Join(dameng.ErrCommitOutcomeUnknown, context.DeadlineExceeded)} {
		for _, fn := range []func(error) error{normalize, normalizeDameng} {
			safe := fn(e)
			kind, ok := CommitOutcome(safe)
			if !ok || kind != CommitUnknown || errors.Is(safe, context.Canceled) {
				t.Fatal("unknown outcome lost")
			}
		}
	}
	if e := SQL(Dameng, "SELECT ? FROM DUAL", 1).validate(Dameng); e != nil {
		t.Fatal(e)
	}
	if e := SQL(PostgreSQL, "SELECT $1", 1).validate(Dameng); e == nil {
		t.Fatal("foreign SQL dialect accepted")
	}
}
