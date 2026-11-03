package models

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This source-contract test reads the owned contribution, not a copied schema.
// It models only conditional-key projection and DM's all-NULL exemption; actual
// generated-column metadata, DDL execution and concurrent enforcement need DM.
func TestNativeModelConditionalUniqueness(t *testing.T) {
	imports := nativeConditionalKey(t, "MODEL_IMPORTS", "MODEL_IMPORT_ACTIVE_UK")
	models := nativeConditionalKey(t, "MODEL_VERSIONS", "MODELS_READY_CATALOG_UK")
	row := func(tenant, workspace, state, digest string) map[string]string {
		return map[string]string{"TENANT_ID": tenant, "WORKSPACE_ID": workspace, "STATE": state, "CATALOG_DIGEST": digest}
	}

	t.Run("multiple_terminal_imports_same_and_other_workspace", func(t *testing.T) {
		rows := []map[string]string{
			row("tenant-a", "workspace-a", "ready", "catalog-a"),
			row("tenant-a", "workspace-a", "ready", "catalog-b"),
			row("tenant-a", "workspace-a", "cancelled", "catalog-a"),
			row("tenant-a", "workspace-b", "ready", "catalog-a"),
			row("tenant-a", "workspace-b", "cancelled", "catalog-b"),
		}
		nativeAssertNoKeyCollisions(t, imports, rows)
		// Original partial-NULL key must reproduce the cross-Workspace collision.
		old := imports
		old.columns = []string{"TENANT_ID", "ACTIVE_WORKSPACE_ID"}
		if !old.conflicts(t, rows[0], rows[3]) {
			t.Fatal("negative control missed the original terminal import conflict")
		}
	})

	t.Run("import_lifecycle_and_scope", func(t *testing.T) {
		states := []string{"uploading", "queued", "verifying", "failed", "ready", "cancelled"}
		for _, left := range states {
			for _, right := range states {
				active := func(state string) bool { return state != "ready" && state != "cancelled" }
				want := active(left) && active(right)
				a := row("tenant-a", "workspace-a", left, "catalog-a")
				b := row("tenant-a", "workspace-a", right, "catalog-b")
				if got := imports.conflicts(t, a, b); got != want {
					t.Errorf("same scope %s/%s: conflict=%v, want %v", left, right, got, want)
				}
				for _, scope := range [][2]string{{"tenant-a", "workspace-b"}, {"tenant-b", "workspace-a"}} {
					b = row(scope[0], scope[1], right, "catalog-a")
					if imports.conflicts(t, a, b) {
						t.Errorf("distinct scope %v %s/%s collided", scope, left, right)
					}
				}
			}
		}
	})

	t.Run("multiple_retired_models_same_and_other_workspace", func(t *testing.T) {
		rows := []map[string]string{
			row("tenant-a", "workspace-a", "deleted", "catalog-a"),
			row("tenant-a", "workspace-a", "deleted", "catalog-a"),
			row("tenant-a", "workspace-a", "deleted", "catalog-b"),
			row("tenant-a", "workspace-b", "deleted", "catalog-a"),
			row("tenant-a", "workspace-b", "deleted", "catalog-b"),
			row("tenant-a", "workspace-a", "ready", "catalog-a"),
			row("tenant-a", "workspace-b", "ready", "catalog-a"),
		}
		nativeAssertNoKeyCollisions(t, models, rows)
		old := models
		old.columns = []string{"TENANT_ID", "WORKSPACE_ID", "READY_CATALOG_DIGEST"}
		if !old.conflicts(t, rows[0], rows[2]) {
			t.Fatal("negative control missed the original retired model conflict")
		}
	})

	t.Run("ready_model_catalog_and_scope", func(t *testing.T) {
		for _, state := range []string{"ready", "deleted"} {
			for _, scope := range [][2]string{{"tenant-a", "workspace-a"}, {"tenant-a", "workspace-b"}, {"tenant-b", "workspace-a"}} {
				for _, digest := range []string{"catalog-a", "catalog-b"} {
					a := row("tenant-a", "workspace-a", "ready", "catalog-a")
					b := row(scope[0], scope[1], state, digest)
					want := state == "ready" && scope == [2]string{"tenant-a", "workspace-a"} && digest == "catalog-a"
					if got := models.conflicts(t, a, b); got != want {
						t.Errorf("%s %v %s: conflict=%v, want %v", state, scope, digest, got, want)
					}
				}
			}
		}
	})
}

