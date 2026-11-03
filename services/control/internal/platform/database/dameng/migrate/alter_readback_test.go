//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// Exercises the production metadata decoder and dirty ALTER selection through
// database/sql. These deterministic dictionary rows are not native qualification.
type alterRows struct {
	width, position int
	values          [][]driver.Value
}

func (r *alterRows) Columns() []string { return make([]string, r.width) }
func (*alterRows) Close() error        { return nil }
func (r *alterRows) Next(out []driver.Value) error {
	if r.position == len(r.values) {
		return io.EOF
	}
	copy(out, r.values[r.position])
	r.position++
	return nil
}

type alterDictionary struct {
	tables      map[string]table
	entries     []journalEntry
	columnReads int
	failColumns bool
}

func (d *alterDictionary) Connect(context.Context) (driver.Conn, error) { return d, nil }
func (d *alterDictionary) Driver() driver.Driver                        { return d }
func (*alterDictionary) Open(string) (driver.Conn, error) {
	return nil, errors.New("explicit fixture only")
}
func (*alterDictionary) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unavailable")
}
func (*alterDictionary) Begin() (driver.Tx, error) { return nil, errors.New("begin unavailable") }
func (*alterDictionary) Close() error              { return nil }
func (d *alterDictionary) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	r := &alterRows{}
	switch {
	case strings.Contains(q, "FROM ALL_OBJECTS"):
		r.width = 2
		for name := range d.tables {
			r.values = append(r.values, []driver.Value{name, "TABLE"})
		}
	case strings.Contains(q, `FROM "PO_SCHEMA_MIGRATIONS"`):
		r.width = 7
		at := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
		for _, e := range d.entries {
			var applied driver.Value
			if e.Completed {
				applied = at.Add(-time.Minute)
			}
			r.values = append(r.values, []driver.Value{int64(e.Step), e.Scope, e.Source, e.Object, e.State, at, applied})
		}
	case strings.Contains(q, "FROM ALL_TAB_COLUMNS"):
		d.columnReads++
		if d.failColumns {
			return nil, errors.New("synthetic native statement failure")
		}
		r.width = 10
		for _, c := range d.tables[args[1].Value.(string)].Columns {
			nullable := "N"
			if c.Nullable {
				nullable = "Y"
			}
			unit := "B"
			if c.Characters {
				unit = "C"
			}
			var def driver.Value
			if c.Default != "" {
				def = c.Default
			}
			r.values = append(r.values, []driver.Value{c.Name, c.Type, int64(c.Width), unit, int64(c.Width), int64(c.Width), nullable, def, "NO", "NO"})
		}
	case strings.Contains(q, "FROM ALL_CONSTRAINTS"):
		r.width = 10
		for _, c := range d.tables[args[1].Value.(string)].Constraints {
			var condition, key driver.Value
			if c.Kind == "C" {
				condition = c.Check
			} else {
				key = c.Name + "_IDX"
			}
			r.values = append(r.values, []driver.Value{c.Name, c.Kind, condition, nil, nil, nil, "ENABLED", "NOT DEFERRABLE", "VALIDATED", key})
		}
	case strings.Contains(q, "FROM ALL_CONS_COLUMNS"):
		r.width = 1
		for _, c := range d.tables[args[2].Value.(string)].Constraints {
			if c.Name == args[1].Value.(string) {
				for _, col := range c.Columns {
					r.values = append(r.values, []driver.Value{col})
				}
			}
		}
	case strings.Contains(q, "FROM ALL_INDEXES"):
		r.width = 4
	default:
		return nil, errors.New("unexpected dictionary query")
	}
	return r, nil
}
func alterFixture(t *testing.T) (catalog, table, table, table) {
	t.Helper()
	c := catalog{journal: journalTable()}
	a := table{Name: "THING", Columns: []column{col("ID", "INTEGER", 0, false, "")}}
	b := cloneTable(a)
	b.Columns = append(b.Columns, text("LABEL", 20, true))
	z := cloneTable(b)
	z.Constraints = append(z.Constraints, check("THING_ID", "\"ID\">0"))
	f := schemaFragment{FormatVersion: 1, ID: "fixture", Changes: []schemaChange{{ID: "THING", Kind: "table", After: &a, SQL: "CREATE TABLE THING (ID INTEGER NOT NULL)"}, {ID: "THING_LABEL", Kind: "alter", Before: &a, After: &b, SQL: "ALTER TABLE THING ADD LABEL VARCHAR(20 CHAR)"}, {ID: "THING_CHECK", Kind: "alter", Before: &b, After: &z, SQL: "ALTER TABLE THING ADD CONSTRAINT THING_ID CHECK (ID>0)"}}}
	if e := appendFragment(&c, f, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	return c, a, b, z
}
func TestNativeDirtyAlterExactBeforeAfterAndSupersededShape(t *testing.T) {
	c, before, after, final := alterFixture(t)
	for _, tc := range []struct {
		name           string
		actual         table
		applied, dirty int
		wantPresent    bool
		wantError      bool
	}{
		{"before", before, 1, 2, false, false}, {"after", after, 1, 2, true, false}, {"later applied supersedes earlier", final, 3, 0, true, false}, {"unrecognized partial", final, 1, 2, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &alterDictionary{tables: map[string]table{journalName: c.journal, "THING": tc.actual}}
			for i := 0; i < tc.applied; i++ {
				s := c.steps[i]
				d.entries = append(d.entries, journalEntry{s.Number, Scope, s.SHA256, s.Object, "applied", true, true})
			}
			if tc.dirty != 0 {
				s := c.steps[tc.dirty-1]
				d.entries = append(d.entries, journalEntry{s.Number, Scope, s.SHA256, s.Object, "applying", true, false})
			}
			pool := sql.OpenDB(d)
			defer pool.Close()
			connection, e := pool.Conn(testContext(t))
			if e != nil {
				t.Fatal(e)
			}
			defer connection.Close()
			n := nativeBackend{connection, dameng.SchemaConfiguration{Schema: "APP", StepSeconds: 5, MaximumMetadataBytes: 16384}, c}
			s, e := n.inspect(testContext(t))
			if tc.wantError {
				if e == nil {
					t.Fatal("non-contract partial shape accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if s.Present["THING_LABEL"] != tc.wantPresent {
				t.Fatal("dirty ALTER status does not match actual shape")
			}
			r, e := assess(c, s)
			if e != nil || r.AppliedSteps != tc.applied || r.DirtyStep != tc.dirty {
				t.Fatal("journal/readback disagreement", r, e)
			}
		})
	}
}
func TestNativeAlterDoesNotQueryAgainAfterNativeMetadataFailure(t *testing.T) {
	c, before, _, _ := alterFixture(t)
	d := &alterDictionary{tables: map[string]table{journalName: c.journal, "THING": before}, failColumns: true}
	for i := 0; i < 2; i++ {
		s := c.steps[i]
		state := "applied"
		if i == 1 {
			state = "applying"
		}
		d.entries = append(d.entries, journalEntry{s.Number, Scope, s.SHA256, s.Object, state, true, i == 0})
	}
	p := sql.OpenDB(d)
	defer p.Close()
	connection, e := p.Conn(testContext(t))
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	n := nativeBackend{connection, dameng.SchemaConfiguration{Schema: "APP", StepSeconds: 5, MaximumMetadataBytes: 16384}, c}
	if _, e = n.inspect(testContext(t)); FailureCode(e) != "DAMENG_SCHEMA_COLUMN_METADATA_UNAVAILABLE" || d.columnReads != 1 {
		t.Fatal("native failure was treated as retryable shape drift", e, d.columnReads)
	}
}
