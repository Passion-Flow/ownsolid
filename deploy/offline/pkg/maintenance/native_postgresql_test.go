package maintenance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Synthetic canonical reference shapes exercise transport/binding only. None
// is an original native producer receipt, SQL effect or signature verification.
func nativeBackupFixture() wire.NativePostgreSQLBackupReference {
	d := strings.Repeat("a", 64)
	return wire.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: d, SchemaVersion: 29, TableCount: 108, RequestSHA256: strings.Repeat("b", 64), CandidateSHA256: strings.Repeat("c", 64), CompletionSHA256: strings.Repeat("d", 64), ReadbackSHA256: strings.Repeat("e", 64), MVPSourceSHA256: strings.Repeat("f", 64), RegistryConfigured: true, Registry: &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: d, ManifestSHA256: d, PayloadTreeSHA256: d, Pins: 2, Files: 12, Bytes: 35106}}
}

func nativeRestoreFixture() wire.NativePostgreSQLRestoreReference {
	b := nativeBackupFixture()
	d := strings.Repeat("a", 64)
	return wire.NativePostgreSQLRestoreReference{FormatVersion: 1, ApplicationProfile: b.ApplicationProfile, Backup: b, RequestSHA256: d, ManifestSHA256: b.CandidateSHA256, IntentSHA256: d, FilesSHA256: d, CompletionSHA256: d, FilesReadbackSHA256: d, ObservationSHA256: d, TargetBindingSHA256: d, PostDataSHA256: d, Registry: &wire.NativePostgreSQLRegistryImportReference{SnapshotReceiptSHA256: b.Registry.ReceiptSHA256, ImportReceiptSHA256: d, ReadbackSHA256: d}}
}

func TestOriginalNativeBackupReferenceRetainsSourceSignatureBindingAndStoredType(t *testing.T) {
	for _, command := range []Command{Backup, InspectBackup, ReconcileBackup, InspectRestoreSource} {
		t.Run(string(command), func(t *testing.T) {
			reference := nativeBackupFixture()
			var original any = scope.BackupResult{Status: "complete_selected_profile_backup_restore_pending", Profile: wire.MVPPostgreSQLCPUProfile, ReleaseManifestSHA256: reference.SourceManifestSHA256, NativePostgreSQL: &reference, Qualification: "synthetic_reference_projection_only"}
			if command == InspectRestoreSource {
				original = scope.RestoreSourceResult{Status: "verified_independent_backup_source_not_restored", Profile: wire.MVPPostgreSQLCPUProfile, ReleaseManifestSHA256: reference.SourceManifestSHA256, NativePostgreSQL: &reference, Qualification: "synthetic_reference_projection_only"}
			}
			r, err := project(command, original)
			if err != nil || r.NativePostgreSQL == nil || r.NativePostgreSQL.Restore != nil || !reflect.DeepEqual(r.NativePostgreSQL.Backup, &reference) || r.ReleaseManifestSHA256 != reference.SourceManifestSHA256 || r.EvidenceSHA256 != digest(r.Original) {
				t.Fatal("original typed backup/source reference lost", err)
			}
			public, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var stored Result
			if wire.StrictJSON(public, &stored) != nil || !reflect.DeepEqual(stored.NativePostgreSQL, r.NativePostgreSQL) {
				t.Fatal("stored typed native reference changed")
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(public, &fields) != nil {
				t.Fatal("public projection invalid")
			}
			canonical, _ := json.Marshal(reference)
			if string(fields["native_postgresql"]) != string(canonical) || strings.Contains(string(public), "native_postgresql_restore") || strings.Contains(string(public), "source_root") || strings.Contains(string(public), "Original") {
				t.Fatal("canonical native JSON changed or private original exposed")
			}
			reference.Registry.Pins++
			if r.NativePostgreSQL.Backup.Registry.Pins != 2 {
				t.Fatal("projection aliases mutable original registry proof")
			}
		})
	}
}

func TestOriginalNativeRestoreReferencesKeepExactCanonicalVariant(t *testing.T) {
	for _, command := range []Command{RestoreSQL, ReconcileSQL, VerifyRestoredDatabase, ReconcileDatabase, RestoreStores, ReconcileStores} {
		t.Run(string(command), func(t *testing.T) {
			reference := nativeRestoreFixture()
			var original any
			switch command {
			case RestoreSQL, ReconcileSQL:
				original = scope.RestoreSQLResult{Status: "synthetic_reference_projection_only", Qualification: "no_native_effect_or_completion_claim", NativePostgreSQL: &reference}
			case VerifyRestoredDatabase, ReconcileDatabase:
				original = scope.RestoreDatabaseResult{Status: "synthetic_reference_projection_only", Qualification: "no_native_effect_or_completion_claim", NativePostgreSQL: &reference}
			default:
				original = scope.RestoreStoresResult{Status: "synthetic_reference_projection_only", Qualification: "no_native_effect_or_completion_claim", NativePostgreSQL: &reference}
			}
			r, err := project(command, original)
			if err != nil || r.NativePostgreSQL == nil || r.NativePostgreSQL.Backup != nil || !reflect.DeepEqual(r.NativePostgreSQL.Restore, &reference) {
				t.Fatal("original typed restore reference lost", err)
			}
			public, _ := json.Marshal(r)
			var stored Result
			if wire.StrictJSON(public, &stored) != nil || !reflect.DeepEqual(stored.NativePostgreSQL, r.NativePostgreSQL) {
				t.Fatal("stored restore proof changed")
			}
			if _, err := project(Backup, original); err == nil {
				t.Fatal("restore reference accepted as backup proof")
			}
		})
	}
}

func TestNativeProjectionRejectsWrongSignedBindingWithZeroOutput(t *testing.T) {
	reference := nativeBackupFixture()
	for name, original := range map[string]scope.BackupResult{
		"unsigned":         {Status: "synthetic", Qualification: "synthetic", Profile: wire.MVPPostgreSQLCPUProfile, NativePostgreSQL: &reference},
		"different source": {Status: "synthetic", Qualification: "synthetic", Profile: wire.MVPPostgreSQLCPUProfile, ReleaseManifestSHA256: strings.Repeat("1", 64), NativePostgreSQL: &reference},
		"legacy profile":   {Status: "synthetic", Qualification: "synthetic", Profile: wire.CumulativeCPUProfile, ReleaseManifestSHA256: reference.SourceManifestSHA256, NativePostgreSQL: &reference},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := project(Backup, original)
			if err == nil || r.NativePostgreSQL != nil || r.Status != "" || len(r.Original) != 0 {
				t.Fatal("unbound original source became a successful projection")
			}
		})
	}
	original := scope.BackupResult{Status: "synthetic", Qualification: "synthetic", Profile: wire.MVPPostgreSQLCPUProfile, ReleaseManifestSHA256: reference.SourceManifestSHA256, NativePostgreSQL: &reference}
	if r, err := project(RestoreSQL, original); err == nil || r.NativePostgreSQL != nil {
		t.Fatal("backup-only proof accepted for imports")
	}
}

