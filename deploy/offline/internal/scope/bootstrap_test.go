package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit secret/result double for the journal and real protected-copy filesystem.
func (d *databaseActionDouble) ReadBootstrapIntent(context.Context, engine.DatabaseTask, string) (engine.BootstrapIntent, error) {
	b := []byte(`{"synthetic_secret":"private-test-only"}`)
	return engine.BootstrapIntent{Contents: b, Bytes: len(b), SHA256: wire.Digest(b)}, nil
}
func bootstrapScopeFixture(t *testing.T, withStores ...bool) (configuration.Config, *databaseActionDouble, *databaseWorkerDouble) {
	t.Helper()
	c, d, w := accessScopeFixture(t, append([]bool{true}, withStores...)...)
	ctx := context.Background()
	for _, kind := range []string{engine.DatabaseRuntimePrepare, engine.DatabaseRuntimeCheck} {
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
func TestScopedBootstrapIndependentQualificationAndPrivateCopy(t *testing.T) {
	c, d, w := bootstrapScopeFixture(t)
	ctx := context.Background()
	if _, e := processRuntime(ctx, c, d, w, "running", false); e == nil || e.Error() != "OFFLINE_SCOPE_BOOTSTRAP_QUALIFICATION_REQUIRED" {
		t.Fatal("bootstrap gate bypassed", e)
	}
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseBootstrapReconcile, false); e == nil {
		t.Fatal("bootstrap skipped")
	}
	for _, kind := range []string{engine.DatabaseBootstrap, engine.DatabaseBootstrapReconcile} {
		if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
			t.Fatal(e)
		}
		r, e := databaseTaskAction(ctx, c, d, w, "execute", false, kind)
		if e != nil || !r.ProcessSucceeded || r.BootstrapQualified {
			t.Fatal(r, e)
		}
		r, e = databaseTaskAction(ctx, c, d, w, "removed", false, kind)
		if e != nil || r.BootstrapQualified != (kind == engine.DatabaseBootstrapReconcile) {
			t.Fatal(r, e)
		}
	}
	s, e := Read(ctx, c)
	if e != nil || !s.BootstrapQualified || !s.SchemaQualified || !s.RuntimeAccessQualified || s.BootstrapCredentialFile != bootstrapCopyName {
		t.Fatal(s, e)
	}
	raw, e := os.ReadFile(filepath.Join(c.ScopeRoot, bootstrapCopyName))
	if e != nil || !bytes.Contains(raw, []byte("private-test-only")) {
		t.Fatal("protected copy missing", e)
	}
	if bytes.Contains(encode(s), []byte("private-test-only")) {
		t.Fatal("secret status exposed")
	}
	if _, e = createDatabaseTask(ctx, c, d, w, engine.DatabaseBootstrapResume, false); e == nil {
		t.Fatal("successful check allowed another resume")
	}
	if e = os.WriteFile(filepath.Join(c.ScopeRoot, bootstrapCopyName), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(ctx, c); e == nil {
		t.Fatal("changed copy accepted")
	}
}
func TestScopedBootstrapFailedCheckExplicitResumeKeepsCredential(t *testing.T) {
	c, d, w := bootstrapScopeFixture(t)
	ctx := context.Background()
	for i, kind := range []string{engine.DatabaseBootstrap, engine.DatabaseBootstrapReconcile, engine.DatabaseBootstrapResume, engine.DatabaseBootstrapReconcile} {
		if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
			t.Fatal(i, e)
		}
		if i < 2 {
			d.actionFault = "exit"
		} else {
			d.actionFault = ""
		}
		r, e := databaseTaskAction(ctx, c, d, w, "execute", false, kind)
		if (e == nil) != (i >= 2) || r.BootstrapQualified {
			t.Fatal("failure incorrectly qualified", i, r, e)
		}
		r, e = databaseTaskAction(ctx, c, d, w, "removed", false, kind)
		if e != nil || r.BootstrapQualified != (i == 3) {
			t.Fatal(i, r, e)
		}
	}
}
func TestBootstrapCopyRejectsUnsafeExistingFileAndMissingRecordedCopy(t *testing.T) {
	for _, fault := range []string{"mode", "symlink", "hardlink", "changed", "absent"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := bootstrapScopeFixture(t)
			ctx := context.Background()
			if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseBootstrap, false); e != nil {
				t.Fatal(e)
			}
			if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, engine.DatabaseBootstrap); e != nil {
				t.Fatal(e)
			}
			p := filepath.Join(c.ScopeRoot, bootstrapCopyName)
			switch fault {
			case "mode":
				os.Chmod(p, 0644)
			case "symlink":
				os.Rename(p, p+".retained")
				os.Symlink(p+".retained", p)
			case "hardlink":
				os.Link(p, filepath.Join(filepath.Dir(c.ScopeRoot), "link"))
			case "changed":
				os.WriteFile(p, []byte("changed"), 0600)
			case "absent":
				os.Remove(p)
			}
			if _, e := Read(ctx, c); e == nil {
				t.Fatal("unsafe copy accepted")
			}
		})
	}
}

func TestScopedBootstrapRejectsForgedPredecessorVolumeAndOutputIdentity(t *testing.T) {
	c, d, w := bootstrapScopeFixture(t)
	ctx := context.Background()
	if _, e := createDatabaseTask(ctx, c, d, w, engine.DatabaseBootstrap, false); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	original := databaseTaskState(l.events).record
	for _, change := range []func(*DatabaseTaskRecord){
		func(r *DatabaseTaskRecord) { r.PreviousTaskSHA256 = wire.Digest([]byte("forged")) },
		func(r *DatabaseTaskRecord) { r.OutputVolumeSHA256 = wire.Digest([]byte("forged")) },
		func(r *DatabaseTaskRecord) { r.SchemaSHA256 = wire.Digest([]byte("forged")) },
		func(r *DatabaseTaskRecord) { r.AccessSHA256 = wire.Digest([]byte("forged")) },
		func(r *DatabaseTaskRecord) { r.Task.OutputOwner = nil },
		func(r *DatabaseTaskRecord) { r.Task.OutputOwner.Role = "models" },
	} {
		var copied DatabaseTaskRecord
		if e = json.Unmarshal(encode(*original), &copied); e != nil {
			t.Fatal(e)
		}
		change(&copied)
		if validDatabaseTaskRecord(l.events, &copied, l.marker.ScopeID) {
			t.Fatal("forged bootstrap context accepted")
		}
	}
}
