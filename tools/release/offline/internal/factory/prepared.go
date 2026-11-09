package factory

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The v1 unpack operation has two completed events and no subsequent mutations.
const maximumPreparationJournalBytes = 1 << 20
const directoryReadBatchEntries = 64

type preparedMarker struct {
	Status         string `json:"status"`
	ArchiveSHA256  string `json:"archive_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Profile        string `json:"profile"`
	ReleaseID      string `json:"release_id"`
}
type preparationEvent struct {
	Status         string `json:"status"`
	Code           string `json:"code"`
	ArchiveSHA256  string `json:"archive_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Time           string `json:"time"`
}

// VerifyPrepared rereads every exact file without trusting the ready marker alone.
func VerifyPrepared(ctx context.Context, c Config, directory, archiveSHA, manifestSHA string) (Result, error) {
	return verifyPrepared(ctx, c, directory, archiveSHA, manifestSHA, nil)
}

type preparedAuthority struct {
	manifest  []byte
	signature []byte
}

func verifyPrepared(ctx context.Context, c Config, directory, archiveSHA, manifestSHA string, authority *preparedAuthority) (Result, error) {
	result := Result{}
	if ctx == nil || ctx.Err() != nil {
		return result, fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
	}
	if !wire.ValidSHA256(archiveSHA) || !wire.ValidSHA256(manifestSHA) {
		return result, fail("RELEASE_INDEPENDENT_EXPECTED_DIGESTS_REQUIRED")
	}
	if mounted(c.MountRoot) != nil || !within(c.MountRoot, directory) || privateDirectory(directory) != nil {
		return result, fail("RELEASE_PRIVATE_PREPARED_ROOT_REQUIRED")
	}
	rootBefore, e := os.Lstat(directory)
	if e != nil {
		return result, fail("RELEASE_PREPARED_ROOT_UNAVAILABLE")
	}
	root, e := openPreparedDirectory(directory, rootBefore)
	if e != nil {
		return result, e
	}
	defer root.Close()
	journalBefore, e := os.Lstat(directory + ".journal.jsonl")
	if e != nil {
		return result, fail("RELEASE_COMPLETED_PREPARATION_JOURNAL_REQUIRED")
	}
	markerRaw, e := readRegular(filepath.Join(directory, PreparedMarker), c.MaximumMetadataBytes, true)
	if e != nil {
		return result, e
	}
	var marker preparedMarker
	if wire.StrictJSON(markerRaw, &marker) != nil || marker.Status != "prepared_verified_inventory_only" || marker.ArchiveSHA256 != archiveSHA || marker.ManifestSHA256 != manifestSHA {
		return result, fail("RELEASE_PREPARED_MARKER_INVALID")
	}
	journal, e := readRegular(directory+".journal.jsonl", maximumPreparationJournalBytes, true)
	if e != nil {
		return result, fail("RELEASE_COMPLETED_PREPARATION_JOURNAL_REQUIRED")
	}
	if !bytes.HasSuffix(journal, []byte("\n")) {
		return result, fail("RELEASE_PREPARATION_JOURNAL_TRUNCATED")
	}
	lines := bytes.Split(bytes.TrimSuffix(journal, []byte("\n")), []byte("\n"))
	if len(lines) != 2 {
		return result, fail("RELEASE_PREPARATION_JOURNAL_INVALID")
	}
	for i, line := range lines {
		var event preparationEvent
		expected := "reserved"
		if i == 1 {
			expected = "prepared_verified_inventory_only"
		}
		if wire.StrictJSON(line, &event) != nil || event.Status != expected || event.Code != "" || event.ArchiveSHA256 != archiveSHA || event.ManifestSHA256 != manifestSHA {
			return result, fail("RELEASE_PREPARATION_JOURNAL_INVALID")
		}
		if _, e = time.Parse(time.RFC3339Nano, event.Time); e != nil {
			return result, fail("RELEASE_PREPARATION_JOURNAL_TIME_INVALID")
		}
	}
	raw, e := readRegular(filepath.Join(directory, "manifest.json"), c.MaximumManifestBytes, true)
	if e != nil || wire.Digest(raw) != manifestSHA {
		return result, fail("RELEASE_PREPARED_MANIFEST_INVALID")
	}
	var m wire.Manifest
	if wire.StrictJSON(raw, &m) != nil {
		return result, fail("RELEASE_PREPARED_INVENTORY_INVALID")
	}
	c = verificationConfigForManifest(c, m)
	signature, e := readRegular(filepath.Join(directory, "signature.json"), c.MaximumMetadataBytes, true)
	if e != nil {
		return result, e
	}
	trust, e := loadTrust(c)
	if e != nil {
		return result, e
	}
	if e = verifySignature(raw, signature, trust); e != nil {
		return result, e
	}
	if c.validateManifest(m) != nil || marker.Profile != m.Profile || marker.ReleaseID != m.ReleaseID {
		return result, fail("RELEASE_PREPARED_INVENTORY_INVALID")
	}
	type expectedFile struct {
		size   int64
		digest string
	}
	files := map[string]expectedFile{"manifest.json": {int64(len(raw)), wire.Digest(raw)}, "signature.json": {int64(len(signature)), wire.Digest(signature)}, PreparedMarker: {int64(len(markerRaw)), wire.Digest(markerRaw)}}
	directories := map[string]bool{".": true}
	licenseAssets := map[string]wire.Asset{}
	for _, a := range m.Assets {
		files[a.Path] = expectedFile{a.Size, a.SHA256}
		if a.Kind == wire.LicenseTrustAssetKind {
			licenseAssets[a.Path] = a
		}
		for parent := path.Dir(a.Path); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	queue := []string{"."}
	seen := map[string]bool{}
	fileSnapshots := map[string]os.FileInfo{}
	directorySnapshots := map[string]os.FileInfo{".": rootBefore}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return result, fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
		}
		current := queue[0]
		queue = queue[1:]
		e = readPreparedDirectory(ctx, filepath.Join(directory, filepath.FromSlash(current)), directorySnapshots[current], func(folder *os.Root, entry os.DirEntry) error {
			name := path.Join(current, entry.Name())
			before, se := folder.Lstat(entry.Name())
			if se != nil || !owned(before) {
				return fail("RELEASE_PREPARED_FILE_IDENTITY_INVALID")
			}
			if before.Mode().IsDir() {
				if !directories[name] || directorySnapshots[name] != nil {
					return fail("RELEASE_PREPARED_UNKNOWN_DIRECTORY")
				}
				directorySnapshots[name] = before
				queue = append(queue, name)
				return nil
			}
			expected, known := files[name]
			if !known || seen[name] {
				return fail("RELEASE_PREPARED_UNKNOWN_OR_UNSAFE_FILE")
			}
			if se = readPreparedFile(ctx, folder, entry.Name(), before, expected.size, expected.digest, licenseAssets[name]); se != nil {
				return se
			}
			seen[name] = true
			fileSnapshots[name] = before
			return nil
		})
		if e != nil {
			return result, e
		}
	}
	if len(seen) != len(files) || len(directorySnapshots) != len(directories) {
		return result, fail("RELEASE_PREPARED_FILE_MISSING")
	}
	// Recheck the small authority files after the tree scan to detect concurrent edits.
	for name, expected := range map[string][]byte{PreparedMarker: markerRaw, "manifest.json": raw, "signature.json": signature} {
		stored, e := readRegular(filepath.Join(directory, name), int64(len(expected)), true)
		if e != nil || !bytes.Equal(stored, expected) {
			return result, fail("RELEASE_PREPARED_AUTHORITY_CHANGED")
		}
	}
	stored, e := readRegular(directory+".journal.jsonl", maximumPreparationJournalBytes, true)
	if e != nil || !bytes.Equal(stored, journal) {
		return result, fail("RELEASE_PREPARATION_JOURNAL_CHANGED")
	}
	journalAfter, e := os.Lstat(directory + ".journal.jsonl")
	if e != nil || !sameRegularSnapshot(journalBefore, journalAfter, maximumPreparationJournalBytes, true, 1) {
		return result, fail("RELEASE_PREPARATION_JOURNAL_CHANGED")
	}
	if e = verifyPreparedSnapshot(ctx, root, fileSnapshots, directorySnapshots); e != nil {
		return result, e
	}
	if authority != nil {
		*authority = preparedAuthority{manifest: raw, signature: signature}
	}
	qualification := "verified_prepared_inventory_only_not_application_installation_or_target"
	if m.Profile == wire.FixtureProfile {
		qualification = "synthetic_prepared_fixture_only_never_complete_platform_installation"
	}
	return Result{Status: "passed_verified_prepared_inventory", Profile: m.Profile, ReleaseID: m.ReleaseID, ManifestSHA256: manifestSHA, ArchiveSHA256: archiveSHA, Assets: len(m.Assets), Qualification: qualification, Manifest: m}, nil
}