func TestTransparentNativeUnionRefusesUnknownNullMixedAndStaleProofs(t *testing.T) {
	reference := nativeBackupFixture()
	raw, _ := json.Marshal(reference)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["source_root"] = "/synthetic/private"
	unknown, _ := json.Marshal(fields)
	delete(fields, "source_root")
	fields["backup"] = reference
	mixed, _ := json.Marshal(fields)
	for _, input := range [][]byte{[]byte("null"), []byte("{}"), []byte("[]"), unknown, mixed, []byte(strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"format_version":1`, 1))} {
		union := NativePostgreSQLReference{Backup: &reference}
		if union.UnmarshalJSON(input) == nil || union.Backup != nil || union.Restore != nil {
			t.Fatal("malformed union retained a stale proof")
		}
	}
	if _, err := json.Marshal(NativePostgreSQLReference{Backup: &reference, Restore: func() *wire.NativePostgreSQLRestoreReference { r := nativeRestoreFixture(); return &r }()}); err == nil {
		t.Fatal("ambiguous native union serialized")
	}
}

func TestOuterNativeReferenceRejectsExplicitNullAndPreservesOmission(t *testing.T) {
	for _, profile := range []string{wire.CumulativeCPUProfile, wire.MVPPostgreSQLCPUProfile} {
		t.Run(profile, func(t *testing.T) {
			// Shape/progress compatibility only: omission is not completed MVP proof.
			original := map[string]any{"status": "synthetic_inventory_only", "qualification": "synthetic_outer_shape_not_completion", "profile": profile}
			omitted, err := project(InspectRestoreSource, original)
			if err != nil || omitted.Profile != profile || omitted.NativePostgreSQL != nil {
				t.Fatal("original omitted reference projection changed", err)
			}
			raw, err := json.Marshal(omitted)
			var stored Result
			if err != nil || json.Unmarshal(raw, &stored) != nil || stored.Profile != profile || stored.NativePostgreSQL != nil || strings.Contains(string(raw), `"native_postgresql"`) {
				t.Fatal("original omitted reference stored shape changed", err)
			}
			var outer map[string]json.RawMessage
			if json.Unmarshal(raw, &outer) != nil {
				t.Fatal("synthetic outer fixture invalid")
			}
			outer["native_postgresql"] = json.RawMessage("null")
			nullRaw, _ := json.Marshal(outer)
			reference := nativeBackupFixture()
			stored = Result{Status: "stale", NativePostgreSQL: &NativePostgreSQLReference{Backup: &reference}}
			if err := json.Unmarshal(nullRaw, &stored); err == nil || stored.Status != "" || stored.NativePostgreSQL != nil {
				t.Error("M26_EXPLICIT_NATIVE_NULL_MUST_NOT_BECOME_OMITTED: stored Result accepted null or retained stale output", err)
			}
			original["native_postgresql"] = nil
			projected, err := project(InspectRestoreSource, original)
			if err == nil || projected.Status != "" || projected.NativePostgreSQL != nil || len(projected.Original) != 0 {
				t.Error("M26_EXPLICIT_NATIVE_NULL_MUST_NOT_BECOME_OMITTED: original projection accepted null or retained output", err)
			}
		})
	}
}
