package damengbackup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type rowFixture struct {
	values     [][]driver.Value
	open       atomic.Int64
	wrongChunk bool
}
type rowFixtureDriver struct{}
type rowFixtureConn struct{ f *rowFixture }
type rowFixtureRows struct {
	f       *rowFixture
	columns []string
	values  [][]driver.Value
	index   int
	closed  bool
}

var rowFixtureRegistry sync.Map

func (rowFixtureDriver) Open(name string) (driver.Conn, error) {
	v, ok := rowFixtureRegistry.Load(name)
	if !ok {
		return nil, errors.New("fixture missing")
	}
	return &rowFixtureConn{v.(*rowFixture)}, nil
}
func (*rowFixtureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*rowFixtureConn) Close() error              { return nil }
func (*rowFixtureConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (c *rowFixtureConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if c.f.open.Load() != 0 {
		return nil, errors.New("cursor not joined before LOB query")
	}
	values := [][]driver.Value{}
	columns := []string{"V"}
	switch {
	case strings.HasPrefix(query, "SELECT COUNT(*)"):
		values = [][]driver.Value{{int64(len(c.f.values))}}
	case strings.HasPrefix(query, "SELECT DBMS_LOB.SUBSTR("):
		amount, offset, id := args[0].Value.(int64), args[1].Value.(int64), args[2].Value.(int64)
		which := 1
		if strings.Contains(query, `"B"`) {
			which = 2
		}
		var v driver.Value
		for _, row := range c.f.values {
			if row[0].(int64) == id {
				if which == 1 {
					r := []rune(row[1].(string))
					v = string(r[offset-1 : offset-1+amount])
				} else {
					b := row[2].([]byte)
					v = append([]byte(nil), b[offset-1:offset-1+amount]...)
				}
				break
			}
		}
		if c.f.wrongChunk {
			v = ""
		}
		values = [][]driver.Value{{v}}
	case strings.Contains(query, " OFFSET ? ROWS FETCH NEXT ? ROWS ONLY"):
		offset, limit := int(args[0].Value.(int64)), int(args[1].Value.(int64))
		columns = []string{"ID", "C", "B"}
		for _, row := range c.f.values[offset:min(offset+limit, len(c.f.values))] {
			out := []driver.Value{row[0], nil, nil}
			if row[1] != nil {
				out[1] = int64(utf8.RuneCountInString(row[1].(string)))
			}
			if row[2] != nil {
				out[2] = int64(len(row[2].([]byte)))
			}
			values = append(values, out)
		}
	default:
		return nil, errors.New("unexpected native SQL")
	}
	c.f.open.Add(1)
	return &rowFixtureRows{f: c.f, columns: columns, values: values}, nil
}
func (r *rowFixtureRows) Columns() []string { return r.columns }
func (r *rowFixtureRows) Close() error {
	if !r.closed {
		r.closed = true
		r.f.open.Add(-1)
	}
	return nil
}
func (r *rowFixtureRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
func init() { sql.Register("dameng-recovery-row-fixture", rowFixtureDriver{}) }
func rowTestPolicy() SnapshotPolicy {
	return SnapshotPolicy{PageRows: 2, MaximumRows: 1000, MaximumBytes: 1 << 20, MaximumLOBBytes: 1 << 20, LOBChunkSize: 256, MaximumTreeBytes: 1 << 20, MaximumTreeFiles: 100, MaximumTreeMetadataBytes: 1 << 20}
}
func rowTestDB(t *testing.T, f *rowFixture) *sql.DB {
	t.Helper()
	name := t.Name()
	rowFixtureRegistry.Store(name, f)
	t.Cleanup(func() { rowFixtureRegistry.Delete(name) })
	db, e := sql.Open("dameng-recovery-row-fixture", name)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEveryRowLOBReadbackIsTypedPagedAndChunkIndependent(t *testing.T) {
	text := strings.Repeat("\u6587🙂x", 317)
	blob := []byte(strings.Repeat("\x00\xffdata", 613))
	f := &rowFixture{values: [][]driver.Value{{int64(1), text, blob}, {int64(2), "", []byte{}}, {int64(3), nil, nil}}}
	db := rowTestDB(t, f)
	table := nativeTable{Name: "T", Columns: []nativeColumn{{"ID", "INTEGER", false}, {"C", "CLOB", true}, {"B", "BLOB", true}}, Keys: []string{"ID"}}
	var previous string
	for _, size := range []int{256, 512, 1024} {
		p := rowTestPolicy()
		p.LOBChunkSize = size
		s := newRowStream(p)
		if e := readNativeTable(context.Background(), db, table, s); e != nil {
			t.Fatal(e)
		}
		current := hashString(s.digest)
		if previous != "" && current != previous {
			t.Fatal("LOB chunk policy changed data fingerprint")
		}
		previous = current
		if s.rows != 3 || f.open.Load() != 0 {
			t.Fatal("incomplete rows/cursors")
		}
	}
}
func TestLOBReadbackRefusesWrongChunkAndBudgets(t *testing.T) {
	for _, mode := range []string{"chunk", "lob", "rows", "bytes", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := &rowFixture{values: [][]driver.Value{{int64(1), strings.Repeat("🙂", 600), []byte("x")}}}
			p := rowTestPolicy()
			ctx := context.Background()
			switch mode {
			case "chunk":
				f.wrongChunk = true
			case "lob":
				p.MaximumLOBBytes = 10
			case "rows":
				p.MaximumRows = 0
			case "bytes":
				p.MaximumBytes = 10
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			s := newRowStream(p)
			table := nativeTable{"T", []nativeColumn{{"ID", "INTEGER", false}, {"C", "CLOB", true}, {"B", "BLOB", true}}, []string{"ID"}}
			if e := readNativeTable(ctx, rowTestDB(t, f), table, s); e == nil {
				t.Fatal("invalid complete image accepted")
			}
		})
	}
}
func TestTypedSnapshotPreservesNullFalseEmptyAndPrecision(t *testing.T) {
	cases := []struct {
		kind  string
		value any
		valid bool
	}{{"BIT", false, true}, {"BIT", int64(0), true}, {"BIT", "false", false}, {"BIT", int64(2), false}, {"VARCHAR", "", true}, {"VARCHAR", nil, true}, {"VARBINARY", []byte{}, true}, {"VARBINARY", nil, true}, {"BIGINT", int64(1 << 60), true}, {"BIGINT", float64(1 << 60), false}, {"TIMESTAMP", time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC), true}, {"TIMESTAMP", time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC), false}}
	seen := map[string]string{}
	for _, c := range cases {
		s := newRowStream(rowTestPolicy())
		e := s.scalar(c.kind, c.value)
		if (e == nil) != c.valid {
			t.Fatalf("typed %s valid=%v: %v", c.kind, c.valid, e)
		}
		if c.valid {
			key := c.kind
			if c.value == nil {
				key += "NULL"
			}
			seen[key] = hashString(s.digest)
		}
	}
	if seen["VARCHAR"] == seen["VARCHARNULL"] || seen["VARBINARY"] == seen["VARBINARYNULL"] {
		t.Fatal("NULL collapsed into empty")
	}
}
