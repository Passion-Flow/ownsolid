package artifactstores

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// These are explicit repository/statement doubles. They do not execute DM SQL,
// substitute for the native adapter, or report a provider operation successful.
type nativeObjectExecutor struct {
	dialect transaction.Dialect
	exec    func(transaction.Statement) (int64, error)
	query   func(transaction.Statement) (transaction.Rows, error)
}

func (q nativeObjectExecutor) Dialect() transaction.Dialect { return q.dialect }
func (q nativeObjectExecutor) Exec(_ context.Context, s transaction.Statement) (int64, error) {
	if q.exec == nil {
		return 0, errors.New("UNEXPECTED_TEST_EXEC")
	}
	return q.exec(s)
}
func (q nativeObjectExecutor) Query(_ context.Context, s transaction.Statement) (transaction.Rows, error) {
	if q.query == nil {
		return nil, errors.New("UNEXPECTED_TEST_QUERY")
	}
	return q.query(s)
}
func (q nativeObjectExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	return nativeObjectErrorRow{}
}

type nativeObjectErrorRow struct{}

func (nativeObjectErrorRow) Scan(...any) error { return errors.New("UNEXPECTED_TEST_QUERY_ROW") }

func TestDamengObjectStatementNeverFallsBack(t *testing.T) {
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL, transaction.Dameng, "unrecognized"} {
		q := nativeObjectExecutor{dialect: dialect}
		want := map[transaction.Dialect]string{transaction.PostgreSQL: "explicit PG", transaction.MySQL: "explicit MySQL", transaction.Dameng: `SELECT "ID" FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=?`}[dialect]
		got := statement(q, "explicit PG", "explicit MySQL", `SELECT "ID" FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=?`, "synthetic-tenant")
		if !reflect.DeepEqual(got, transaction.SQL(dialect, want, "synthetic-tenant")) {
			t.Fatal("selected dialect/arguments changed")
		}
	}
}

func TestDamengObjectBindingsKeepTextBinaryAndCursorSemantics(t *testing.T) {
	q := nativeObjectExecutor{dialect: transaction.Dameng}
	pg := nativeObjectExecutor{dialect: transaction.PostgreSQL}
	my := nativeObjectExecutor{dialect: transaction.MySQL}
	raw := []byte(`{"accepted":false}`)
	if got, ok := jsonParameter(q, raw).(string); !ok || got != string(raw) {
		t.Fatal("JSON not bound as CLOB text")
	}
	for _, other := range []nativeObjectExecutor{pg, my} {
		if !reflect.DeepEqual(jsonParameter(other, raw), raw) {
			t.Fatal("existing JSON binding changed")
		}
	}
	w := workspaces.Workspace{TenantID: "tenant", ID: "workspace"}
	o := storedObject{Object: Object{ID: "object", WorkspaceID: w.ID, SHA256: strings.Repeat("a", 64)}, Tenant: w.TenantID}
	op := "operation"
	o.OperationID = &op
	cases := []struct{ got, want []any }{
		{listArgs(q, w, false, "user", "", 23), []any{"tenant", "workspace", false, "user", nil, nil}},
		{listArgs(q, w, true, "user", "cursor", 23), []any{"tenant", "workspace", true, "user", "cursor", "cursor"}},
		{grantListArgs(q, w, "object", "", 23), []any{"tenant", "workspace", "object", nil, nil}},
		{ingestionListArgs(q, w, "object", false, "user", "", 23), []any{"tenant", "workspace", "object", false, "user", nil, nil}},
		{expiryArgs(q, "tenant", "workspace", "store", 23), []any{"tenant", "workspace", "store"}},
		{grantAuthorityArgs(q, w, "object", "grant", "user", "read"), []any{"tenant", "workspace", "object", "grant", "user", "read", "read"}},
		{partAckArgs(q, o, objectstore.Part{Number: 2, ETag: "etag"}), []any{"etag", "tenant", "workspace", "object", int32(2)}},
		{recordTargetArgs(q, o, "ingestion", "target"), []any{"target", "tenant", "workspace", "object", "ingestion", "target"}},
		{ingestionReceiptArgs(q, o, "ingestion", raw), []any{string(raw), "tenant", "workspace", "object", "ingestion"}},
		{verificationArgs(q, o, "ingestion", "target"), []any{o.SHA256, "tenant", "workspace", "object", "ingestion", "target"}},
		{workerSentArgs(q, o, 17), []any{17, "tenant", "workspace", "object", "operation"}},
	}
	for i, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Fatalf("native binding case %d differs", i)
		}
	}
	if !reflect.DeepEqual(listArgs(my, w, false, "user", "", 23), []any{"tenant", "workspace", false, "user", "", "", 23}) || !reflect.DeepEqual(listArgs(pg, w, false, "user", "", 23), []any{"tenant", "workspace", false, "user", "", 23}) {
		t.Fatal("existing pagination arguments changed")
	}
	a := identity.Auth{Epoch: 7}
	a.Me.User.ID = "actor"
	operation := operationArgs(q, a, w, o, "create", "creating", 17)
	if len(operation) != 8 || operation[0] != "creating" || !id(operation[1].(string)) || operation[2] != "create" || operation[3] != string(actorBytes(a)) || operation[4] != 17 || operation[5] != "tenant" {
		t.Fatal("operation identity/text/argument order lost")
	}
	if !reflect.DeepEqual(reconcileArgs(q, o, "next", a, 17), []any{"next", string(actorBytes(a)), 17, "tenant", "workspace", "object"}) {
		t.Fatal("reconciliation actor not text-bound")
	}
}

