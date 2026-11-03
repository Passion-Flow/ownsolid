package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeObservationHash(c string) string { return strings.Repeat(c, 64) }

func nativeBackupObservationFixture() NativePostgreSQLBackupObservation {
	r := &wire.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: nativeObservationHash("a"), SchemaVersion: 29, TableCount: 108, RequestSHA256: nativeObservationHash("b"), CandidateSHA256: nativeObservationHash("c"), CompletionSHA256: nativeObservationHash("d"), ReadbackSHA256: nativeObservationHash("e"), MVPSourceSHA256: nativeObservationHash("f")}
	return NativePostgreSQLBackupObservation{Format: "projectone.postgres.independent-readback.v1", BackupID: strings.Repeat("1", 32), RequestSHA256: r.RequestSHA256, ManifestSHA256: r.CandidateSHA256, ArchiveSHA256: nativeObservationHash("2"), ArchiveDirectorySHA256: nativeObservationHash("3"), ObservedAt: "2026-11-03T00:00:00Z", NativePostgreSQL: r}
}

func nativeRestoreObservationFixture() NativePostgreSQLRestoreObservation {
	b := nativeBackupObservationFixture()
	r := &wire.NativePostgreSQLRestoreReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, Backup: *b.NativePostgreSQL, RequestSHA256: nativeObservationHash("4"), ManifestSHA256: b.ManifestSHA256, IntentSHA256: nativeObservationHash("5"), FilesSHA256: nativeObservationHash("6"), CompletionSHA256: nativeObservationHash("7"), FilesReadbackSHA256: nativeObservationHash("8"), ObservationSHA256: nativeObservationHash("9"), TargetBindingSHA256: nativeObservationHash("a"), PostDataSHA256: nativeObservationHash("b")}
	return NativePostgreSQLRestoreObservation{Format: "projectone.postgres.restore-observation.v1", RestoreID: strings.Repeat("2", 32), IntentSHA256: r.IntentSHA256, FilesSHA256: r.FilesSHA256, ManifestSHA256: r.ManifestSHA256, TargetBindingSHA256: r.TargetBindingSHA256, PostDataSHA256: r.PostDataSHA256, OriginalCompletionPresent: true, ReadOnly: true, ObservedAt: "2026-11-03T00:00:00Z", Qualification: "Synthetic transport fixture; no native operation executed", NativePostgreSQL: r}
}

func TestNativeBackupObservationBindsDistinctOriginalAndSignedDigests(t *testing.T) {
	v := nativeBackupObservationFixture()
	raw, _ := json.Marshal(v)
	got, e := DecodeNativePostgreSQLBackupObservation(raw, int64(len(raw)), v.BackupID, v.RequestSHA256, v.ManifestSHA256, v.NativePostgreSQL.SourceManifestSHA256)
	if e != nil || got.OriginalSHA256 != wire.Digest(raw) || !bytes.Equal(got.Original, raw) {
		t.Fatal("valid synthetic transport refused", e)
	}
	raw[0] = '!'
	if got.Original[0] != '{' {
		t.Fatal("private original aliases mutable caller bytes")
	}
	for name, change := range map[string]func(*NativePostgreSQLBackupObservation){
		"missing native proof":                   func(x *NativePostgreSQLBackupObservation) { x.NativePostgreSQL = nil },
		"candidate confused with signed release": func(x *NativePostgreSQLBackupObservation) { x.ManifestSHA256 = x.NativePostgreSQL.SourceManifestSHA256 },
		"request swapped":                        func(x *NativePostgreSQLBackupObservation) { x.RequestSHA256 = nativeObservationHash("0") },
		"unproven live lock":                     func(x *NativePostgreSQLBackupObservation) { x.CurrentLockHeld = true },
		"invalid observation":                    func(x *NativePostgreSQLBackupObservation) { x.ObservedAt = "today" },
	} {
		t.Run(name, func(t *testing.T) {
			x := nativeBackupObservationFixture()
			change(&x)
			b, _ := json.Marshal(x)
			if _, e := DecodeNativePostgreSQLBackupObservation(b, 1<<20, v.BackupID, v.RequestSHA256, v.ManifestSHA256, v.NativePostgreSQL.SourceManifestSHA256); e == nil {
				t.Fatal("contradictory original observation accepted")
			}
		})
	}
	if _, e = DecodeNativePostgreSQLBackupObservation(got.Original, 1<<20, v.BackupID, v.RequestSHA256, v.ManifestSHA256, v.ManifestSHA256); e == nil {
		t.Fatal("caller signed source was not independently bound")
	}
}

