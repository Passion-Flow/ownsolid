package release

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func postgresApplicationRequestFixture() (PostgreSQLBackupRequest, PostgreSQLRestoreRequest) {
	p := PostgreSQLBackupPolicy{DumpFile: "/tools/pg_dump", DumpSHA256: strings.Repeat("a", 64), RestoreFile: "/tools/pg_restore", RestoreSHA256: strings.Repeat("b", 64), ControlFile: "/tools/control", ControlSHA256: strings.Repeat("c", 64), ExpectedVersion: "18.6", ScratchRoot: "/private/scratch", OperationSeconds: 300, TerminationGraceSeconds: 2, MaximumStoreBytes: 1 << 30, MaximumStoreFiles: 1000, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 1 << 20, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 32 << 30}
	b := PostgreSQLBackupRequest{FormatVersion: 1, BackupID: strings.Repeat("a", 32), SourceScopeID: strings.Repeat("b", 32), MaintenanceSHA256: strings.Repeat("a", 64), SourceManifestSHA256: strings.Repeat("b", 64), ControlImageSHA256: strings.Repeat("c", 64), PostgresImageSHA256: strings.Repeat("d", 64), TenantID: "11111111-1111-4111-8111-111111111111", Database: "synthetic_database", Operator: "synthetic_operator", RuntimeConfigurationFile: "/private/config/runtime.yaml", ConfigurationRoot: "/private/config", BackupRoot: "/private/backup", Reason: "Synthetic profile-bound backup", Policy: p}
	r := PostgreSQLRestoreRequest{FormatVersion: 1, RestoreID: strings.Repeat("c", 32), TargetScopeID: strings.Repeat("d", 32), SourceManifestSHA256: strings.Repeat("e", 64), BackupRoot: "/private/backup", RestoreRoot: "/private/restore", TargetDatabase: "synthetic_target", TargetOperator: "synthetic_operator", RuntimeConfigurationFile: "/private/config/runtime.yaml", ConfigurationRoot: "/private/config", Reason: "Synthetic profile-bound restore", Policy: p}
	return b, r
}

func TestPostgresNativeApplicationSelectionPreservesLegacyOmission(t *testing.T) {
	for _, profile := range []string{"", PostgreSQLLegacyApplicationProfile, MVPPostgreSQLCPUApplicationProfile} {
		backup, restore := postgresApplicationRequestFixture()
		backup.ApplicationProfile, restore.ApplicationProfile = profile, profile
		if !backup.Valid() || !restore.Valid() {
			t.Fatal("valid explicit/legacy selector refused", profile)
		}
		want := profile
		if want == "" {
			want = PostgreSQLLegacyApplicationProfile
		}
		if backup.EffectiveApplicationProfile() != want || restore.EffectiveApplicationProfile() != want {
			t.Fatal("effective selector changed", profile)
		}
		for _, request := range []any{backup, restore} {
			raw, e := json.Marshal(request)
			if e != nil || bytes.Contains(raw, []byte(`"application_profile"`)) != (profile != "") {
				t.Fatal("legacy canonical field omission changed", e)
			}
			var out any
			if _, ok := request.(PostgreSQLBackupRequest); ok {
				out = &PostgreSQLBackupRequest{}
			} else {
				out = &PostgreSQLRestoreRequest{}
			}
			if StrictJSON(raw, out) != nil {
				t.Fatal("current selector failed strict roundtrip")
			}
			again, e := json.Marshal(out)
			if e != nil || !bytes.Equal(raw, again) {
				t.Fatal("legacy/explicit request bytes changed in roundtrip", e)
			}
		}
	}
}

func TestPostgresNativeApplicationSelectionRefusesAmbiguousOrUnknownInputs(t *testing.T) {
	backup, restore := postgresApplicationRequestFixture()
	for _, value := range []string{"platform", "localization", "releases-cache-v1", "mvp-postgresql-cpu-v2", "MVP-POSTGRESQL-CPU-V1", "releases "} {
		backup.ApplicationProfile, restore.ApplicationProfile = value, value
		if backup.Valid() || restore.Valid() || backup.EffectiveApplicationProfile() != "" || restore.EffectiveApplicationProfile() != "" {
			t.Fatal("unknown selector supplied native authority", value)
		}
	}
	for _, raw := range []string{
		`{"application_profile":""}`, `{"application_profile":null}`, `{"application_profile":false}`, `{"application_profile":1}`,
		`{"application_profile":"platform"}`, `{"Application_Profile":"releases"}`, `{"application_profiLe":"releases"}`,
		`{"application_profile":"releases","APPLICATION_PROFILE":"mvp-postgresql-cpu-v1"}`,
		`{"application_profile":"releases","application_profile":"releases"}`,
	} {
		beforeBackup, beforeRestore := postgresApplicationRequestFixture()
		backup, restore := beforeBackup, beforeRestore
		if StrictJSON([]byte(raw), &backup) == nil || StrictJSON([]byte(raw), &restore) == nil {
			t.Fatal("ambiguous explicit profile decoded", raw)
		}
		before, _ := json.Marshal(beforeBackup)
		after, _ := json.Marshal(backup)
		if !bytes.Equal(before, after) {
			t.Fatal("failed profile decode mutated backup request")
		}
		before, _ = json.Marshal(beforeRestore)
		after, _ = json.Marshal(restore)
		if !bytes.Equal(before, after) {
			t.Fatal("failed profile decode mutated restore request")
		}
	}
}

func TestRestoredRuntimeMVPRequiresExplicitProfileAndExactNewLineageVersion(t *testing.T) {
	legacy := restoredRuntimeFixture()
	old, _ := json.Marshal(legacy)
	if bytes.Contains(old, []byte(`"application_profile"`)) || !legacy.Valid() || legacy.EffectiveApplicationProfile() != PostgreSQLLegacyApplicationProfile {
		t.Fatal("historical generic context/omitted bytes changed")
	}
	for _, version := range []int{16, 22, 27, MVPPostgreSQLCPUV1SchemaVersion} {
		v := legacy
		v.ApplicationProfile = MVPPostgreSQLCPUApplicationProfile
		v.SchemaVersion = version
		if v.Valid() != (version == MVPPostgreSQLCPUV1SchemaVersion) {
			t.Fatal("new profile aliased another schema lineage", version)
		}
	}
	v := legacy
	v.SchemaVersion = MVPPostgreSQLCPUV1SchemaVersion
	if v.EffectiveApplicationProfile() != PostgreSQLLegacyApplicationProfile {
		t.Fatal("a version number silently selected MVP authority")
	}
	v.ApplicationProfile = MVPPostgreSQLCPUApplicationProfile
	raw, _ := json.Marshal(v)
	var out RestoredRuntimeContext
	if StrictJSON(raw, &out) != nil || out != v || !out.Valid() {
		t.Fatal("exact explicit MVP context failed roundtrip")
	}
	for _, raw := range []string{`{"application_profile":""}`, `{"application_profile":null}`, `{"APPLICATION_PROFILE":"mvp-postgresql-cpu-v1"}`, `{"application_profile":"platform"}`} {
		before := v
		if StrictJSON([]byte(raw), &v) == nil || v != before {
			t.Fatal("ambiguous context mutated previous authority", raw)
		}
	}
}
