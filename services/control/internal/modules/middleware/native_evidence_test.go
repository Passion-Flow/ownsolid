package middleware

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/pkg/maintenance"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Canonical shapes only: these tests exercise consumers, never an original
// native producer, signature verification, SQL, registry import or target.
func nativeSourceStages() []maintenance.Result {
	d := strings.Repeat("a", 64)
	ref := wire.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: d, SchemaVersion: 29, TableCount: 108, RequestSHA256: d, CandidateSHA256: strings.Repeat("c", 64), CompletionSHA256: d, ReadbackSHA256: d, MVPSourceSHA256: d, RegistryConfigured: true, Registry: &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: d, ManifestSHA256: d, PayloadTreeSHA256: d, Pins: 2, Files: 12, Bytes: 35106}}
	backup := maintenance.Result{Command: maintenance.Backup, Status: "complete_selected_profile_backup_restore_pending", ScopeID: strings.Repeat("b", 32), ManifestSHA256: strings.Repeat("9", 64), EvidenceSHA256: d, Profile: wire.MVPPostgreSQLCPUProfile, ReleaseManifestSHA256: d, NativePostgreSQL: &maintenance.NativePostgreSQLReference{Backup: &ref}}
	source := backup
	source.Command, source.Status, source.SourceScopeID = maintenance.InspectRestoreSource, "verified_independent_backup_source_not_restored", backup.ScopeID
	source.ScopeID, source.PlanSHA256 = "", d
	source.Source = &maintenance.DatabaseSource{Database: "synthetic", Operator: "synthetic", ServerVersionNumber: 180006, TenantID: "11111111-1111-1111-1111-111111111111", SchemaVersion: 29, VectorVersion: "0.8.7"}
	source.Stores = []string{"datasets", "models", "preparations", "training"}
	return []maintenance.Result{backup, source}
}

func TestNativeSourceConsumerRequiresOriginalTypedProofAndSignedOuter(t *testing.T) {
	stages := nativeSourceStages()
	if !completedBackupResult(stages[0]) || !completedSourceResult(stages[1], stages[0].ManifestSHA256) || !sameOriginalNativeBackup(stages[0], stages[1]) {
		t.Fatal("complete synthetic native reference chain refused")
	}
	// The outer inventory manifest is deliberately different from the original
	// native candidate and signed-source manifest; they are distinct authorities.
	op := Operation{ID: "22222222-2222-2222-2222-222222222222", Kind: "backup", State: "succeeded", Result: mustJSON(executionEvidence{FormatVersion: 1, Kind: "backup", Stages: stages})}
	evidence, root, err := backupReceipt(op)
	if err != nil || root != op.ID || evidence.Stages[1].NativePostgreSQL == nil || evidence.Stages[1].NativePostgreSQL.Backup.CandidateSHA256 != stages[1].NativePostgreSQL.Backup.CandidateSHA256 {
		t.Fatal("stored native reference was dropped or confused with wrapper manifest", err)
	}
	for name, mutate := range map[string]func(*maintenance.Result){
		"missing reference":                func(r *maintenance.Result) { r.NativePostgreSQL = nil },
		"missing signed binding":           func(r *maintenance.Result) { r.ReleaseManifestSHA256 = "" },
		"different signed binding":         func(r *maintenance.Result) { r.ReleaseManifestSHA256 = strings.Repeat("1", 64) },
		"legacy profile with native proof": func(r *maintenance.Result) { r.Profile = wire.CumulativeCPUProfile },
		"old source schema":                func(r *maintenance.Result) { r.Source.SchemaVersion = 27 },
		"missing original stores":          func(r *maintenance.Result) { r.Stores = r.Stores[:3] },
		"registry is not fifth store":      func(r *maintenance.Result) { r.Stores = append(r.Stores, "imageregistry") },
		"configured payload not retained":  func(r *maintenance.Result) { r.NativePostgreSQL.Backup.Registry = nil },
		"disabled registry is not final profile": func(r *maintenance.Result) {
			r.NativePostgreSQL.Backup.RegistryConfigured = false
			r.NativePostgreSQL.Backup.Registry = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var source maintenance.Result
			raw, _ := json.Marshal(stages[1])
			if wire.StrictJSON(raw, &source) != nil {
				t.Fatal("test source clone failed")
			}
			mutate(&source)
			if completedSourceResult(source, stages[0].ManifestSHA256) {
				t.Fatal("incomplete/mismatched final native source accepted")
			}
		})
	}
	stale := stages[1]
	stale.Profile, stale.NativePostgreSQL, stale.ReleaseManifestSHA256 = "", nil, ""
	if completedSourceResult(stale, stages[0].ManifestSHA256) {
		t.Fatal("old omitted-profile schema29 metadata treated as legacy-qualified native proof")
	}
}

func TestNativeBackupAndIndependentSourceKeepSameOriginalReference(t *testing.T) {
	for name, mutate := range map[string]func(*wire.NativePostgreSQLBackupReference){
		"candidate":        func(r *wire.NativePostgreSQLBackupReference) { r.CandidateSHA256 = strings.Repeat("1", 64) },
		"completion":       func(r *wire.NativePostgreSQLBackupReference) { r.CompletionSHA256 = strings.Repeat("1", 64) },
		"readback":         func(r *wire.NativePostgreSQLBackupReference) { r.ReadbackSHA256 = strings.Repeat("1", 64) },
		"source":           func(r *wire.NativePostgreSQLBackupReference) { r.MVPSourceSHA256 = strings.Repeat("1", 64) },
		"registry receipt": func(r *wire.NativePostgreSQLBackupReference) { r.Registry.ReceiptSHA256 = strings.Repeat("1", 64) },
		"registry tree":    func(r *wire.NativePostgreSQLBackupReference) { r.Registry.PayloadTreeSHA256 = strings.Repeat("1", 64) },
		"registry pins":    func(r *wire.NativePostgreSQLBackupReference) { r.Registry.Pins++ },
	} {
		t.Run(name, func(t *testing.T) {
			stages := nativeSourceStages()
			var independent maintenance.Result
			raw, _ := json.Marshal(stages[1])
			if wire.StrictJSON(raw, &independent) != nil {
				t.Fatal("test independent clone failed")
			}
			mutate(independent.NativePostgreSQL.Backup)
			if !completedSourceResult(independent, stages[0].ManifestSHA256) || sameOriginalNativeBackup(stages[0], independent) {
				t.Fatal("independently different original native reference accepted")
			}
		})
	}
}
