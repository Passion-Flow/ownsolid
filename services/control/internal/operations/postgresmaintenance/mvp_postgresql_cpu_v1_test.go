package postgresmaintenance

import (
	"context"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type mvpPredicateExecutor struct {
	transaction.Executor
	active [len(mvpCheckKinds)]bool
	fail   bool
	calls  int
}

func (q *mvpPredicateExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	q.calls++
	return mvpPredicateRow{q.active, q.fail}
}

type mvpPredicateRow struct {
	active [len(mvpCheckKinds)]bool
	fail   bool
}

func (r mvpPredicateRow) Scan(out ...any) error {
	if r.fail {
		return errors.New("synthetic unreadable state")
	}
	for i := range r.active {
		*out[i].(*bool) = r.active[i]
	}
	return nil
}

func TestMVPQuiescenceRetainsUncertainRemoteCustody(t *testing.T) {
	for i, kind := range mvpCheckKinds {
		t.Run(kind, func(t *testing.T) {
			q := &mvpPredicateExecutor{}
			q.active[i] = true
			if e := checkMVPRemoteWorkWithin(t.Context(), q); e == nil || e.Error() != "POSTGRES_MAINTENANCE_"+kind+"_NOT_QUIESCENT" || q.calls != 1 {
				t.Fatal("unresolved owner must refuse", e)
			}
		})
	}
	q := &mvpPredicateExecutor{fail: true}
	if e := checkMVPRemoteWorkWithin(t.Context(), q); e == nil || e.Error() != "POSTGRES_MAINTENANCE_MVP_REMOTE_STATE_UNAVAILABLE" {
		t.Fatal("unreadable current state admitted", e)
	}
	if e := checkMVPRemoteWorkWithin(t.Context(), &mvpPredicateExecutor{}); e != nil {
		t.Fatal(e)
	}
}

func TestMVPSelectionDoesNotBroadenHistoricalMaintenanceProfiles(t *testing.T) {
	for _, profile := range []string{"", "platform", "localization", "releases-cache-v1", "MVP-POSTGRESQL-CPU-V1"} {
		if supportedProfile(profile) {
			t.Fatal("foreign profile", profile)
		}
	}
	if !supportedProfile(ApplicationProfile) || !supportedProfile(MVPApplicationProfile) || ApplicationProfile != "releases" || SchemaVersion != 22 {
		t.Fatal("historical selection changed")
	}
}
