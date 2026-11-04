package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func nativeBackupReferenceFixture() NativePostgreSQLBackupReference {
	d := strings.Repeat("a", 64)
	return NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: d, SchemaVersion: 30, TableCount: 110, RequestSHA256: d, CandidateSHA256: d, CompletionSHA256: d, ReadbackSHA256: d, MVPSourceSHA256: d}
}

func nativeRestoreReferenceFixture() NativePostgreSQLRestoreReference {
	d := strings.Repeat("a", 64)
	return NativePostgreSQLRestoreReference{FormatVersion: 1, ApplicationProfile: MVPPostgreSQLCPUApplicationProfile, Backup: nativeBackupReferenceFixture(), RequestSHA256: d, ManifestSHA256: d, IntentSHA256: d, FilesSHA256: d, CompletionSHA256: d, FilesReadbackSHA256: d, ObservationSHA256: d, TargetBindingSHA256: d, PostDataSHA256: d}
}

func TestNativePostgreSQLBackupReferenceRequiresExactSourceAndOriginalRecords(t *testing.T) {
	if !nativeBackupReferenceFixture().Valid() {
		t.Fatal("complete synthetic reference shape refused")
	}
	for name, change := range map[string]func(*NativePostgreSQLBackupReference){
		"legacy application":                    func(v *NativePostgreSQLBackupReference) { v.ApplicationProfile = PostgreSQLLegacyApplicationProfile },
		"unbound source manifest":               func(v *NativePostgreSQLBackupReference) { v.SourceManifestSHA256 = "" },
		"old platform schema":                   func(v *NativePostgreSQLBackupReference) { v.SchemaVersion = 27 },
		"wrong table count":                     func(v *NativePostgreSQLBackupReference) { v.TableCount = 107 },
		"missing completion":                    func(v *NativePostgreSQLBackupReference) { v.CompletionSHA256 = "" },
		"missing independent readback":          func(v *NativePostgreSQLBackupReference) { v.ReadbackSHA256 = "" },
		"private path instead of source digest": func(v *NativePostgreSQLBackupReference) { v.MVPSourceSHA256 = "/private/source" },
		"unretained configured registry":        func(v *NativePostgreSQLBackupReference) { v.RegistryConfigured = true },
	} {
		t.Run(name, func(t *testing.T) {
			v := nativeBackupReferenceFixture()
			change(&v)
			if v.Valid() {
				t.Fatal("incomplete/mismatched reference admitted")
			}
		})
	}
}

func TestNativePostgreSQLRestoreReferenceCannotUseBackupAsImportEvidence(t *testing.T) {
	if !nativeRestoreReferenceFixture().Valid() {
		t.Fatal("complete synthetic restore reference shape refused")
	}
	for name, change := range map[string]func(*NativePostgreSQLRestoreReference){
		"backup manifest changed":      func(v *NativePostgreSQLRestoreReference) { v.ManifestSHA256 = strings.Repeat("b", 64) },
		"missing native completion":    func(v *NativePostgreSQLRestoreReference) { v.CompletionSHA256 = "" },
		"missing files readback":       func(v *NativePostgreSQLRestoreReference) { v.FilesReadbackSHA256 = "" },
		"missing readonly observation": func(v *NativePostgreSQLRestoreReference) { v.ObservationSHA256 = "" },
		"missing original postimage":   func(v *NativePostgreSQLRestoreReference) { v.PostDataSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			v := nativeRestoreReferenceFixture()
			change(&v)
			if v.Valid() {
				t.Fatal("backup-only/incomplete restore reference admitted")
			}
		})
	}
}

func TestNativePostgreSQLRegistryImportReferenceRetainsOriginalSnapshotBinding(t *testing.T) {
	v := nativeRestoreReferenceFixture()
	d := strings.Repeat("a", 64)
	v.Backup.RegistryConfigured = true
	v.Backup.Registry = &NativePostgreSQLRegistryReference{ReceiptSHA256: d, ManifestSHA256: d, PayloadTreeSHA256: d}
	if v.Valid() {
		t.Fatal("configured registry without native import admitted")
	}
	v.Registry = &NativePostgreSQLRegistryImportReference{SnapshotReceiptSHA256: d, ImportReceiptSHA256: d, ReadbackSHA256: d}
	if !v.Valid() {
		t.Fatal("complete registry reference shape refused")
	}
	v.Registry.SnapshotReceiptSHA256 = strings.Repeat("b", 64)
	if v.Valid() {
		t.Fatal("foreign registry snapshot admitted")
	}
	v.Registry.SnapshotReceiptSHA256 = d
	v.Registry.ReadbackSHA256 = ""
	if v.Valid() {
		t.Fatal("missing registry independent readback admitted")
	}
}

func TestNativePostgreSQLReferenceRoundtripAndSafeProjection(t *testing.T) {
	v := nativeRestoreReferenceFixture()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var got NativePostgreSQLRestoreReference
	if StrictJSON(raw, &got) != nil || !got.Valid() {
		t.Fatal("canonical reference roundtrip failed")
	}
	for _, forbidden := range []string{"source_root", "target_root", "password", "credential", "operator", "database", "runtime_context", "registry"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatalf("private or absent value exposed: %s", forbidden)
		}
	}
	if StrictJSON(append(raw[:len(raw)-1], []byte(`,"source_root":"/private"}`)...), &got) == nil {
		t.Fatal("unknown private field admitted")
	}
}
