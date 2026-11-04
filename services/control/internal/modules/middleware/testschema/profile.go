//go:build repository_integration

// Package testschema owns explicit candidate versus activated M26 test admission.
// It is compiled only into repository integration controls, never serving code.
package testschema

import (
	"context"
	"io/fs"
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

const Environment = "PROJECT_ONE_MIDDLEWARE_SCHEMA_PROFILE"

func Prepare(t *testing.T, ctx context.Context, f repositoryfixture.Fixture, source fs.FS) {
	t.Helper()
	profile := os.Getenv(Environment)
	if profile == "" {
		profile = "active"
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("bounded schema test context required")
	}
	switch profile {
	case "candidate":
		folder := "schema/" + string(f.Store.Dialect())
		entries, e := fs.ReadDir(source, folder)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			raw, e := fs.ReadFile(source, folder+"/"+entry.Name())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Store.Exec(ctx, transaction.SQL(f.Store.Dialect(), string(raw))); e != nil {
				t.Fatal("actual exact unnumbered M26 candidate statement refused", entry.Name(), e)
			}
		}
	case "active":
		// repositoryfixture has already applied its real compiled owning catalog.
		// Require both fresh actual tables; never create or silently fall back.
		for _, sql := range []string{"SELECT count(*) FROM middleware_instances", "SELECT count(*) FROM middleware_operations"} {
			var n int
			if f.Store.QueryRow(ctx, transaction.SQL(f.Store.Dialect(), sql)).Scan(&n) != nil || n != 0 {
				t.Fatal("actual current activated empty M26 tables required")
			}
		}
	default:
		t.Fatal("unknown explicit M26 schema test profile")
	}
	t.Log("M26_SCHEMA_PROFILE", profile)
}
