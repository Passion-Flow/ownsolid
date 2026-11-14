//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
	base "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/knowledge-workflow-portal"
	plugin "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/plugin-tool"
	closure "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/runtime-owner-closure"
)

// Private203 catalog: every source byte is an actual selected migration asset;
// every expected metadata byte comes from the protected actual measurement.
func rclMeasuredCatalog(t *testing.T) Catalog {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_RCL_EVOLUTION")
	expected := os.Getenv("PROJECT_ONE_RCL_EVOLUTION_SHA256")
	resolved, e := filepath.EvalSymlinks(path)
	info, se := os.Lstat(path)
	if e != nil || se != nil || !filepath.IsAbs(path) || resolved != path || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maximumCatalogBytes || !checksum.MatchString(expected) {
		t.Fatal("RCL_EXACT_PRIVATE_MEASURED_METADATA_REQUIRED")
	}
	raw, e := os.ReadFile(path)
	if e != nil || hash(string(raw)) != expected {
		t.Fatal("RCL_ACTUAL_METADATA_CHANGED")
	}
	canonical, e := CanonicalizeMeasuredEvolution(raw)
	if e != nil {
		t.Fatal("RCL actual canonicalization", e)
	}
	var measured, active evolutionDocument
	old, e := assets.Files.ReadFile("evolution.json")
	if e != nil || json.Unmarshal(old, &active) != nil || json.Unmarshal(canonical, &measured) != nil || len(measured.Steps) != 188 {
		t.Fatal("RCL_ACTUAL188_EVOLUTION_ENTRIES_REQUIRED")
	}
	if len(active.Steps) != 134 && len(active.Steps) != 179 && len(active.Steps) != 188 && len(active.Steps) != 250 {
		t.Fatal("RCL_EXACT_REVIEWED_ACTIVE_PREFIX_REQUIRED")
	}
	// New active statements do not enter the retained private203 fixture.
	for n, v := range active.Steps[:min(len(active.Steps), len(measured.Steps))] {
		if measured.Steps[n] != v {
			t.Fatal("RCL measured active prefix changed", n)
		}
	}
	reference, e := assets.Files.ReadFile("reference.json")
	var original referenceDocument
	if e != nil || json.Unmarshal(reference, &original) != nil {
		t.Fatal("RCL_IMMUTABLE_BASE_REFERENCE_REQUIRED", e)
	}
	allowed := map[string]bool{original.Journal.File: true}
	for _, item := range append(original.Steps, measured.Steps...) {
		allowed[item.File] = true
	}
	source := fstest.MapFS{}
	for _, files := range []fs.FS{assets.Files, base.Files, plugin.Files, closure.Files} {
		names, e := fs.Glob(files, "*.sql")
		if e != nil {
			t.Fatal(e)
		}
		for _, name := range names {
			if !allowed[name] {
				continue
			}
			raw, e := fs.ReadFile(files, name)
			if e != nil {
				t.Fatal(e)
			}
			if existing := source[name]; existing != nil {
				if string(existing.Data) != string(raw) {
					t.Fatal("RCL_CONFLICTING_ACTUAL_SOURCE")
				}
				continue
			}
			source[name] = &fstest.MapFile{Data: raw, Mode: 0444}
		}
	}
	c, e := loadEvolution(reference, canonical, source)
	if e != nil || c.Steps() != 203 {
		t.Fatal("RCL_ACTUAL203_PRIVATE_CATALOG_REQUIRED", e)
	}
	return c
}
func rclActual(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
		t.Fatal("RCL_ACTUAL_EXCLUSIVE_FIXTURE_REQUIRED")
	}
	s, _, e := mysqlstore.Open(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	return s, rclMeasuredCatalog(t)
}
func rclRun(t *testing.T, s *mysqlstore.Store, c Catalog, mode Mode) Report {
	t.Helper()
	r, e := Run(t.Context(), s, c, candidateOptions(c), mode, nil)
	if e != nil || !r.Ready || !r.LockReleased || r.AppliedSteps != c.Steps() {
		t.Fatal("RCL actual owning journal/readiness", r, e)
	}
	return r
}
func rclNoReady(t *testing.T, s *mysqlstore.Store, c Catalog, mode Mode, code string) {
	t.Helper()
	before := candidateData(t, s)
	r, e := Run(t.Context(), s, c, candidateOptions(c), mode, nil)
	if e == nil || e.Error() != code || r.Ready || !r.LockReleased || candidateData(t, s) != before {
		t.Fatal("RCL fail-closed unchanged graph/journal", r, e)
	}
}

