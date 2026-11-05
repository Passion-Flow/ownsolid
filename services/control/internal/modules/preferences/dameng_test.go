package preferences

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type nativePreferenceRow struct {
	value Preference
	err   error
}

func (r nativePreferenceRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string), *dest[1].(*int64), *dest[2].(*time.Time) = r.value.Theme, r.value.Revision, r.value.UpdatedAt
	return nil
}

type nativePreferenceExecutor struct {
	transaction.Executor
	t                 *testing.T
	rows              []nativePreferenceRow
	counts            []int64
	execErrors        []error
	queries, executes int
}

func (*nativePreferenceExecutor) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *nativePreferenceExecutor) QueryRow(_ context.Context, statement transaction.Statement) transaction.Row {
	q.t.Helper()
	want := transaction.SQL(transaction.Dameng, "SELECT theme,revision,updated_at FROM user_preferences WHERE user_id=? FOR UPDATE", "actor")
	if !reflect.DeepEqual(statement, want) || q.queries >= len(q.rows) {
		q.t.Fatal("unexpected native preference query or statement after a failure")
	}
	r := q.rows[q.queries]
	q.queries++
	return r
}
func (q *nativePreferenceExecutor) Exec(_ context.Context, statement transaction.Statement) (int64, error) {
	q.t.Helper()
	if q.executes >= len(q.counts) {
		q.t.Fatal("unexpected statement after failure")
	}
	if q.executes == 0 && !reflect.DeepEqual(statement, transaction.SQL(transaction.Dameng, "UPDATE user_preferences SET theme=?,revision=revision+1,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE user_id=? AND revision=?", DarkTheme, "actor", int64(1))) {
		q.t.Fatal("native revision predicate or parameter order changed")
	}
	i := q.executes
	q.executes++
	return q.counts[i], q.execErrors[i]
}

func TestDamengPreferenceRevisionAuditAndFailureBoundary(t *testing.T) {
	at := time.Date(2026, 10, 24, 7, 0, 0, 123456000, time.UTC)
	old := Preference{Theme: LightTheme, Density: ComfortableDensity, Locale: AutoLocale, Revision: 1, UpdatedAt: at}
	updated := Preference{Theme: DarkTheme, Density: ComfortableDensity, Locale: AutoLocale, Revision: 2, UpdatedAt: at.Add(time.Second)}
	unknown := errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	for _, tc := range []struct {
		name           string
		rows           []nativePreferenceRow
		counts         []int64
		execErrors     []error
		expected       int64
		wantCode       string
		wantQueries    int
		wantExecutions int
	}{
		{"success", []nativePreferenceRow{{old, nil}, {updated, nil}}, []int64{1, 1}, []error{nil, nil}, 1, "", 2, 2},
		{"stale_revision", []nativePreferenceRow{{old, nil}}, nil, nil, 2, "PREFERENCE_REVISION_CONFLICT", 1, 0},
		{"missing_seed", []nativePreferenceRow{{Preference{}, sql.ErrNoRows}}, nil, nil, 1, "PREFERENCE_UNAVAILABLE", 1, 0},
		{"zero_update", []nativePreferenceRow{{old, nil}}, []int64{0}, []error{nil}, 1, "PREFERENCE_REVISION_CONFLICT", 1, 1},
		{"multi_update", []nativePreferenceRow{{old, nil}}, []int64{2}, []error{nil}, 1, "PREFERENCE_REVISION_CONFLICT", 1, 1},
		{"poisoned_update", []nativePreferenceRow{{old, nil}}, []int64{0}, []error{unknown}, 1, string(transaction.CommitUnknown), 1, 1},
		{"invalid_readback", []nativePreferenceRow{{old, nil}, {old, nil}}, []int64{1}, []error{nil}, 1, "PREFERENCE_UNAVAILABLE", 2, 1},
		{"zero_audit", []nativePreferenceRow{{old, nil}, {updated, nil}}, []int64{1, 0}, []error{nil, nil}, 1, "STORAGE_UNAVAILABLE", 2, 2},
		{"multi_audit", []nativePreferenceRow{{old, nil}, {updated, nil}}, []int64{1, 2}, []error{nil, nil}, 1, "STORAGE_UNAVAILABLE", 2, 2},
		{"unknown_audit", []nativePreferenceRow{{old, nil}, {updated, nil}}, []int64{1, 0}, []error{nil, unknown}, 1, string(transaction.CommitUnknown), 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &nativePreferenceExecutor{t: t, rows: tc.rows, counts: tc.counts, execErrors: tc.execErrors}
			a := identity.Auth{}
			a.Me.User.ID = "actor"
			a.Me.Audience = "portal"
			got, err := updateWithin(t.Context(), q, a, "tenant", UpdateRequest{Theme: new(DarkTheme), ExpectedRevision: tc.expected}, "request")
			err = preferenceError(err)
			if tc.wantCode == "" {
				if err != nil || got != updated {
					t.Fatal("native preference update/readback failed", err)
				}
			} else if err == nil || err.Error() != tc.wantCode || got != (Preference{}) {
				t.Fatal("failure leaked DTO or changed outcome", err)
			}
			if q.queries != tc.wantQueries || q.executes != tc.wantExecutions {
				t.Fatal("statement continued after failure or audit was skipped")
			}
		})
	}
}

// Chains without the density column refuse what they cannot store, before any write.
func TestDamengPreferenceRefusesWhatItsChainCannotStore(t *testing.T) {
	at := time.Date(2026, 10, 24, 7, 0, 0, 0, time.UTC)
	old := Preference{Theme: LightTheme, Density: ComfortableDensity, Revision: 1, UpdatedAt: at}
	for name, input := range map[string]UpdateRequest{
		"system_theme":    {Theme: new(SystemTheme), ExpectedRevision: 1},
		"compact_density": {Density: new(CompactDensity), ExpectedRevision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			q := &nativePreferenceExecutor{t: t, rows: []nativePreferenceRow{{old, nil}}}
			a := identity.Auth{}
			a.Me.User.ID = "actor"
			a.Me.Audience = "portal"
			_, e := updateWithin(t.Context(), q, a, "tenant", input, "request")
			if e == nil || e.Error() != "PREFERENCE_UNAVAILABLE" || q.executes != 0 {
				t.Fatal("native chain accepted a value it cannot store", e, q.executes)
			}
		})
	}
}

func TestDamengPreferenceBinding(t *testing.T) {
	q := &nativePreferenceExecutor{}
	args := preferenceAuditArgs(q, "id", "actor", "tenant", "portal", "request")
	if !reflect.DeepEqual(args, []any{"id", "actor", "tenant", "portal", "actor", "request"}) {
		t.Fatal("audit actor/target binding changed")
	}
	if !reflect.DeepEqual(preferenceSQL(q, "PG", "MY", "DM", args...), transaction.SQL(transaction.Dameng, "DM", args...)) {
		t.Fatal("native statement selected another dialect")
	}
}
