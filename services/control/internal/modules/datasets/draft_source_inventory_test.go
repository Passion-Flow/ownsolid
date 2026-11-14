package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func originalInventoryDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestSnapshotOriginalSourceRoles(t *testing.T) {
	normalized := SnapshotSource{ObjectID: uuid.NewString(), SHA256: strings.Repeat("a", 64), SizeBytes: 3}
	for _, format := range []SourceFormat{SourceCSV, SourceParquet} {
		t.Run(string(format), func(t *testing.T) {
			original := SnapshotSource{ObjectID: normalized.ObjectID, SHA256: strings.Repeat("b", 64), SizeBytes: 2, OriginalFormat: format}
			references := snapshotReferences{TenantID: uuid.NewString(), Sources: []SnapshotSource{normalized, original}}
			files, err := referenceFiles(references)
			want := "objects/" + original.ObjectID + "/" + draftOriginalFile(format)
			if err != nil || len(files) != 2 || files[want].SHA256 != original.SHA256 ||
				files[objectPath(normalized.ObjectID)].SHA256 != normalized.SHA256 ||
				snapshotSchemaVersion(references) != originalSourceSnapshotSchema {
				t.Fatal("complete original/normalized roles", files, err)
			}
			cases := map[string][]SnapshotSource{
				"orphan original":      {original},
				"duplicate normalized": {normalized, normalized},
				"duplicate original":   {normalized, original, original},
			}
			second := original
			if format == SourceCSV {
				second.OriginalFormat = SourceParquet
			} else {
				second.OriginalFormat = SourceCSV
			}
			cases["two original formats"] = []SnapshotSource{normalized, original, second}
			for name, sources := range cases {
				t.Run(name, func(t *testing.T) {
					references.Sources = sources
					if _, err := referenceFiles(references); err == nil || err.Error() != "DATASET_SNAPSHOT_INVALID" {
						t.Fatal("invalid source roles accepted", err)
					}
				})
			}
		})
	}
	for name, change := range map[string]func(*SnapshotSource){
		"unknown format":                        func(v *SnapshotSource) { v.OriginalFormat = "../secret" },
		"json is not a retained draft original": func(v *SnapshotSource) { v.OriginalFormat = SourceJSON },
		"bad object":                            func(v *SnapshotSource) { v.ObjectID = "../other" },
		"bad digest":                            func(v *SnapshotSource) { v.SHA256 = "bad" },
		"empty":                                 func(v *SnapshotSource) { v.SizeBytes = 0 },
		"over ceiling":                          func(v *SnapshotSource) { v.SizeBytes = wire.MaximumBytesCeiling + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			v := normalized
			v.OriginalFormat = SourceCSV
			change(&v)
			if _, err := snapshotSourcePath(v); err == nil {
				t.Fatal("invalid original accepted")
			}
		})
	}
	references := snapshotReferences{TenantID: uuid.NewString(), Sources: []SnapshotSource{normalized}}
	if snapshotSchemaVersion(references) != normalizedSourceSnapshotSchema {
		t.Fatal("legacy version changed")
	}
	raw, err := json.Marshal(normalized)
	if err != nil || strings.Contains(string(raw), "original_format") {
		t.Fatal("legacy source encoding changed", err)
	}
}

func TestDraftSourceInventoryReportBinding(t *testing.T) {
	normalizedSHA := strings.Repeat("a", 64)
	report := SourceImportReport{SourceBytes: 2, SourceSHA256: strings.Repeat("b", 64), OutputBytes: 3, OutputSHA256: normalizedSHA}
	encode := func(v SourceImportReport) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, format := range []SourceFormat{SourceCSV, SourceParquet} {
		if _, err := decodeDraftSourceReport(format, encode(report), normalizedSHA, 3); err != nil {
			t.Fatal(err)
		}
	}
	for name, change := range map[string]func(*SourceImportReport){
		"changed normalized digest": func(v *SourceImportReport) { v.OutputSHA256 = strings.Repeat("c", 64) },
		"changed normalized count":  func(v *SourceImportReport) { v.OutputBytes++ },
		"invalid original digest":   func(v *SourceImportReport) { v.SourceSHA256 = "bad" },
		"empty original":            func(v *SourceImportReport) { v.SourceBytes = 0 },
		"over ceiling original":     func(v *SourceImportReport) { v.SourceBytes = wire.MaximumBytesCeiling + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			v := report
			change(&v)
			if _, err := decodeDraftSourceReport(SourceCSV, encode(v), normalizedSHA, 3); err == nil {
				t.Fatal("unbound receipt accepted")
			}
		})
	}
	for _, raw := range [][]byte{[]byte("null"), []byte("{broken")} {
		if _, err := decodeDraftSourceReport(SourceCSV, raw, normalizedSHA, 3); err == nil {
			t.Fatal("invalid report accepted")
		}
	}
}

func TestDatasetSnapshotSourceCodecCompatibility(t *testing.T) {
	for _, test := range []struct {
		name     string
		version  int
		original bool
		valid    bool
	}{
		{"legacy JSONL", 1, false, true}, {"complete original", 2, true, true},
		{"old version with original", 1, true, false}, {"new version without original", 2, false, false},
		{"unknown version", 3, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			normalized, original := []byte("normalized"), []byte("original")
			object, tenant := uuid.NewString(), uuid.NewString()
			references := snapshotReferences{TenantID: tenant, Sources: []SnapshotSource{{ObjectID: object, SHA256: originalInventoryDigest(normalized), SizeBytes: int64(len(normalized))}}, Versions: []SnapshotVersion{}}
			payloads := map[string][]byte{objectPath(object): normalized}
			if test.original {
				source := SnapshotSource{ObjectID: object, SHA256: originalInventoryDigest(original), SizeBytes: int64(len(original)), OriginalFormat: SourceCSV}
				references.Sources = append(references.Sources, source)
				payloads["objects/"+object+"/source.csv"] = original
			}
			namespace := storeNamespace{SchemaVersion: storeSchema, StoreID: uuid.NewString(), TenantID: tenant}
			marker, err := json.Marshal(namespace)
			if err != nil {
				t.Fatal(err)
			}
			payloads[markerFile] = marker
			files := map[string]SnapshotFile{}
			for name, raw := range payloads {
				absolute := filepath.Join(directory, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absolute, raw, 0600); err != nil {
					t.Fatal(err)
				}
				files[name] = SnapshotFile{Path: name, SHA256: originalInventoryDigest(raw), SizeBytes: int64(len(raw))}
			}
			snapshot := FileSnapshot{SchemaVersion: test.version, Namespace: namespace, References: references, Files: sortedFiles(files)}
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, snapshotName), raw, 0600); err != nil {
				t.Fatal(err)
			}
			service := &Service{Identity: &identity.Service{}}
			got, err := service.readSnapshot(context.Background(), directory)
			if test.valid && (err != nil || got.SchemaVersion != test.version) {
				t.Fatal("valid snapshot refused", err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid version/role combination accepted")
			}
			if test.valid && test.original {
				if err := os.Remove(filepath.Join(directory, "objects", object, "source.csv")); err != nil {
					t.Fatal(err)
				}
				if _, err := service.readSnapshot(context.Background(), directory); err == nil {
					t.Fatal("missing original accepted")
				}
			}
		})
	}
}
