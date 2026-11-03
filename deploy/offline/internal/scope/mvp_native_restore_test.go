package scope

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These are explicitly synthetic original-output bindings, not SQL, signature,
// registry import, Engine or full native restore qualification.
func mvpNativeRestoreFixture() (RestoreSourceResult, engine.NativePostgreSQLRestoreObservation) {
	h := func(c string) string { return strings.Repeat(c, 64) }
	b := wire.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: h("a"), SchemaVersion: 29, TableCount: 108, RequestSHA256: h("b"), CandidateSHA256: h("c"), CompletionSHA256: h("d"), ReadbackSHA256: h("e"), MVPSourceSHA256: h("f"), RegistryConfigured: true, Registry: &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: h("1"), ManifestSHA256: h("2"), PayloadTreeSHA256: h("3")}}
	source := RestoreSourceResult{Status: "verified_independent_backup_source_not_restored", Profile: wire.MVPPostgreSQLCPUProfile, ReleaseManifestSHA256: b.SourceManifestSHA256, NativePostgreSQL: &b, ManifestSHA256: h("0"), Stores: []string{"datasets", "models", "preparations", "training"}, Source: engine.PostgresBackupSource{Database: "synthetic_source", Operator: "synthetic_operator", TenantID: "11111111-1111-4111-8111-111111111111", ServerVersionNumber: 180006, SchemaVersion: 29, VectorVersion: "0.8.7"}}
	source.SourceScopeID, source.SourceAttemptID = strings.Repeat("1", 32), strings.Repeat("6", 32)
	source.RequestSHA256, source.PlanSHA256 = h("7"), h("8")
	r := &wire.NativePostgreSQLRestoreReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, Backup: b, RequestSHA256: h("4"), ManifestSHA256: b.CandidateSHA256, IntentSHA256: h("5"), FilesSHA256: h("6"), CompletionSHA256: h("7"), FilesReadbackSHA256: h("8"), ObservationSHA256: h("9"), TargetBindingSHA256: h("a"), PostDataSHA256: h("b"), Registry: &wire.NativePostgreSQLRegistryImportReference{SnapshotReceiptSHA256: b.Registry.ReceiptSHA256, ImportReceiptSHA256: h("c"), ReadbackSHA256: h("d")}}
	return source, engine.NativePostgreSQLRestoreObservation{Format: "projectone.postgres.restore-observation.v1", RestoreID: strings.Repeat("2", 32), ManifestSHA256: r.ManifestSHA256, IntentSHA256: r.IntentSHA256, FilesSHA256: r.FilesSHA256, TargetBindingSHA256: r.TargetBindingSHA256, PostDataSHA256: r.PostDataSHA256, OriginalCompletionPresent: true, ReadOnly: true, ObservedAt: "2026-11-03T00:00:00Z", Qualification: "Synthetic original output; no native restore executed", NativePostgreSQL: r}
}

func TestMVPNativeRestoreOriginalOutputBindsSignedSourceAndPreservesBytes(t *testing.T) {
	source, value := mvpNativeRestoreFixture()
	raw := append([]byte(" \n"), encode(value)...)
	got, e := bindMVPNativeRestoreObservation(raw, int64(len(raw)), value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil)
	if e != nil || !bytes.Equal(got.Original, raw) || got.OriginalSHA256 != wire.Digest(raw) || !reflect.DeepEqual(got.NativePostgreSQL, value.NativePostgreSQL) {
		t.Fatal("original output was not retained or bound", e)
	}
	raw[0] = '!'
	if got.Original[0] != ' ' {
		t.Fatal("original output aliases caller memory")
	}
	public, e := json.Marshal(got)
	if e != nil || bytes.Contains(public, []byte("Original")) || bytes.Contains(public, []byte(got.OriginalSHA256)) {
		t.Fatal("private original bytes/digest entered serialized observation")
	}
	if source.ManifestSHA256 == got.ManifestSHA256 || source.ReleaseManifestSHA256 == got.ManifestSHA256 || got.OriginalSHA256 == got.NativePostgreSQL.ObservationSHA256 {
		t.Fatal("test lost distinct outer, signed, candidate and raw-output hashes")
	}
}

func TestMVPNativeRestoreFreshObservationPreservesImmutableIdentity(t *testing.T) {
	source, first := mvpNativeRestoreFixture()
	_, current := mvpNativeRestoreFixture()
	current.ObservedAt = "2026-11-03T00:01:00Z"
	current.NativePostgreSQL.ObservationSHA256 = strings.Repeat("e", 64)
	got, e := bindMVPNativeRestoreObservation(encode(current), 1<<20, current.RestoreID, current.NativePostgreSQL.RequestSHA256, source, first.NativePostgreSQL)
	if e != nil || !sameMVPNativeRestore(first.NativePostgreSQL, got.NativePostgreSQL) || got.NativePostgreSQL.ObservationSHA256 == first.NativePostgreSQL.ObservationSHA256 {
		t.Fatal("new readonly observation changed original identity", e)
	}
	typ := reflect.TypeOf(*first.NativePostgreSQL)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if name == "ObservationSHA256" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			_, changed := mvpNativeRestoreFixture()
			field := reflect.ValueOf(changed.NativePostgreSQL).Elem().Field(i)
			field.Set(reflect.Zero(field.Type()))
			if sameMVPNativeRestore(first.NativePostgreSQL, changed.NativePostgreSQL) {
				t.Fatal("missing original fact compared equal")
			}
		})
	}
	for _, change := range []func(*wire.NativePostgreSQLRestoreReference){
		func(r *wire.NativePostgreSQLRestoreReference) { r.CompletionSHA256 = strings.Repeat("0", 64) },
		func(r *wire.NativePostgreSQLRestoreReference) { r.FilesReadbackSHA256 = strings.Repeat("0", 64) },
		func(r *wire.NativePostgreSQLRestoreReference) {
			r.Registry.ImportReceiptSHA256 = strings.Repeat("0", 64)
		},
		func(r *wire.NativePostgreSQLRestoreReference) { r.Registry.ReadbackSHA256 = strings.Repeat("0", 64) },
	} {
		_, changed := mvpNativeRestoreFixture()
		change(changed.NativePostgreSQL)
		if _, e := bindMVPNativeRestoreObservation(encode(changed), 1<<20, changed.RestoreID, changed.NativePostgreSQL.RequestSHA256, source, first.NativePostgreSQL); e == nil {
			t.Fatal("changed original completion/import/readback accepted")
		}
	}
}

