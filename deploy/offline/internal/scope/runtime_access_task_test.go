package scope

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func accessScopeFixture(t *testing.T, withBootstrap ...bool) (configuration.Config, *databaseActionDouble, *databaseWorkerDouble) {
	t.Helper()
	c, d, w := databaseActionFixture(t, withBootstrap...)
	ctx := context.Background()
	for _, kind := range []string{engine.DatabaseMigrate, engine.DatabaseSchemaCheck} {
		if kind == engine.DatabaseSchemaCheck {
			if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, kind); e != nil {
			t.Fatal(e)
		}
		if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, kind); e != nil {
			t.Fatal(e)
		}
	}
	return c, d, w
}
func TestScopedAccessQualificationRequiresOrderedIndependentCheckRemoval(t *testing.T) {
	c, d, w := accessScopeFixture(t)
	ctx := context.Background()
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseRuntimeCheck, false); e == nil {
		t.Fatal("access preparation skipped")
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseRuntimePrepare, false); e != nil {
		t.Fatal(e)
	}
	s, e := Read(ctx, c)
	if e != nil || !s.SchemaQualified || !s.MigrationProcessSucceeded || s.RuntimeAccessQualified {
		t.Fatal("historical schema lost", s, e)
	}
	if _, e = databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseSchemaCheck); e == nil {
		t.Fatal("wrong-kind execute")
	}
	if _, e = databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseRuntimePrepare); e != nil {
		t.Fatal(e)
	}
	if _, e = createDatabaseTask(ctx, c, d, w, engine.DatabaseRuntimeCheck, false); e == nil {
		t.Fatal("unremoved preparation advanced")
	}
	if _, e = databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseRuntimePrepare); e != nil {
		t.Fatal(e)
	}
	if _, e = createDatabaseTask(ctx, c, d, w, engine.DatabaseRuntimeCheck, false); e != nil {
		t.Fatal(e)
	}
	r, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseRuntimeCheck)
	if e != nil || !r.ProcessSucceeded || r.RuntimeAccessQualified || !r.SchemaQualified {
		t.Fatal(r, e)
	}
	if _, e = processRuntime(ctx, c, d, w, "running", false); e == nil {
		t.Fatal("unremoved access check opened application gate")
	}
	r, e = databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseRuntimeCheck)
	if e != nil || !r.RuntimeAccessQualified || !r.Removed {
		t.Fatal(r, e)
	}
	s, e = Read(ctx, c)
	if e != nil || !s.RuntimeAccessQualified || !s.SchemaQualified || !s.AccessPreparationSucceeded || !s.AccessCheckSucceeded || s.Status != "isolated_scope_runtime_access_qualified" {
		t.Fatal(s, e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	record := databaseTaskState(l.events).record
	copy := *record
	copy.AccessSHA256 = "changed"
	if validDatabaseTaskRecord(l.events, &copy, l.marker.ScopeID) {
		t.Fatal("forged access proof accepted")
	}
	copy = *record
	copy.SchemaSHA256 = "changed"
	if validDatabaseTaskRecord(l.events, &copy, l.marker.ScopeID) {
		t.Fatal("forged schema proof accepted")
	}
}
func TestScopedFailedAccessCheckNeverQualifiesOrRepairs(t *testing.T) {
	c, d, w := accessScopeFixture(t)
	ctx := context.Background()
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseRuntimePrepare, false); e != nil {
		t.Fatal(e)
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseRuntimePrepare); e != nil {
		t.Fatal(e)
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseRuntimePrepare); e != nil {
		t.Fatal(e)
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseRuntimeCheck, false); e != nil {
		t.Fatal(e)
	}
	d.actionFault = "exit"
	if r, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseRuntimeCheck); e == nil || r.RuntimeAccessQualified {
		t.Fatal("failed check qualified")
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseRuntimeCheck); e != nil {
		t.Fatal(e)
	}
	s, e := Read(ctx, c)
	if e != nil || s.RuntimeAccessQualified || !s.SchemaQualified {
		t.Fatal(s, e)
	}
	if _, e = processRuntime(ctx, c, d, w, "running", false); e == nil || e.Error() != "OFFLINE_SCOPE_RUNTIME_ACCESS_QUALIFICATION_REQUIRED" {
		t.Fatal("failed access gate bypassed", e)
	}
}
