package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func restoredRuntimeFixture() RestoredRuntimeContext {
	return RestoredRuntimeContext{FormatVersion: 1, RestoreID: strings.Repeat("a", 32), SourceScopeID: strings.Repeat("b", 32), TargetScopeID: strings.Repeat("c", 32), BackupManifestSHA256: strings.Repeat("d", 64), SQLArchiveSHA256: strings.Repeat("e", 64), TenantID: "11111111-1111-4111-8111-111111111111", SchemaVersion: 16, TargetDatabase: "synthetic_restore", TargetOperator: "synthetic_operator", Reason: "Explicit isolated restored runtime role"}
}

func TestRestoredRuntimeContextIdentityAndStrictWireGuards(t *testing.T) {
	base := restoredRuntimeFixture()
	if !base.Valid() {
		t.Fatal("valid synthetic restore context")
	}
	for _, mutate := range []func(*RestoredRuntimeContext){
		func(v *RestoredRuntimeContext) { v.FormatVersion = 2 }, func(v *RestoredRuntimeContext) { v.RestoreID = "x" }, func(v *RestoredRuntimeContext) { v.SourceScopeID = v.TargetScopeID },
		func(v *RestoredRuntimeContext) { v.BackupManifestSHA256 = "" }, func(v *RestoredRuntimeContext) { v.SQLArchiveSHA256 = "" }, func(v *RestoredRuntimeContext) { v.TenantID = "00000000-0000-0000-0000-000000000000" },
		func(v *RestoredRuntimeContext) { v.SchemaVersion = 0 }, func(v *RestoredRuntimeContext) { v.TargetDatabase = "postgresql://override" }, func(v *RestoredRuntimeContext) { v.TargetOperator = "customer; DROP ROLE" },
		func(v *RestoredRuntimeContext) { v.Reason = "" }, func(v *RestoredRuntimeContext) { v.Reason = "bad\nreason" },
	} {
		v := base
		mutate(&v)
		if v.Valid() {
			t.Fatal("invalid context accepted")
		}
	}
	for _, raw := range []string{`{"format_version":1,"format_version":1}`, `{"unknown":true}`, `{"reason":null}`} {
		var v RestoredRuntimeContext
		if StrictJSON([]byte(raw), &v) == nil {
			t.Fatal("unsafe wire input")
		}
	}
	raw, _ := json.Marshal(base)
	var v RestoredRuntimeContext
	if StrictJSON(raw, &v) != nil || v != base {
		t.Fatal("typed context roundtrip")
	}
}