type nativeSchemaKey struct {
	columns []string
	virtual map[string]string
}

// Read the real nativeTable declaration through Go's parser. Fail explicitly if
// the contribution changes shape; do not silently fall back to expected SQL.
func nativeConditionalKey(t *testing.T, tableName, constraintName string) nativeSchemaKey {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.FromSlash("../../platform/database/dameng/migrate/contribution_models.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	key := nativeSchemaKey{virtual: map[string]string{}}
	found := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || nativeSchemaFunction(call) != "nativeTable" || len(call.Args) < 3 || nativeSchemaString(t, call.Args[0]) != tableName {
			return true
		}
		found++
		columns, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			t.Fatal("expected literal column declaration")
		}
		for _, entry := range columns.Elts {
			column, ok := entry.(*ast.CallExpr)
			if !ok || nativeSchemaFunction(column) != "modelsVirtual" {
				continue
			}
			if len(column.Args) != 4 {
				t.Fatal("unexpected virtual-column declaration")
			}
			key.virtual[nativeSchemaString(t, column.Args[0])] = nativeSchemaString(t, column.Args[3])
		}
		for _, entry := range call.Args[2:] {
			constraint, ok := entry.(*ast.CallExpr)
			if !ok || nativeSchemaFunction(constraint) != "unique" || nativeSchemaString(t, constraint.Args[0]) != constraintName {
				continue
			}
			if key.columns != nil {
				t.Fatal("duplicate conditional unique declaration")
			}
			for _, field := range constraint.Args[1:] {
				key.columns = append(key.columns, nativeSchemaString(t, field))
			}
		}
		return false
	})
	if found != 1 || len(key.columns) == 0 {
		t.Fatalf("missing or ambiguous %s.%s declaration", tableName, constraintName)
	}
	for _, field := range key.columns {
		if _, ok := key.virtual[field]; !ok {
			t.Fatalf("conditional key component %s must be a lifecycle-generated column", field)
		}
	}
	return key
}

func nativeSchemaFunction(call *ast.CallExpr) string {
	if name, ok := call.Fun.(*ast.Ident); ok {
		return name.Name
	}
	return ""
}

func nativeSchemaString(t *testing.T, expression ast.Expr) string {
	t.Helper()
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		t.Fatal("expected literal schema string")
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

var nativeSchemaCase = regexp.MustCompile(`^CASE WHEN "STATE"(='([^']+)'| NOT IN \('([^']+)'(?:,'([^']+)')?\)) THEN "([A-Z_]+)" ELSE NULL END$`)

func (key nativeSchemaKey) projection(t *testing.T, row map[string]string) ([]string, bool) {
	t.Helper()
	values := make([]string, len(key.columns))
	allNull := true
	for i, field := range key.columns {
		if expression, ok := key.virtual[field]; ok {
			parts := nativeSchemaCase.FindStringSubmatch(expression)
			if parts == nil {
				t.Fatalf("unsupported conditional expression: %s", expression)
			}
			selected := row["STATE"] == parts[2]
			if strings.HasPrefix(parts[1], " NOT IN") {
				selected = row["STATE"] != parts[3] && row["STATE"] != parts[4]
			}
			if !selected {
				continue
			}
			field = parts[5]
		}
		value, ok := row[field]
		if !ok || value == "" {
			t.Fatalf("missing non-NULL fixture value for %s", field)
		}
		values[i] = value
		allNull = false
	}
	return values, allNull
}

func (key nativeSchemaKey) conflicts(t *testing.T, a, b map[string]string) bool {
	t.Helper()
	x, xNull := key.projection(t, a)
	y, yNull := key.projection(t, b)
	return !xNull && !yNull && reflect.DeepEqual(x, y)
}

func nativeAssertNoKeyCollisions(t *testing.T, key nativeSchemaKey, rows []map[string]string) {
	t.Helper()
	for i, a := range rows {
		for j := i + 1; j < len(rows); j++ {
			if key.conflicts(t, a, rows[j]) {
				t.Errorf("rows %d/%d must coexist", i, j)
			}
		}
	}
}
