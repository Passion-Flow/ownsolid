package mysqlbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// These synthetic rows exercise only deterministic bounded streaming and buffer
// reuse. They do not emulate SQL authority, commit, metadata or native acceptance.
type streamRows struct {
	values  []string
	index   int
	buffer  []byte
	closed  bool
	failure error
}

func (r *streamRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.buffer = append(r.buffer[:0], r.values[r.index]...)
	r.index++
	return true
}
func (r *streamRows) Scan(dest ...any) error {
	if len(dest) != 1 {
		return errors.New("synthetic unexpected columns")
	}
	*(dest[0].(*any)) = r.buffer
	return nil
}
func (r *streamRows) Err() error { return r.failure }
func (r *streamRows) Close()     { r.closed = true }

type streamExecutor struct {
	rows    *streamRows
	queries int
}

func (q *streamExecutor) Dialect() transaction.Dialect { return transaction.MySQL }
func (q *streamExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	return 0, errors.New("synthetic writes forbidden")
}
func (q *streamExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	return q.rows
}
func (q *streamExecutor) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	q.queries++
	return q.rows, nil
}
func TestBorrowedPostImageStreamsBuffersAndOmitsOnlyAudit(t *testing.T) {
	template := DatabaseReadback{Tables: []TableReadback{{Table: "audit_events", Columns: []string{"id"}, PrimaryKey: []string{"id"}}, {Table: "users", Columns: []string{"id"}, PrimaryKey: []string{"id"}}}}
	q := &streamExecutor{rows: &streamRows{values: []string{"first", "other"}}}
	p := parserBackupPolicy()
	actual, e := readDatabaseWithin(t.Context(), q, template, p)
	if e != nil || q.queries != 1 || !q.rows.closed || len(actual.Tables) != 1 || actual.Tables[0].Table != "users" || actual.Tables[0].Rows != 2 {
		t.Fatal("bounded streaming/close", actual, e)
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	encoder.Encode([]string{"id"})
	encoder.Encode([]any{[]byte("first")})
	encoder.Encode([]any{[]byte("other")})
	if actual.Tables[0].SHA256 != hex.EncodeToString(hash.Sum(nil)) {
		t.Fatal("driver buffer reused before row hash")
	}
	q = &streamExecutor{rows: &streamRows{values: []string{"first"}, failure: errors.New("synthetic reader failure")}}
	if proof, e := readDatabaseWithin(t.Context(), q, template, p); e == nil || proof.SHA256 != "" || !q.rows.closed {
		t.Fatal("failed row reader projected post-image")
	}
	template.Tables[1].Columns = []string{"id` FROM foreign;"}
	q = &streamExecutor{rows: &streamRows{}}
	if proof, e := readDatabaseWithin(t.Context(), q, template, p); e == nil || proof.SHA256 != "" || q.queries != 0 {
		t.Fatal("foreign template reached SQL")
	}
}
