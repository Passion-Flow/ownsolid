package sso

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestDamengSSOUncertainOutcomePrecedesCancellation(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rolled := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, e := range []error{unknown, fmt.Errorf("private wrapper: %w", unknown), errors.Join(context.Canceled, unknown), errors.Join(rolled, context.DeadlineExceeded, unknown)} {
		for _, normalize := range []func(error) error{sourceOutcome, publicFlowError, maintenanceError} {
			if result := normalize(e); result == nil || result.Error() != string(transaction.CommitUnknown) {
				t.Fatal("uncertain outcome was hidden or private diagnostic escaped")
			}
		}
	}
	for _, normalize := range []func(error) error{sourceOutcome, publicFlowError, maintenanceError} {
		if result := normalize(errors.Join(context.Canceled, rolled)); result == nil || result.Error() != string(transaction.CommitRolledBack) {
			t.Fatal("known rollback lost its distinct outcome")
		}
		if normalize(nil) != nil {
			t.Fatal("successful operation gained an error")
		}
	}
}

// This double detects pre-storage refusal and unconfirmed writes. It supplies no
// successful query/result fixture and cannot establish native qualification.
type damengSSORejectExecutor struct{ calls int }

func (*damengSSORejectExecutor) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *damengSSORejectExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	q.calls++
	return 0, nil
}
func (q *damengSSORejectExecutor) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	q.calls++
	return nil, errors.New("unexpected storage")
}
func (q *damengSSORejectExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	q.calls++
	return damengSSORejectRow{}
}

type damengSSORejectRow struct{}

func (damengSSORejectRow) Scan(...any) error { return errors.New("unexpected storage") }

func TestDamengSSOLossyTimeAndUnboundedMaintenanceRefused(t *testing.T) {
	stamp := time.Date(2026, 10, 24, 0, 0, 0, 123456000, time.UTC)
	if !damengTimestamp(stamp) || !damengTimestamp(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("supported native timestamp refused")
	}
	for _, bad := range []time.Time{time.Time{}, stamp.Add(time.Nanosecond), stamp.In(time.FixedZone("offset", 3600)), time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if damengTimestamp(bad) {
			t.Fatal("lossy native timestamp accepted")
		}
	}
	q := &damengSSORejectExecutor{}
	for _, limits := range [][2]int{{0, 1}, {1001, 1}, {1, 0}, {1, 91}} {
		if cleanupFlowsWithin(context.Background(), q, limits[0], limits[1]) == nil || q.calls != 0 {
			t.Fatal("invalid cleanup bounds reached storage")
		}
	}
	for _, limit := range []int{-1, 0, 65} {
		if statement := sourceBoundedSQL(q, "pg", "my", "native", "", limit, 64); !reflect.DeepEqual(statement, transaction.Statement{}) {
			t.Fatal("unbounded integer became native SQL syntax")
		}
	}
}

func TestDamengSSOIntentRequiresOneConfirmedRow(t *testing.T) {
	q := &damengSSORejectExecutor{}
	a := unitBindingActor()
	stamp := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	value := Connector{ID: uuid.NewString(), Name: "Synthetic \u4e16\u754c", Provider: ProviderGitea, Origin: "https://provider.example.invalid", ClientID: uuid.NewString(), Revision: 1, AuthVersion: 1, CreatedAt: stamp, UpdatedAt: stamp}
	e := saveConnectorIntent(context.Background(), q, uuid.NewString(), a, uuid.NewString(), connectorCreate, make([]byte, 32), value)
	if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || q.calls != 1 {
		t.Fatal("unconfirmed intent produced a successful receipt or replayed")
	}
}
