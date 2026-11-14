package mail

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type nativeMailRow func(...any) error

func (r nativeMailRow) Scan(dest ...any) error { return r(dest...) }

// Fault double covers outcome handling only; native SQL requires the target gate.
type nativeMailOutcome struct {
	transaction.Store
	calls, queries, writes int
	rows                   int64
	err                    error
}

func (*nativeMailOutcome) Dialect() transaction.Dialect { return transaction.Dameng }
func (s *nativeMailOutcome) Exec(context.Context, transaction.Statement) (int64, error) {
	s.writes++
	return s.rows, nil
}
func (s *nativeMailOutcome) QueryRow(context.Context, transaction.Statement) transaction.Row {
	s.queries++
	if s.queries == 1 {
		return nativeMailRow(func(...any) error { return sql.ErrNoRows })
	}
	return nativeMailRow(func(dest ...any) error {
		*dest[0].(*string) = "synthetic-delivery"
		*dest[1].(*string) = "invitation"
		*dest[2].(*string) = "synthetic@example.invalid"
		*dest[3].(*string) = "en"
		*dest[4].(*string) = "synthetic-key"
		*dest[5].(*[]byte) = []byte("synthetic-nonce")
		*dest[6].(*[]byte) = []byte("synthetic-ciphertext")
		*dest[7].(*int) = 0
		*dest[8].(*string) = "synthetic-message"
		return nil
	})
}
func (s *nativeMailOutcome) Within(ctx context.Context, _ bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	if e := fn(ctx, s); e != nil {
		return e
	}
	return s.err
}
func TestDamengMailUnknownClaimDoesNotReleasePayload(t *testing.T) {
	unknown := errors.Join(context.DeadlineExceeded, &transaction.Error{Kind: transaction.CommitUnknown})
	store := &nativeMailOutcome{rows: 1, err: unknown}
	c, found, e := (&Outbox{}).claim(context.Background(), store)
	if e != unknown || found || c.ID != "" || len(c.Cipher) != 0 || len(c.Nonce) != 0 || c.Lease != "" || c.Recipient != "" || store.calls != 1 || store.writes != 1 {
		t.Fatal("uncertain claim released payload or replayed")
	}
}
func TestDamengMailEnqueueRequiresConfirmedRow(t *testing.T) {
	outbox := &Outbox{Keys: &Keyring{Active: "synthetic", decoded: map[string][]byte{"synthetic": make([]byte, 32)}}}
	for _, rows := range []int64{0, 2} {
		store := &nativeMailOutcome{rows: rows}
		receipt, e := outbox.EnqueueWithin(context.Background(), store, Input{Purpose: "password_changed", Recipient: "synthetic@example.invalid", Locale: "en", UserID: "synthetic"})
		if e == nil || receipt.ID != "" || receipt.State != "" || store.writes != 1 {
			t.Fatal("unconfirmed enqueue reported queued")
		}
	}
}
func TestDamengMailRetryRefusesLossyDuration(t *testing.T) {
	store := &nativeMailOutcome{rows: 1}
	for _, wait := range []time.Duration{-time.Microsecond, time.Nanosecond} {
		if e := (&Outbox{}).requeue(context.Background(), store, claimed{}, wait, "synthetic"); e == nil || store.writes != 0 {
			t.Fatal("lossy retry interval reached storage")
		}
	}
}