func TestDamengObjectSQLSourceClosure(t *testing.T) {
	set := token.NewFileSet()
	packages, e := parser.ParseDir(set, ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	var literalText func(ast.Expr) string
	literalText = func(expr ast.Expr) string {
		switch v := expr.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				s, e := strconv.Unquote(v.Value)
				if e != nil {
					t.Fatal(e)
				}
				return s
			}
		case *ast.BinaryExpr:
			return literalText(v.X) + literalText(v.Y)
		}
		return ""
	}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := call.Fun.(*ast.Ident)
				if !ok || name.Name != "statement" {
					return true
				}
				count++
				if len(call.Args) < 4 {
					t.Fatal("native statement absent", set.Position(call.Pos()))
				}
				if index, ok := call.Args[3].(*ast.IndexExpr); ok {
					if literalText(call.Args[2]) != "" || index.Index.(*ast.BasicLit).Value != "2" {
						t.Fatal("invalid explicit metadata triplet")
					}
					return true
				}
				dm, my := literalText(call.Args[3]), literalText(call.Args[2])
				if dm == "" {
					if !strings.Contains(my, "operation_id=UUID()") {
						t.Fatal("native statement missing", set.Position(call.Pos()))
					}
					return true
				}
				for _, forbidden := range []string{"UTC_TIMESTAMP", "TIMESTAMPADD", "CAST(? AS JSON)", "FOR SHARE", "LIMIT ?", "::", "$1", "`", "UUID()"} {
					if strings.Contains(dm, forbidden) {
						t.Fatal("non-native fragment", forbidden, set.Position(call.Pos()))
					}
				}
				if !strings.Contains(dm, `"ARTIFACT_`) && !strings.Contains(dm, `"WORKSPACE_MEMBERSHIPS"`) && !strings.Contains(dm, `"INSTALLATION_STATE"`) && !strings.Contains(dm, `"AUDIT_EVENTS"`) && !strings.Contains(dm, `"USERS"`) {
					t.Fatal("unquoted native relation", set.Position(call.Pos()))
				}
				want := strings.Count(my, "?")
				if strings.Contains(my, "LIMIT ?") {
					want--
				}
				if strings.Count(dm, "?") != want {
					t.Fatal("positional parameter count changed", set.Position(call.Pos()))
				}
				return true
			})
		}
	}
	if count != 71 {
		t.Fatalf("review native statement inventory: %d", count)
	}
}

