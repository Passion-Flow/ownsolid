package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit namespace/SQL-result double; actual task integration is tagged separately.
func (d *databaseActionDouble) InspectStoreTaskResult(_ context.Context, t engine.DatabaseTask, _ string) (engine.StoreTaskObservation, error) {
	role, _, _ := engine.StoreTaskContext(t.Kind)
	kind, id := "", "bcd9b6c4-f70a-4e0f-931c-892f5e6a3bed"
	if role == "training" {
		kind, id = "training", "c8f9f8d3-f70a-4e0f-931c-892f5e6a3bed"
	}
	if role == "preparations" {
		kind, id = "model_preparation", "d8f9f8d3-f70a-4e0f-931c-892f5e6a3bed"
	}
	ns := engine.StoreNamespace{SchemaVersion: 1, Kind: kind, StoreID: id, TenantID: "d8c8f7db-1b2b-45b6-8583-6d72f3f57c38"}
	if d.actionFault == "store-namespace" {
		ns.StoreID = "e8f9f8d3-f70a-4e0f-931c-892f5e6a3bed"
	}
	return engine.StoreTaskObservation{Status: "observed_private_fresh_store_namespace", Namespace: ns, NamespaceSHA256: wire.Digest(encode(ns)), Qualification: "private_namespace_bytes_only_not_backend_audit_schema_scope_application_or_target_qualification"}, nil
}
func storeScopeFixture(t *testing.T) (configuration.Config, *databaseActionDouble, *databaseWorkerDouble) {
	t.Helper()
	c, d, w := bootstrapScopeFixture(t, true)
	ctx := context.Background()
	for _, kind := range []string{engine.DatabaseBootstrap, engine.DatabaseBootstrapReconcile} {
		if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
			t.Fatal(e)
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
func TestScopedStoresRequireOrderedIndependentChecksAndPreserveHistoricalNamespaces(t *testing.T) {
	c, d, w := storeScopeFixture(t)
	ctx := context.Background()
	if _, e := processRuntime(ctx, c, d, w, "running", false); e == nil || e.Error() != "OFFLINE_SCOPE_STORE_QUALIFICATION_REQUIRED" {
		t.Fatal("store gate bypassed", e)
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.TrainingStoreInitialize, false); e == nil {
		t.Fatal("dataset skipped")
	}
	for _, step := range []struct{ role, init, check string }{{"datasets", engine.DatasetStoreInitialize, engine.DatasetStoreCheck}, {"training", engine.TrainingStoreInitialize, engine.TrainingStoreCheck}, {"preparations", engine.PreparationStoreInitialize, engine.PreparationStoreCheck}} {
		for _, kind := range []string{step.init, step.check} {
			if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
				t.Fatal(kind, e)
			}
			if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, kind); e != nil {
				t.Fatal(kind, e)
			}
			if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, kind); e != nil {
				t.Fatal(kind, e)
			}
			s, e := Read(ctx, c)
			if e != nil || s.Stores[step.role].Qualified != (kind == step.check) {
				t.Fatal(s, e)
			}
		}
	}
	s, e := Read(ctx, c)
	if e != nil || len(s.Stores) != 3 || !s.BootstrapQualified || !s.SchemaQualified {
		t.Fatal(s, e)
	}
	for _, role := range orderedStoreRoles {
		if !s.Stores[role].Qualified || s.Stores[role].Namespace == nil {
			t.Fatal("historical store proof lost", role)
		}
	}
	if _, e = createDatabaseTask(ctx, c, d, w, engine.DatasetStoreRecord, false); e == nil {
		t.Fatal("successful sequence replayed")
	}
	if _, e = processRuntime(ctx, c, d, w, "running", false); e != nil {
		t.Fatal("qualified stores did not open the process gate", e)
	}
}
func TestScopedStoreFailedCheckRequiresExplicitRecordAndSameNamespace(t *testing.T) {
	c, d, w := storeScopeFixture(t)
	ctx := context.Background()
	for i, kind := range []string{engine.DatasetStoreInitialize, engine.DatasetStoreCheck, engine.DatasetStoreRecord, engine.DatasetStoreCheck} {
		if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
			t.Fatal(i, e)
		}
		d.actionFault = ""
		if i == 1 {
			d.actionFault = "exit"
		}
		_, e := databaseTaskAction(ctx, c, d, w, "execute", false, kind)
		if (e == nil) != (i != 1) {
			t.Fatal(i, e)
		}
		if _, e = databaseTaskAction(ctx, c, d, w, "removed", false, kind); e != nil {
			t.Fatal(e)
		}
		s, e := Read(ctx, c)
		if e != nil || s.Stores["datasets"].Qualified != (i == 3) {
			t.Fatal(s, e)
		}
	}
}
func TestScopedStoreChangedNamespaceNeverQualifiesAndPreservesIntent(t *testing.T) {
	c, d, w := storeScopeFixture(t)
	ctx := context.Background()
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatasetStoreInitialize, false); e != nil {
		t.Fatal(e)
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatasetStoreInitialize); e != nil {
		t.Fatal(e)
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatasetStoreInitialize); e != nil {
		t.Fatal(e)
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatasetStoreCheck, false); e != nil {
		t.Fatal(e)
	}
	d.actionFault = "store-namespace"
	if r, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatasetStoreCheck); e == nil || !r.Pending {
		t.Fatal("changed namespace qualified", r, e)
	}
	starts := d.starts
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatasetStoreCheck); e == nil || d.starts != starts {
		t.Fatal("unknown namespace result reexecuted")
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if string(before) != string(after) {
		t.Fatal("repeat mutated journal")
	}
	d.actionFault = ""
	if _, e := databaseTaskAction(ctx, c, d, w, "", true, engine.DatasetStoreCheck); e != nil {
		t.Fatal("exact retained namespace reconcile", e)
	}
}
func TestScopedStoreRequestAndForgedOutputLineageRefused(t *testing.T) {
	for _, raw := range []string{`{}`, `{"format_version":1,"reason":""}`, `{"format_version":1,"reason":"a","unknown":true}`, `{"format_version":1,"reason":"a","reason":"b"}`, `{"format_version":2,"reason":"a"}`} {
		if _, e := storeRequest([]engine.ConfigurationFile{{Name: "store-request.json", Contents: []byte(raw)}}); e == nil {
			t.Fatal("invalid operation accepted", raw)
		}
	}
	c, d, w := storeScopeFixture(t)
	if _, e := createDatabaseTask(context.Background(), c, d, w, engine.DatasetStoreInitialize, false); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	original := databaseTaskState(l.events).record
	for _, change := range []func(*DatabaseTaskRecord){func(r *DatabaseTaskRecord) { r.OutputVolumeSHA256 = "forged" }, func(r *DatabaseTaskRecord) { r.PreviousTaskSHA256 = "forged" }, func(r *DatabaseTaskRecord) { r.Task.OutputOwner.Role = "training" }, func(r *DatabaseTaskRecord) { r.Task.Reason = "" }} {
		var r DatabaseTaskRecord
		json.Unmarshal(encode(*original), &r)
		change(&r)
		if validDatabaseTaskRecord(l.events, &r, l.marker.ScopeID) {
			t.Fatal("forged store context accepted")
		}
	}
}
