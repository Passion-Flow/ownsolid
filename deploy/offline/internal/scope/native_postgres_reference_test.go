package scope

import (
	"encoding/json"
	"testing"
)

func TestNativePostgreSQLReferenceOmissionPreservesLegacyResultShape(t *testing.T) {
	for name, value := range map[string]any{
		"backup": BackupResult{}, "source": RestoreSourceResult{},
		"sql": RestoreSQLResult{}, "database": RestoreDatabaseResult{}, "stores": RestoreStoresResult{},
	} {
		t.Run(name, func(t *testing.T) {
			raw, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil {
				t.Fatal("result unavailable")
			}
			if _, present := fields["native_postgresql"]; present {
				t.Fatal("legacy result acquired a native reference")
			}
			if _, present := fields["release_manifest_sha256"]; present {
				t.Fatal("legacy result acquired an unverified signed source digest")
			}
		})
	}
}