type nativeObjectRows struct {
	values [][2]string
	index  int
	closed bool
	err    error
	endErr error
}

func (r *nativeObjectRows) Next() bool { return r.index < len(r.values) }
func (r *nativeObjectRows) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	v := r.values[r.index]
	r.index++
	*dest[0].(*string) = v[0]
	*dest[1].(*string) = v[1]
	return nil
}
func (r *nativeObjectRows) Err() error { return r.endErr }
func (r *nativeObjectRows) Close()     { r.closed = true }

func TestDamengRestoreFencePreservesUncertainOutcomeAndClosesRows(t *testing.T) {
	const tenant = "11111111-1111-1111-1111-111111111111"
	const object = "22222222-2222-2222-2222-222222222222"
	const old = "33333333-3333-3333-3333-333333333333"
	for _, mode := range []string{"complete", "commit_unknown", "constraint_failure", "row_count", "scan_failure", "query_failure", "rows_failure", "duplicate_row"} {
		t.Run(mode, func(t *testing.T) {
			queries, writes := 0, 0
			rows := &nativeObjectRows{values: [][2]string{{object, old}}}
			failure := &transaction.Error{Kind: transaction.CommitUnknown, NativeCode: -70005}
			if mode == "constraint_failure" {
				failure = &transaction.Error{Kind: transaction.CheckConflict, NativeCode: -6603}
			}
			if mode == "scan_failure" {
				rows.err = failure
			}
			if mode == "rows_failure" {
				rows.endErr = failure
			}
			if mode == "duplicate_row" {
				rows.values = append(rows.values, rows.values[0])
			}
			q := nativeObjectExecutor{dialect: transaction.Dameng}
			q.query = func(s transaction.Statement) (transaction.Rows, error) {
				queries++
				if queries > 2 {
					t.Fatal("unbounded restoration retry")
				}
				if mode == "query_failure" {
					return nil, failure
				}
				if queries == 1 {
					return rows, nil
				}
				return &nativeObjectRows{}, nil
			}
			q.exec = func(s transaction.Statement) (int64, error) {
				writes++
				if !rows.closed {
					t.Fatal("native result consumer retained during mutation")
				}
				// Inspect synthetic arguments only; Statement's production formatter stays redacted.
				args := reflect.ValueOf(s).FieldByName("args")
				fresh := args.Index(0).Elem().String()
				if !id(fresh) || fresh == old || args.Index(1).Elem().String() != tenant || args.Index(2).Elem().String() != object || args.Index(3).Elem().String() != old {
					t.Fatal("restore fence not bound to original operation")
				}
				if mode == "commit_unknown" || mode == "constraint_failure" {
					return 0, failure
				}
				if mode == "row_count" {
					return 0, nil
				}
				return 1, nil
			}
			e := fenceDamengAfterRestore(context.Background(), q, tenant)
			if mode == "complete" {
				if e != nil || queries != 2 || writes != 1 {
					t.Fatal("restore fence incomplete", e)
				}
				return
			}
			if e == nil || queries != 1 {
				t.Fatal("failure continued native SQL")
			}
			if mode == "row_count" {
				if e.Error() != "OBJECT_OPERATION_STALE" {
					t.Fatal(e)
				}
			} else if mode == "duplicate_row" {
				if e.Error() != "OBJECT_STORED_PROVENANCE_INVALID" || writes != 0 {
					t.Fatal("invalid result changed restored state", e)
				}
			} else if !errors.Is(e, failure) {
				t.Fatal("native outcome replaced", e)
			}
			if mode != "query_failure" && !rows.closed {
				t.Fatal("native rows leaked")
			}
		})
	}
}
