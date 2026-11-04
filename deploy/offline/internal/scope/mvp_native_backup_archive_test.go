package scope

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Synthetic complete transport inventory, never an original SQL/native proof.
func mvpNativeArchiveFixture(t *testing.T) (*os.Root, *os.File, engine.NativePostgreSQLArchiveArtifact, configuration.BackupRequest) {
	t.Helper()
	dir := t.TempDir()
	unsealOnCleanup(t, dir)
	os.Chmod(dir, 0700)
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Close() })
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	top := strings.Repeat("c", 32)
	entry := func(name string, mode int64, raw []byte, directory bool) {
		t.Helper()
		kind := byte(tar.TypeReg)
		if directory {
			kind = tar.TypeDir
		}
		if e := writer.WriteHeader(&tar.Header{Name: top + name, Mode: mode, Size: int64(len(raw)), Uid: 1000, Gid: 1000, Typeflag: kind, Format: tar.FormatPAX}); e != nil {
			t.Fatal(e)
		}
		if _, e := writer.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	entry("", 0700, nil, true)
	for _, name := range []string{"candidate.json", "complete.json", "independent-readback.json", "postgres.dump", "effective-control.yaml"} {
		entry("/"+name, 0600, []byte("explicit transport fixture\n"), false)
	}
	for _, name := range []string{"stores", "stores/models"} {
		entry("/"+name, 0700, nil, true)
	}
	entry("/stores/models/sealed", 0555, nil, true)
	entry("/stores/models/sealed/empty", 0444, nil, false)
	entry("/stores/models/\u539f\u59cb \u6587\u4ef6-"+strings.Repeat("a", 160), 0644, []byte("immutable native bytes"), false)
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	file, e := root.OpenFile(mvpNativeArchiveFile, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { file.Close() })
	if _, e = file.Write(output.Bytes()); e != nil {
		t.Fatal(e)
	}
	if e = file.Sync(); e != nil {
		t.Fatal(e)
	}
	a := engine.NativePostgreSQLArchiveArtifact{RootName: top, UID: 1000, GID: 1000, Bytes: int64(output.Len()), SHA256: wire.Digest(output.Bytes()), MaximumEntries: 100, MaximumManifestBytes: 1 << 20}
	limits := configuration.BackupDefaults()
	limits.DestinationRoot = dir
	limits.Reason = "Explicit original transport copy fixture"
	limits.MaximumBytes = 1 << 20
	limits.MaximumEntries = 200
	return root, file, a, limits
}

func TestMVPNativeArchiveExtractionRetainsOriginalTarAndPortableNativeTree(t *testing.T) {
	root, file, a, limits := mvpNativeArchiveFixture(t)
	profile, e := extractMVPNativeArchive(context.Background(), root, file, a, limits, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if profile.Artifact != a {
		t.Fatal("original descriptor changed")
	}
	entries, _, e := inventoryMVPNativeSource(context.Background(), root, limits.MaximumBytes, limits.MaximumEntries, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyMVPNativeCopiedArchive(entries, profile); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(filepath.Join(limits.DestinationRoot, mvpNativeArchiveFile))
	if e != nil || wire.Digest(original) != a.SHA256 {
		t.Fatal("original tar rewritten", e)
	}
	info, e := root.Stat(mvpNativeDirectory + "/stores/models/sealed")
	if e != nil || info.Mode() != os.ModeDir|0555 {
		t.Fatal("original sealed mode changed", e)
	}
	if _, e = extractMVPNativeArchive(context.Background(), root, file, a, limits, 1<<20); e == nil {
		t.Fatal("uncertain extraction replayed")
	}
	if _, e = root.Lstat(backupManifestFile); !os.IsNotExist(e) {
		t.Fatal("transport copy fabricated native completion")
	}
}

func TestMVPNativeArchiveExtractionRejectsChangedBytesBeforeCreatingChild(t *testing.T) {
	root, file, a, limits := mvpNativeArchiveFixture(t)
	if _, e := file.WriteAt([]byte("changed"), 1024); e != nil {
		t.Fatal(e)
	}
	file.Sync()
	if _, e := extractMVPNativeArchive(context.Background(), root, file, a, limits, 1<<20); e == nil {
		t.Fatal("changed original archive accepted")
	}
	if _, e := root.Lstat(mvpNativeDirectory); !os.IsNotExist(e) {
		t.Fatal("unverified native child created")
	}
}

func TestMVPNativeArchiveCopiedModeAndContentRemainOriginalIdentity(t *testing.T) {
	root, file, a, limits := mvpNativeArchiveFixture(t)
	profile, e := extractMVPNativeArchive(context.Background(), root, file, a, limits, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e = root.Chmod(mvpNativeDirectory+"/stores/models/sealed/empty", 0600); e != nil {
		t.Fatal(e)
	}
	entries, _, e := inventoryMVPNativeSource(context.Background(), root, limits.MaximumBytes, limits.MaximumEntries, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyMVPNativeCopiedArchive(entries, profile); e == nil {
		t.Fatal("changed source mode admitted")
	}
}