func TestActualMySQLRuntimeOwnerMigration(t *testing.T) {
	t.Run("RCL08_fresh203_repeat_readiness", func(t *testing.T) {
		s, c := rclActual(t)
		rclRun(t, s, c, Apply)
		before := candidateData(t, s)
		events := 0
		r, e := Run(t.Context(), s, c, candidateOptions(c), Apply, func(Event) { events++ })
		if e != nil || !r.Ready || events != 0 || candidateData(t, s) != before {
			t.Fatal("RCL actual replay changed state", r, e)
		}
		rclRun(t, s, c, Readiness)
	})
	t.Run("RCL08_dirty195_before_ddl", func(t *testing.T) {
		s, c := rclActual(t)
		rclRun(t, s, candidatePrefix(c, 194), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 195 && v.Stage == Applying {
				cancel()
			}
		})
		if e == nil || r.DirtyStep != 195 || !r.LockReleased {
			t.Fatal("RCL pre-DDL durable intent", r, e)
		}
		rclNoReady(t, s, c, Apply, CodeReconciliationRequired)
		rclRun(t, s, c, Reconcile)
		rclRun(t, s, c, Readiness)
	})
	t.Run("RCL08_unknown200_after_ddl", func(t *testing.T) {
		s, c := rclActual(t)
		rclRun(t, s, candidatePrefix(c, 199), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 200 && v.Stage == Verified {
				cancel()
			}
		})
		if e == nil || r.DirtyStep != 200 || !r.LockReleased {
			t.Fatal("RCL actual FK committed unknown journal outcome", r, e)
		}
		rclNoReady(t, s, c, Apply, CodeReconciliationRequired)
		rclRun(t, s, c, Reconcile)
		rclRun(t, s, c, Readiness)
	})
	t.Run("RCL08_unknown203_after_trigger", func(t *testing.T) {
		s, c := rclActual(t)
		rclRun(t, s, candidatePrefix(c, 202), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 203 && v.Stage == Verified {
				cancel()
			}
		})
		if e == nil || r.DirtyStep != 203 || !r.LockReleased {
			t.Fatal("RCL actual trigger committed unknown journal outcome", r, e)
		}
		rclNoReady(t, s, c, Apply, CodeReconciliationRequired)
		rclRun(t, s, c, Reconcile)
		rclRun(t, s, c, Readiness)
	})
	t.Run("RCL08_missing_guard_drift", func(t *testing.T) {
		s, c := rclActual(t)
		rclRun(t, s, c, Apply)
		migrationExec(t, s, "DROP TRIGGER alias_intent_immutable_update")
		rclNoReady(t, s, c, Readiness, CodeDefinitionDrift)
		rclNoReady(t, s, c, Reconcile, CodeDefinitionDrift)
	})
	t.Run("RCL08_prepare_nonempty_prefix_graph", func(t *testing.T) {
		s, c := rclActual(t)
		rclRun(t, s, c, Apply)
		candidateRecoverySeed(t, s)
		rclRun(t, s, c, Readiness)
	})
}

// The owning Python runner performs an actual stopped restart and logical
// export/fresh restore. These roots only read retained rows and actual metadata.
func TestActualMySQLRuntimeOwnerRecovery(t *testing.T) {
	for _, leaf := range []string{"RCL08_graph_readback", "RCL08_after_restart_readback", "RCL08_fresh_restore_readback", "RCL08_restored_guard_refusal"} {
		t.Run(leaf, func(t *testing.T) {
			s, c := rclActual(t)
			if leaf == "RCL08_restored_guard_refusal" {
				migrationExec(t, s, "DROP TRIGGER alias_intent_immutable_delete")
				rclNoReady(t, s, c, Readiness, CodeDefinitionDrift)
				return
			}
			rclRun(t, s, c, Readiness)
			for _, table := range []string{"managed_components", "component_log_snapshots", "api_keys", "gateway_requests", "gateway_recoveries", "model_alias_cutover_intents"} {
				var n int
				if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); e != nil || n < 1 {
					t.Fatal("RCL actual nonempty retained owner", table, e)
				}
			}
			t.Log("MYSQL_RUNTIME_OWNER_DATA_SHA256=" + candidateData(t, s))
		})
	}
}
