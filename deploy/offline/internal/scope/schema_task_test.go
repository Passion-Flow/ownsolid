package scope

import (
	"bytes"
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func TestMigrationRecordEncodingKeepsExistingJournalHash(t *testing.T) {
	r := DatabaseTaskRecord{DescriptorSHA256: "descriptor", VolumeSHA256: "volume", RuntimeSHA256: "runtime", Task: engine.DatabaseTask{Kind: engine.DatabaseMigrate}, ID: "container"}
	legacy := struct {
		DescriptorSHA256 string              `json:"descriptor_sha256"`
		VolumeSHA256     string              `json:"volume_sha256"`
		RuntimeSHA256    string              `json:"runtime_sha256"`
		Task             engine.DatabaseTask `json:"task"`
		ID               string              `json:"id,omitempty"`
	}{r.DescriptorSHA256, r.VolumeSHA256, r.RuntimeSHA256, r.Task, r.ID}
	if !bytes.Equal(encode(r), encode(legacy)) {
		t.Fatal("optional predecessor changed existing migration record hashes")
	}
}

func TestSchemaQualificationRequiresMigrationCheckAndRemoval(t *testing.T) {
	c, d, w := databaseActionFixture(t)
	ctx := context.Background()
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseSchemaCheck, false); e == nil {
		t.Fatal("check created before migration/removal")
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "execute", false); e != nil {
		t.Fatal(e)
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseSchemaCheck, false); e == nil {
		t.Fatal("check created before migration removal")
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "removed", false); e != nil {
		t.Fatal(e)
	}
	created, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseSchemaCheck, false)
	if e != nil || created.Pending {
		t.Fatal(created, e)
	}
	if _, e = databaseTaskAction(ctx, c, d, w, "execute", false); e == nil || e.Error() != "OFFLINE_SCOPE_DATABASE_TASK_KIND_MISMATCH" {
		t.Fatal("migration command executed schema task", e)
	}
	if _, e = createDatabaseTask(ctx, c, d, w, engine.DatabaseMigrate, false); e == nil {
		t.Fatal("migration command changed task kind")
	}
	r, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseSchemaCheck)
	if e != nil || !r.ProcessSucceeded || r.SchemaQualified || d.starts != 2 {
		t.Fatal(r, e)
	}
	s, e := Read(ctx, c)
	if e != nil || !s.MigrationProcessSucceeded || !s.SchemaProcessSucceeded || s.SchemaQualified {
		t.Fatal(s, e)
	}
	if _, e = processRuntime(ctx, c, d, w, "running", false); e == nil {
		t.Fatal("unremoved check opened startup gate")
	}
	r, e = databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseSchemaCheck)
	if e != nil || !r.SchemaQualified || !r.Removed {
		t.Fatal(r, e)
	}
	s, e = Read(ctx, c)
	if e != nil || !s.SchemaQualified || s.Status != "isolated_scope_schema_qualified" {
		t.Fatal(s, e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	proof, ok := migrationProof(l.events)
	current := databaseTaskState(l.events).record
	qualified := schemaQualified(l.events)
	l.close()
	if !ok || !qualified || current.MigrationSHA256 != proof || current.Task.Kind != engine.DatabaseSchemaCheck {
		t.Fatal("missing migration proof")
	}
	if _, e = databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseSchemaCheck); e != nil || d.removes != 2 {
		t.Fatal("qualified removal replay", e)
	}
}
func TestFailedSchemaCheckAndChangedProofCannotQualify(t *testing.T) {
	c, d, w := databaseActionFixture(t)
	ctx := context.Background()
	if _, e := databaseTaskAction(ctx, c, d, w, "execute", false); e != nil {
		t.Fatal(e)
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "removed", false); e != nil {
		t.Fatal(e)
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseSchemaCheck, false); e != nil {
		t.Fatal(e)
	}
	d.actionFault = "exit"
	if r, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseSchemaCheck); e == nil || r.SchemaQualified {
		t.Fatal("failed check qualified")
	}
	if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, engine.DatabaseSchemaCheck); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	if schemaQualified(l.events) {
		t.Fatal("failed check removal qualified")
	}
	current := *databaseTaskState(l.events).record
	current.MigrationSHA256 = "changed"
	if validDatabaseTaskRecord(l.events, &current, l.marker.ScopeID) {
		t.Fatal("changed predecessor accepted")
	}
}