func TestNativeRestoreObservationRequiresOriginalCompletionAndStaticBinding(t *testing.T) {
	v := nativeRestoreObservationFixture()
	decode := func(x NativePostgreSQLRestoreObservation) (NativePostgreSQLRestoreObservation, error) {
		b, _ := json.Marshal(x)
		return DecodeNativePostgreSQLRestoreObservation(b, 1<<20, v.RestoreID, v.NativePostgreSQL.RequestSHA256, v.ManifestSHA256, v.NativePostgreSQL.Backup.SourceManifestSHA256)
	}
	if _, e := decode(v); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*NativePostgreSQLRestoreObservation){
		"missing completion with proof": func(x *NativePostgreSQLRestoreObservation) { x.OriginalCompletionPresent = false },
		"completion without proof":      func(x *NativePostgreSQLRestoreObservation) { x.NativePostgreSQL = nil },
		"changed target":                func(x *NativePostgreSQLRestoreObservation) { x.TargetBindingSHA256 = nativeObservationHash("0") },
		"changed postimage":             func(x *NativePostgreSQLRestoreObservation) { x.PostDataSHA256 = nativeObservationHash("0") },
		"changed original files":        func(x *NativePostgreSQLRestoreObservation) { x.FilesSHA256 = nativeObservationHash("0") },
		"nonreadonly response":          func(x *NativePostgreSQLRestoreObservation) { x.ReadOnly = false },
		"old signed source": func(x *NativePostgreSQLRestoreObservation) {
			x.NativePostgreSQL.Backup.SourceManifestSHA256 = nativeObservationHash("0")
		},
	} {
		t.Run(name, func(t *testing.T) {
			x := nativeRestoreObservationFixture()
			change(&x)
			if _, e := decode(x); e == nil {
				t.Fatal("unbound restore evidence accepted")
			}
		})
	}
	v.OriginalCompletionPresent, v.NativePostgreSQL = false, nil
	raw, _ := json.Marshal(v)
	partial, e := DecodeNativePostgreSQLRestoreObservation(raw, 1<<20, v.RestoreID, nativeObservationHash("4"), v.ManifestSHA256, nativeObservationHash("a"))
	if e != nil || partial.NativePostgreSQL != nil || partial.OriginalCompletionPresent {
		t.Fatal("missing completion lost its explicit noncomplete observation", e)
	}
}

func TestNativeObservationBoundsRedactionAndFreshReadonlyTime(t *testing.T) {
	v := nativeRestoreObservationFixture()
	raw, _ := json.Marshal(v)
	decode := func(b []byte, limit int64) (NativePostgreSQLRestoreObservation, error) {
		return DecodeNativePostgreSQLRestoreObservation(b, limit, v.RestoreID, v.NativePostgreSQL.RequestSHA256, v.ManifestSHA256, v.NativePostgreSQL.Backup.SourceManifestSHA256)
	}
	for name, raw := range map[string][]byte{
		"trailing output":       append(append([]byte(nil), raw...), []byte(" another result")...),
		"private unknown field": append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"dsn":"synthetic-private-marker"}`)...),
		"duplicate field":       append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"restore_id":"other"}`)...),
		"nil":                   []byte("null"),
	} {
		t.Run(name, func(t *testing.T) {
			got, e := decode(raw, 1<<20)
			if e == nil || len(got.Original) != 0 || strings.Contains(e.Error(), "synthetic-private-marker") {
				t.Fatal("invalid output leaked or was admitted")
			}
		})
	}
	if _, e := decode(raw, int64(len(raw)-1)); e == nil {
		t.Fatal("metadata bound ignored")
	}
	v.ObservedAt, v.NativePostgreSQL.ObservationSHA256 = "2026-11-03T00:00:01Z", nativeObservationHash("0")
	fresh, _ := json.Marshal(v)
	got, e := decode(fresh, 1<<20)
	if e != nil {
		t.Fatal("fresh readonly observation incorrectly treated as immutable original", e)
	}
	public, _ := json.Marshal(got)
	if bytes.Contains(public, []byte(`"original"`)) || bytes.Contains(public, []byte(`"OriginalSHA256"`)) {
		t.Fatal("private original JSON was projected")
	}
}
