package transaction

import (
	"context"
	"errors"
	"testing"
)

type recordingExecutor struct {
	affected int64
	err      error
}

func (recordingExecutor) Dialect() Dialect { return PostgreSQL }
func (r recordingExecutor) Exec(context.Context, Statement) (int64, error) {
	return r.affected, r.err
}
func (recordingExecutor) Query(context.Context, Statement) (Rows, error) { return nil, nil }
func (recordingExecutor) QueryRow(context.Context, Statement) Row        { return errorRow{} }

func TestOnlyReadsKeepsRowLocksAndRefusesWrites(t *testing.T) {
	reads := []string{
		`SELECT id FROM managed_nodes WHERE id=$1 FOR UPDATE`,
		`select n.status from managed_nodes n for no key update`,
		"\n  WITH t AS (SELECT 1) SELECT * FROM t FOR SHARE",
		`SELECT updated_at FROM node_tasks`,
	}
	for _, text := range reads {
		if !onlyReads(text) {
			t.Errorf("a read was judged a change: %q", text)
		}
	}
	writes := []string{
		`UPDATE managed_nodes SET facts=$1`,
		`WITH d AS (DELETE FROM node_tasks RETURNING id) SELECT count(*) FROM d`,
		`SELECT nextval('task_sequence')`,
		`SELECT set_config('x','y',true)`,
		`SELECT pg_notify('c','p')`,
		`INSERT INTO audit_events VALUES($1)`,
		`SELECT 1; DELETE FROM t`,
		``,
	}
	for _, text := range writes {
		if onlyReads(text) {
			t.Errorf("a change was judged a read: %q", text)
		}
	}
}

func TestWatchedCountsAffectedRowsFailuresAndWritingQueries(t *testing.T) {
	ctx := context.Background()
	read := SQL(PostgreSQL, `SELECT 1 FROM managed_nodes FOR UPDATE`)
	write := SQL(PostgreSQL, `UPDATE managed_nodes SET facts=$1`)
	cases := map[string]struct {
		run  func(*Watched)
		want bool
	}{
		"nothing":            {func(*Watched) {}, false},
		"a read":             {func(w *Watched) { w.QueryRow(ctx, read); _, _ = w.Query(ctx, read) }, false},
		"no row affected":    {func(w *Watched) { _, _ = w.Exec(ctx, write) }, false},
		"a row affected":     {func(w *Watched) { w.Executor = recordingExecutor{affected: 1}; _, _ = w.Exec(ctx, write) }, true},
		"a failed statement": {func(w *Watched) { w.Executor = recordingExecutor{err: errors.New("x")}; _, _ = w.Exec(ctx, write) }, true},
		"a writing query":    {func(w *Watched) { w.QueryRow(ctx, write) }, true},
	}
	for name, c := range cases {
		w := Watch(recordingExecutor{})
		c.run(w)
		if w.Changed() != c.want {
			t.Errorf("%s: changed %v, want %v", name, w.Changed(), c.want)
		}
	}
}
