package releases

import (
	"encoding/json"
	"os"
	"path/filepath"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"strings"
	"testing"
)

func readerFixture(t *testing.T) (Config, view.Snapshot) {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	s := view.Snapshot{Format: view.Format, ScopeID: strings.Repeat("a", 32), ReleaseID: "synthetic-reader", Profile: "test-fixture", ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), ArchiveSHA256: strings.Repeat("d", 64), Sequence: 1, HeadSHA256: strings.Repeat("e", 64), JournalBytes: 200, FirstSequence: 1, RecordedAt: "2026-10-24T00:00:00Z", Events: []view.Event{{Sequence: 1, OperationID: strings.Repeat("f", 32), Action: "release_prepare", Outcome: "failed", Code: "RELEASE_INPUT_INVALID", Time: "2026-10-24T00:00:00Z"}}}
	raw, _ := json.Marshal(s)
	name := filepath.Join(dir, view.SnapshotFile)
	if e := os.WriteFile(name, raw, 0444); e != nil {
		t.Fatal(e)
	}
	os.Chmod(name, 0444)
	return Config{Directory: dir, ScopeID: s.ScopeID, ManifestSHA256: s.ManifestSHA256, ConfigurationSHA256: s.ConfigurationSHA256, PublisherUID: os.Geteuid(), PageSize: 1, OperationSeconds: 1}, s
}
func TestReleaseViewRealFilesAndSimulatedDistinctUIDRules(t *testing.T) {
	// This simulates only the observed reader identity. It does not qualify a real
	// cross-UID OS/container mount; production readSnapshot always uses os.Geteuid.
	c, want := readerFixture(t)
	uid := c.PublisherUID + 1
	s, e := readSnapshotForUID(c, uid)
	if e != nil || s.HeadSHA256 != want.HeadSHA256 {
		t.Fatal(e)
	}
	if _, e = readSnapshotForUID(c, c.PublisherUID); e == nil {
		t.Fatal("same publisher identity allowed")
	}
	if _, e = readSnapshotForUID(c, 0); e == nil {
		t.Fatal("privileged reader admitted")
	}
	for _, name := range []string{view.DirtyFile} {
		p := filepath.Join(c.Directory, name)
		os.WriteFile(p, []byte("pending"), 0444)
		if _, e = readSnapshotForUID(c, uid); e == nil {
			t.Fatal("dirty old success")
		}
		os.Remove(p)
	}
	other := c
	other.ScopeID = strings.Repeat("1", 32)
	if _, e = readSnapshotForUID(other, uid); e == nil {
		t.Fatal("foreign binding")
	}
	file := filepath.Join(c.Directory, view.SnapshotFile)
	os.Chmod(file, 0644)
	if _, e = readSnapshotForUID(c, uid); e == nil {
		t.Fatal("writable source")
	}
	os.Chmod(file, 0444)
	link := filepath.Join(c.Directory, "hardlink")
	if e = os.Link(file, link); e != nil {
		t.Fatal(e)
	}
	if _, e = readSnapshotForUID(c, uid); e == nil {
		t.Fatal("hardlinked source")
	}
	os.Remove(link)
	real := filepath.Join(c.Directory, "retained.json")
	os.Rename(file, real)
	os.Symlink(real, file)
	if _, e = readSnapshotForUID(c, uid); e == nil {
		t.Fatal("symlink source")
	}
}
