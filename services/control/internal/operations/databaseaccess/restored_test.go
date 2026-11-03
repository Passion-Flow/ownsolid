package databaseaccess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoredContextFixture() release.RestoredRuntimeContext {
	return release.RestoredRuntimeContext{FormatVersion: 1, RestoreID: strings.Repeat("a", 32), SourceScopeID: strings.Repeat("b", 32), TargetScopeID: strings.Repeat("c", 32), BackupManifestSHA256: strings.Repeat("d", 64), SQLArchiveSHA256: strings.Repeat("e", 64), TenantID: "11111111-1111-4111-8111-111111111111", SchemaVersion: 16, TargetDatabase: "synthetic_restore", TargetOperator: "synthetic_operator", Reason: "Explicit isolated restored runtime role"}
}

func TestProtectedRestoredRuntimeContextFiles(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	name := filepath.Join(dir, "restored-runtime.json")
	raw, _ := json.Marshal(restoredContextFixture())
	if os.WriteFile(name, raw, 0600) != nil {
		t.Fatal("fixture")
	}
	if _, e := LoadRestoredContext(name); e != nil {
		t.Fatal(e)
	}
	os.Chmod(name, 0644)
	if _, e := LoadRestoredContext(name); e == nil {
		t.Fatal("public context accepted")
	}
	os.Chmod(name, 0600)
	if os.Link(name, name+"-linked") != nil {
		t.Fatal("fixture")
	}
	if _, e := LoadRestoredContext(name); e == nil {
		t.Fatal("linked context accepted")
	}
	os.Remove(name + "-linked")
	for _, bad := range []string{`{"unknown":"secret_marker"}`, `{"format_version":1,"format_version":1}`, `null`} {
		if os.WriteFile(name, []byte(bad), 0600) != nil {
			t.Fatal("fixture")
		}
		_, e := LoadRestoredContext(name)
		if e == nil || strings.Contains(e.Error(), "secret_marker") {
			t.Fatal("invalid context/redaction", e)
		}
	}
}

func TestActualRestoredRuntimeRolePreservesFreshGuardAndRefusesWrongContext(t *testing.T) {
	f := newActualFixture(t)
	identityfixture.Bootstrap(t, f.service)
	ctx := context.Background()
	r := restoredContextFixture()
	pc := f.service.DB.Config().ConnConfig
	r.TargetDatabase, r.TargetOperator = pc.Database, pc.User
	if e := f.lease.QueryRow(ctx, "SELECT tenant_id::text FROM installation_state WHERE id=1").Scan(&r.TenantID); e != nil {
		t.Fatal(e)
	}
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e == nil || e.Error() != "RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED" {
		t.Fatal("fresh guard weakened", e)
	}
	wrong := r
	wrong.TargetDatabase = "foreign_target"
	if _, e := PrepareRestored(ctx, f.service.DB, f.lease, f.input, wrong); e == nil {
		t.Fatal("wrong database accepted")
	}
	wrong = r
	wrong.TenantID = "22222222-2222-4222-8222-222222222222"
	if _, e := PrepareRestored(ctx, f.service.DB, f.lease, f.input, wrong); e == nil {
		t.Fatal("wrong tenant accepted")
	}
	if state, e := PrepareRestored(ctx, f.service.DB, f.lease, f.input, r); e != nil || state != Verified {
		t.Fatal("restored role preparation", e)
	}
	if state, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || state != Verified {
		t.Fatal("independent restored role authentication", e)
	}
	if state, e := PrepareRestored(ctx, f.service.DB, f.lease, f.input, r); e != nil || state != Verified {
		t.Fatal("exact observed repeat", e)
	}
	var count int
	if e := f.service.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='operator.runtime_access_restored'").Scan(&count); e != nil || count != 1 {
		t.Fatal("restored creation audit repeat", e)
	}
}