func TestMVPNativeRestoreJournalIdentityOmitsOnlyCurrentObservationAndOwnsCopies(t *testing.T) {
	_, value := mvpNativeRestoreFixture()
	r := value.NativePostgreSQL
	identity, ok := mvpNativeRestoreIdentityOf(r)
	if !ok {
		t.Fatal("complete original identity refused")
	}
	var original, invariant map[string]json.RawMessage
	if json.Unmarshal(encode(r), &original) != nil || json.Unmarshal(encode(identity), &invariant) != nil {
		t.Fatal("identity serialization unavailable")
	}
	delete(original, "observation_sha256")
	if !reflect.DeepEqual(original, invariant) {
		t.Fatal("journal identity omitted or added an original invariant")
	}
	before := encode(identity)
	r.Registry.ImportReceiptSHA256 = strings.Repeat("0", 64)
	r.Backup.Registry.PayloadTreeSHA256 = strings.Repeat("0", 64)
	r.ObservationSHA256 = strings.Repeat("e", 64)
	if !bytes.Equal(before, encode(identity)) {
		t.Fatal("journal identity aliases mutable observation or registry memory")
	}
	if _, ok := mvpNativeRestoreIdentityOf(nil); ok {
		t.Fatal("missing original became immutable identity")
	}
}

func TestMVPNativeRestoreNoCompletionStaysNoncomplete(t *testing.T) {
	source, completed := mvpNativeRestoreFixture()
	_, partial := mvpNativeRestoreFixture()
	partial.NativePostgreSQL, partial.OriginalCompletionPresent = nil, false
	raw := encode(partial)
	got, e := bindMVPNativeRestoreObservation(raw, 1<<20, partial.RestoreID, completed.NativePostgreSQL.RequestSHA256, source, nil)
	if e != nil || got.NativePostgreSQL != nil || got.OriginalCompletionPresent || !bytes.Equal(got.Original, raw) {
		t.Fatal("missing original completion became complete or lost raw custody", e)
	}
	if _, e := bindMVPNativeRestoreObservation(raw, 1<<20, partial.RestoreID, completed.NativePostgreSQL.RequestSHA256, source, completed.NativePostgreSQL); e == nil {
		t.Fatal("completed original accepted a later missing completion")
	}
}

func TestMVPNativeRestoreSourceCannotDowngradeOrReplaceOriginalBackup(t *testing.T) {
	for name, change := range map[string]func(*RestoreSourceResult){
		"legacy profile":                  func(s *RestoreSourceResult) { s.Profile = wire.CumulativeCPUProfile },
		"old schema":                      func(s *RestoreSourceResult) { s.Source.SchemaVersion = 22 },
		"unsigned source":                 func(s *RestoreSourceResult) { s.ReleaseManifestSHA256 = "" },
		"candidate as signed source":      func(s *RestoreSourceResult) { s.ReleaseManifestSHA256 = s.NativePostgreSQL.CandidateSHA256 },
		"missing native":                  func(s *RestoreSourceResult) { s.NativePostgreSQL = nil },
		"missing original source scope":   func(s *RestoreSourceResult) { s.SourceScopeID = "" },
		"missing original backup attempt": func(s *RestoreSourceResult) { s.SourceAttemptID = "" },
		"missing original source request": func(s *RestoreSourceResult) { s.RequestSHA256 = "" },
		"missing original outer plan":     func(s *RestoreSourceResult) { s.PlanSHA256 = "" },
		"metadata only registry": func(s *RestoreSourceResult) {
			s.NativePostgreSQL.RegistryConfigured, s.NativePostgreSQL.Registry = false, nil
		},
		"foreign completion":      func(s *RestoreSourceResult) { s.NativePostgreSQL.CompletionSHA256 = strings.Repeat("0", 64) },
		"missing original store":  func(s *RestoreSourceResult) { s.Stores = s.Stores[:3] },
		"registry as fifth store": func(s *RestoreSourceResult) { s.Stores = append(s.Stores, "registry") },
	} {
		t.Run(name, func(t *testing.T) {
			source, value := mvpNativeRestoreFixture()
			// Detach the source pointer tree from the observation before tampering.
			raw := encode(value)
			change(&source)
			if _, e := bindMVPNativeRestoreObservation(raw, 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil); e == nil {
				t.Fatal("unbound source accepted")
			}
		})
	}
}
