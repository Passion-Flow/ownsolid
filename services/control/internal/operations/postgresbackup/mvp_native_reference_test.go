package postgresbackup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMVPReferenceEnvelopePreservesHistoricalJSON(t *testing.T) {
	backup := Readback{Format: "historical", BackupID: "synthetic", CurrentLockHeld: false}
	original, e := canonical(backup)
	if e != nil {
		t.Fatal(e)
	}
	wrapped, e := canonical(BackupObservation{Readback: backup})
	if e != nil || !bytes.Equal(original, wrapped) {
		t.Fatal("legacy backup response changed", e)
	}
	restored := RestoreObservation{Format: "historical", ReadOnly: true, OriginalCompletionPresent: false}
	original, e = canonical(restored)
	if e != nil {
		t.Fatal(e)
	}
	wrapped, e = canonical(RestoreObservationWithReference{RestoreObservation: restored})
	if e != nil || !bytes.Equal(original, wrapped) {
		t.Fatal("legacy restore response changed", e)
	}
}

func TestMVPReferencePinsRejectReplacementAndSameByteABA(t *testing.T) {
	for _, kind := range []string{"same-byte-rewrite-restored-mtime", "replacement-inode", "absent-completion-created-and-removed"} {
		t.Run(kind, func(t *testing.T) {
			root := privateTestRoot(t)
			path := filepath.Join(root, "record.json")
			optional := kind == "absent-completion-created-and-removed"
			raw := []byte("synthetic record\n")
			if !optional {
				if e := os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			e := withNativeReferenceRecords(t.Context(), []nativeReferenceRecord{{path, 4096, optional}}, func() error {
				// Mutate on a later filesystem clock tick so ctime comparisons
				// are deterministic even when mtime is restored deliberately.
				time.Sleep(20 * time.Millisecond)
				if optional {
					info, e := os.Stat(root)
					if e != nil {
						return e
					}
					if e = os.WriteFile(path, raw, 0600); e != nil {
						return e
					}
					if e = os.Remove(path); e != nil {
						return e
					}
					return os.Chtimes(root, info.ModTime(), info.ModTime())
				}
				info, e := os.Stat(path)
				if e != nil {
					return e
				}
				if kind == "replacement-inode" {
					replacement := filepath.Join(root, "replacement.json")
					if e = os.WriteFile(replacement, raw, 0600); e != nil {
						return e
					}
					return os.Rename(replacement, path)
				}
				if e = os.WriteFile(path, raw, 0600); e != nil {
					return e
				}
				return os.Chtimes(path, info.ModTime(), info.ModTime())
			})
			if e == nil {
				t.Fatal("record changed during verified projection", kind)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	if e := withNativeReferenceRecords(ctx, nil, func() error { ran = true; return nil }); !errors.Is(e, context.Canceled) || ran {
		t.Fatal("cancelled projection executed", e)
	}
}

// This private-helper case proves only canonical metadata binding/redaction.
// The public observer below must still refuse these non-executable fake records.
func TestMVPReferenceMetadataNeverSubstitutesForOriginalVerification(t *testing.T) {
	root := privateTestRoot(t)
	m := mvpBindingManifestFixture()
	m.Format = "projectone.postgres.matched-backup.v1"
	m.Request.BackupRoot = root
	m.RequestSHA256 = requestDigest(m.Request)
	m.Database.SchemaVersion = 33
	m.Database.Tables = 110
	m.Archive.SHA256 = strings.Repeat("d", 64)
	m.Sources = []TreeReadback{}
	m.Stores = []StoreReadback{}
	if !m.Request.Valid() {
		t.Fatal("valid explicit request required before original-verifier refusal control")
	}
	raw, e := canonical(m)
	if e != nil {
		t.Fatal(e)
	}
	expected := digest(raw)
	write := func(name string, value any) []byte {
		t.Helper()
		raw, e := canonical(value)
		if e != nil || os.WriteFile(filepath.Join(root, name), raw, 0600) != nil {
			t.Fatal("private synthetic metadata", e)
		}
		return raw
	}
	write(candidateName, m)
	var decoded Manifest
	if _, e = readPrivateDocument(filepath.Join(root, candidateName), m.Request.Policy.MaximumManifestBytes, &decoded); e != nil {
		t.Fatal("synthetic candidate must pass the original closed decoder", e)
	}
	if e = validMVPSourceBinding(decoded); e != nil {
		t.Fatal("synthetic source binding must pass before metadata projection", e)
	}
	prior := Readback{Format: "projectone.postgres.independent-readback.v1", BackupID: m.Request.BackupID, RequestSHA256: m.RequestSHA256, ManifestSHA256: expected, ArchiveSHA256: m.Archive.SHA256, ArchiveDirectorySHA256: strings.Repeat("e", 64), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	priorRaw := write(readbackName, prior)
	complete := Completion{Format: "projectone.postgres.backup-complete.v1", BackupID: m.Request.BackupID, ManifestSHA256: expected, ReadbackSHA256: digest(priorRaw), CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	completeRaw := write(completeName, complete)
	reference, _, e := projectNativeBackupReference(m.Request, expected)
	if e != nil || !reference.Valid() || reference.CompletionSHA256 != digest(completeRaw) || reference.ReadbackSHA256 != digest(priorRaw) || reference.MVPSourceSHA256 != valueDigest(m.MVPSource) {
		t.Fatal("canonical original metadata hashes", e)
	}
	public, e := canonical(reference)
	if e != nil || bytes.Contains(public, []byte(root)) || bytes.Contains(public, []byte(m.MVPSource.RuntimePrincipal)) || bytes.Contains(public, []byte("principal_verifier")) {
		t.Fatal("private fields escaped safe reference", e)
	}
	observed, e := ObserveBackup(t.Context(), m.Request, expected)
	if e == nil || observed.NativePostgreSQL != nil {
		t.Fatal("metadata-only records bypassed original native verification", e)
	}
	complete.ReadbackSHA256 = strings.Repeat("f", 64)
	write(completeName, complete)
	if _, _, e = projectNativeBackupReference(m.Request, expected); e == nil {
		t.Fatal("changed original durable readback link accepted")
	}
}
