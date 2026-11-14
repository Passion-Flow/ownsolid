//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

// This in-memory driver exercises actual database/sql Scan and rows.Err through
// the production journal decoder. It does not claim native DM SQL execution.
type journalReadbackRows struct {
	values [][]driver.Value
	index  int
	err    error
}

func (*journalReadbackRows) Columns() []string {
	return []string{"STEP_NO", "SCOPE", "SOURCE_SHA256", "OBJECT_NAME", "STATE", "STARTED_AT", "APPLIED_AT"}
}
func (*journalReadbackRows) Close() error { return nil }
func (r *journalReadbackRows) Next(values []driver.Value) error {
	if r.index == len(r.values) {
		if r.err != nil {
			return r.err
		}
		return io.EOF
	}
	copy(values, r.values[r.index])
	r.index++
	return nil
}

type journalReadbackConnection struct{ rows *journalReadbackRows }

func (*journalReadbackConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("synthetic journal prepare is unsupported")
}
func (*journalReadbackConnection) Close() error { return nil }
func (*journalReadbackConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("synthetic journal transaction is unsupported")
}
func (c *journalReadbackConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return c.rows, nil
}

type journalReadbackConnector struct{ rows *journalReadbackRows }

func (c journalReadbackConnector) Connect(context.Context) (driver.Conn, error) {
	return &journalReadbackConnection{c.rows}, nil
}
func (c journalReadbackConnector) Driver() driver.Driver { return c }
func (journalReadbackConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("synthetic journal requires its connector")
}

func decodeJournalFixture(t *testing.T, values [][]driver.Value, readError error) ([]journalEntry, error) {
	t.Helper()
	db := sql.OpenDB(journalReadbackConnector{&journalReadbackRows{values: values, err: readError}})
	defer db.Close()
	rows, e := db.QueryContext(testContext(t), "synthetic journal readback")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	return readJournalRows(rows)
}

func journalFixtureValue(s step, started time.Time, applied driver.Value) []driver.Value {
	return []driver.Value{int64(s.Number), Scope, s.SHA256, s.Object, "applied", started, applied}
}

func TestNativeJournalReadbackDoesNotOrderByWallClock(t *testing.T) {
	c := smallCatalog(t)
	started := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	for name, delta := range map[string]time.Duration{"rollback": -time.Hour, "equal": 0, "forward": time.Hour} {
		t.Run(name, func(t *testing.T) {
			values := [][]driver.Value{}
			present := map[string]bool{}
			for _, s := range c.steps {
				values = append(values, journalFixtureValue(s, started, started.Add(delta)))
				present[s.Object] = true
			}
			entries, e := decodeJournalFixture(t, values, nil)
			if e != nil {
				t.Fatal("valid journal timestamps refused", e)
			}
			f := &fixtureBackend{state: snapshot{Journal: true, Entries: entries, Present: present}}
			r, e := run(testContext(t), c, f, Apply)
			if e != nil || !r.FoundationReady || r.AppliedSteps != len(c.steps) || r.RuntimeReady || r.PlatformReady || f.creates+f.starts+f.executes+f.completes != 0 {
				t.Fatal("completed readback replayed or refused", r, e)
			}
		})
	}
}

type clockReadbackBackend struct {
	*fixtureBackend
	t       *testing.T
	started time.Time
}

func (f *clockReadbackBackend) inspect(ctx context.Context) (snapshot, error) {
	s, e := f.fixtureBackend.inspect(ctx)
	if e != nil {
		return s, e
	}
	values := [][]driver.Value{}
	for _, entry := range s.Entries {
		var applied driver.Value
		if entry.Completed {
			applied = f.started.Add(-time.Hour)
		}
		values = append(values, []driver.Value{int64(entry.Step), entry.Scope, entry.Source, entry.Object, entry.State, f.started, applied})
	}
	s.Entries, e = decodeJournalFixture(f.t, values, nil)
	return s, e
}

func TestNativeJournalReadbackReconcilesAfterClockRollback(t *testing.T) {
	c := smallCatalog(t)
	s := c.steps[0]
	f := &clockReadbackBackend{
		fixtureBackend: &fixtureBackend{state: snapshot{Journal: true, Entries: []journalEntry{{s.Number, Scope, s.SHA256, s.Object, "applying", true, false}}, Present: map[string]bool{s.Object: true}}},
		t:              t, started: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC),
	}
	r, e := run(testContext(t), c, f, Reconcile)
	if e != nil || r.AppliedSteps != 1 || r.DirtyStep != 0 || r.FoundationReady || r.RuntimeReady || r.PlatformReady || f.completes != 1 || f.creates+f.starts+f.executes != 0 {
		t.Fatal("clock correction stranded reconciliation or replayed DDL", r, e)
	}
	// A fresh explicit invocation rereads the applied row and performs no replay.
	r, e = run(testContext(t), c, f, Reconcile)
	if e != nil || r.AppliedSteps != 1 || f.completes != 1 || f.creates+f.starts+f.executes != 0 {
		t.Fatal("reconciled journal did not survive reread", r, e)
	}
}

func TestNativeJournalReadbackStillRejectsInvalidProof(t *testing.T) {
	c := smallCatalog(t)
	c.steps = c.steps[:1]
	s := c.steps[0]
	started := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func([]driver.Value){
		"null start":       func(row []driver.Value) { row[5] = nil },
		"zero start":       func(row []driver.Value) { row[5] = time.Time{} },
		"invalid start":    func(row []driver.Value) { row[5] = "invalid timestamp" },
		"null completion":  func(row []driver.Value) { row[6] = nil },
		"zero completion":  func(row []driver.Value) { row[6] = time.Time{} },
		"dirty completion": func(row []driver.Value) { row[4] = "applying" },
		"unknown state":    func(row []driver.Value) { row[4] = "unknown" },
		"sequence gap":     func(row []driver.Value) { row[0] = int64(2) },
		"changed source":   func(row []driver.Value) { row[2] = "changed" },
		"changed scope":    func(row []driver.Value) { row[1] = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			row := journalFixtureValue(s, started, started.Add(-time.Hour))
			mutate(row)
			entries, e := decodeJournalFixture(t, [][]driver.Value{row}, nil)
			if e == nil {
				_, e = assess(c, snapshot{Journal: true, Entries: entries, Present: map[string]bool{s.Object: true}})
			}
			if e == nil {
				t.Fatal("invalid journal proof accepted")
			}
		})
	}
	if _, e := decodeJournalFixture(t, nil, errors.New("synthetic read failure")); FailureCode(e) != "DAMENG_SCHEMA_JOURNAL_INVALID" {
		t.Fatal("row iteration failure accepted", e)
	}
	values := make([][]driver.Value, maximumCatalogSteps+1)
	for i := range values {
		values[i] = journalFixtureValue(s, started, started.Add(-time.Hour))
	}
	if _, e := decodeJournalFixture(t, values, nil); FailureCode(e) != "DAMENG_SCHEMA_JOURNAL_INVALID" {
		t.Fatal("journal row limit bypassed", e)
	}
	entries, e := decodeJournalFixture(t, values[:1], nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = assess(c, snapshot{Journal: true, Entries: entries, Present: map[string]bool{}}); FailureCode(e) != "DAMENG_SCHEMA_OBJECT_MISSING" {
		t.Fatal("applied journal without actual object accepted", e)
	}
}
