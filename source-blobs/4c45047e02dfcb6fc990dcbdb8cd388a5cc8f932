package transaction

import (
	"context"
	"regexp"
	"sync/atomic"
)

// Watched passes statements to an Executor and records whether any of them may have changed
// data. It errs towards "changed": a statement it cannot classify, an error, or any affected
// row counts. A caller that sees no change may commit without waiting for the disk, because a
// transaction whose only effect was a row lock loses nothing if a crash forgets it.
type Watched struct {
	Executor
	changed atomic.Bool
}

// Watch starts watching the statements run through q.
func Watch(q Executor) *Watched { return &Watched{Executor: q} }

// Changed reports whether a statement run through w may have changed data.
func (w *Watched) Changed() bool { return w.changed.Load() }

// Exec runs q and counts it as a change when it affected a row or failed.
func (w *Watched) Exec(ctx context.Context, q Statement) (int64, error) {
	n, e := w.Executor.Exec(ctx, q)
	if e != nil || n != 0 {
		w.changed.Store(true)
	}
	return n, e
}

// Query runs q and counts it as a change unless its text only reads.
func (w *Watched) Query(ctx context.Context, q Statement) (Rows, error) {
	if !onlyReads(q.text) {
		w.changed.Store(true)
	}
	return w.Executor.Query(ctx, q)
}

// QueryRow runs q and counts it as a change unless its text only reads.
func (w *Watched) QueryRow(ctx context.Context, q Statement) Row {
	if !onlyReads(q.text) {
		w.changed.Store(true)
	}
	return w.Executor.QueryRow(ctx, q)
}

var (
	// A row lock is not a change of data: its clause is removed before the text is judged.
	rowLockClause = regexp.MustCompile(`(?i)\bFOR\s+(NO\s+KEY\s+)?(UPDATE|SHARE)\b`)
	// Words that can change data from inside a query, including through functions.
	writingWord   = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE|TRUNCATE|NEXTVAL|SETVAL|SET_CONFIG|NOTIFY|PG_NOTIFY|LO_\w+|COPY|CALL|DO|CREATE|ALTER|DROP|GRANT|REVOKE|LOCK)\b`)
	leadingSelect = regexp.MustCompile(`(?is)^\s*(SELECT|WITH)\b`)
)

// onlyReads reports whether text is a query that cannot change data: it starts with SELECT or
// WITH and names no writing statement or function. A query that calls a function the database
// defines is still judged by its text alone, so a writing function must not be called this way.
func onlyReads(text string) bool {
	return leadingSelect.MatchString(text) && !writingWord.MatchString(rowLockClause.ReplaceAllString(text, ""))
}
