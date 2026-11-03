package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeArchiveFixture(t *testing.T, fault string) ([]byte, NativePostgreSQLArchiveArtifact) {
	t.Helper()
	root := "source"
	headers := []tar.Header{{Name: root, Typeflag: tar.TypeDir, Mode: 0700}, {Name: root + "/models", Typeflag: tar.TypeDir, Mode: 0555}, {Name: root + "/models/model.bin", Typeflag: tar.TypeReg, Mode: 0444}, {Name: root + "/configuration", Typeflag: tar.TypeDir, Mode: 0700}, {Name: root + "/configuration/\u6388\u6743 data.txt", Typeflag: tar.TypeReg, Mode: 0644}}
	for _, name := range []string{"candidate.json", "complete.json", "independent-readback.json", "postgres.dump", "effective-control.yaml"} {
		headers = append(headers, tar.Header{Name: root + "/" + name, Typeflag: tar.TypeReg, Mode: 0600})
	}
	if fault == "long-path" {
		headers = append(headers, tar.Header{Name: root + "/configuration/" + strings.Repeat("a", 160), Typeflag: tar.TypeReg, Mode: 0400, Format: tar.FormatPAX})
	}
	if fault == "missing-record" {
		headers = headers[:len(headers)-1]
	}
	if fault == "duplicate" {
		headers = append(headers, headers[len(headers)-1])
	}
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for i, h := range headers {
		h.Uid, h.Gid = 1000, 1000
		body := []byte("explicit synthetic native payload")
		if h.Typeflag == tar.TypeDir {
			body = nil
		}
		h.Size = int64(len(body))
		if fault == "changed-header-time" {
			h.ModTime = time.Unix(1700000000, 0)
		}
		if i == 2 {
			switch fault {
			case "owner":
				h.Uid = 0
			case "group":
				h.Gid = 0
			case "world-write":
				h.Mode = 0666
			case "setuid":
				h.Mode = 04700
			case "link":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "/foreign"
				h.Size = 0
				body = nil
			case "hardlink":
				h.Typeflag = tar.TypeLink
				h.Linkname = root + "/candidate.json"
				h.Size = 0
				body = nil
			case "escape":
				h.Name = root + "/../foreign"
			case "absolute":
				h.Name = "/foreign"
			case "parent":
				h.Name = root + "/absent/child"
			case "sparse-pax":
				h.Format = tar.FormatPAX
				h.PAXRecords = map[string]string{"vendor.fixturex": "foreign"}
			case "xattr":
				h.Xattrs = map[string]string{"user.untrusted": "x"}
			case "file-trailing-slash":
				// Patch a raw header below; archive/tar.Writer refuses it.
			case "changed-mode":
				h.Mode = 0600
			}
		}
		if fault == "bad-directory-mode" && i == 1 {
			h.Mode = 0755
		}
		if e := writer.WriteHeader(&h); e != nil {
			t.Fatal(e)
		}
		if _, e := writer.Write(body); e != nil {
			t.Fatal(e)
		}
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	out := raw.Bytes()
	if fault == "sparse-pax" {
		out = bytes.ReplaceAll(out, []byte("vendor.fixturex"), []byte("GNU.sparse.name"))
	}
	if fault == "file-trailing-slash" {
		header := out[2*tarBlockBytes : 3*tarBlockBytes]
		for i := 0; i < 100; i++ {
			header[i] = 0
		}
		copy(header, []byte(root+"/models/model.bin/"))
		for i := 148; i < 156; i++ {
			header[i] = ' '
		}
		sum := 0
		for _, b := range header {
			sum += int(b)
		}
		copy(header[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	}
	if fault == "truncated" {
		out = out[:len(out)-tarEndBytes]
	}
	if fault == "extra-tail" {
		out = append(out, []byte("private undeclared payload")...)
	}
	a := NativePostgreSQLArchiveArtifact{RootName: root, UID: 1000, GID: 1000, Bytes: int64(len(out)), SHA256: wire.Digest(out), MaximumEntries: 64, MaximumManifestBytes: 4096}
	return out, a
}

func TestNativePostgreSQLArchiveParserPreservesCompleteNativeModesAndLongPaths(t *testing.T) {
	for _, fault := range []string{"", "long-path"} {
		t.Run(fault, func(t *testing.T) {
			raw, a := nativeArchiveFixture(t, fault)
			var copied bytes.Buffer
			out, e := readNativePostgreSQLArchive(bytes.NewReader(raw), &copied, a, a.Bytes)
			if e != nil || !out.Profile.valid() || out.Profile.Artifact != a || !bytes.Equal(raw, copied.Bytes()) {
				t.Fatal("original native bytes changed/refused", e)
			}
			immutable, unicode := false, false
			for _, entry := range out.Entries {
				if entry.Name == "models" {
					immutable = entry.Type == "directory" && entry.Mode == 0555
				}
				if entry.Name == "configuration/\u6388\u6743 data.txt" {
					unicode = entry.Mode == 0644 && entry.SHA256 != ""
				}
			}
			if !immutable || !unicode {
				t.Fatal("original native mode/name not retained")
			}
		})
	}
}

func TestNativePostgreSQLArchiveParserRejectsUnsafeTreeAndBounds(t *testing.T) {
	for _, fault := range []string{"owner", "group", "world-write", "setuid", "link", "hardlink", "escape", "absolute", "parent", "bad-directory-mode", "duplicate", "missing-record", "sparse-pax", "xattr", "file-trailing-slash", "truncated", "extra-tail"} {
		t.Run(fault, func(t *testing.T) {
			raw, a := nativeArchiveFixture(t, fault)
			if _, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes); e == nil {
				t.Fatal("unsafe native archive admitted")
			}
		})
	}
	raw, a := nativeArchiveFixture(t, "")
	a.MaximumEntries = 6
	if _, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes); e == nil {
		t.Fatal("entry limit ignored")
	}
	a.MaximumEntries = 64
	if _, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes-1); e == nil {
		t.Fatal("byte limit ignored")
	}
	a.MaximumManifestBytes = 4096
	a.RootName = "../source"
	if _, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes); e == nil {
		t.Fatal("unsafe frozen root accepted")
	}
}

func TestNativePostgreSQLArchiveParserSeparatesTransportTarAndInventoryIdentity(t *testing.T) {
	raw, a := nativeArchiveFixture(t, "")
	other, b := nativeArchiveFixture(t, "changed-header-time")
	first, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	second, e := readNativePostgreSQLArchive(bytes.NewReader(other), io.Discard, b, b.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	if first.Profile.Artifact.SHA256 == second.Profile.Artifact.SHA256 || first.Profile.EntriesSHA256 != second.Profile.EntriesSHA256 {
		t.Fatal("transport headers confused with payload identity")
	}
	changed, c := nativeArchiveFixture(t, "changed-mode")
	third, e := readNativePostgreSQLArchive(bytes.NewReader(changed), io.Discard, c, c.Bytes)
	if e != nil || third.Profile.EntriesSHA256 == first.Profile.EntriesSHA256 {
		t.Fatal("original mode not bound", e)
	}
}

func TestNativePostgreSQLArchiveDescriptorProtectedHostIdentity(t *testing.T) {
	raw, a := nativeArchiveFixture(t, "")
	name := filepath.Join(t.TempDir(), "native.tar")
	if e := os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	p, e := DescribeNativePostgreSQLArchive(context.Background(), f, a)
	if e != nil || !p.valid() {
		t.Fatal(e)
	}
	a.SHA256 = nativeObservationHash("f")
	if _, e := DescribeNativePostgreSQLArchive(context.Background(), f, a); e == nil {
		t.Fatal("independent archive hash ignored")
	}
	a = p.Artifact
	os.Chmod(name, 0644)
	if _, e := DescribeNativePostgreSQLArchive(context.Background(), f, a); e == nil {
		t.Fatal("public source tar accepted")
	}
	os.Chmod(name, 0600)
	if e := os.Link(name, name+"-other"); e != nil {
		t.Fatal(e)
	}
	if _, e := DescribeNativePostgreSQLArchive(context.Background(), f, a); e == nil {
		t.Fatal("multiply linked source accepted")
	}
}

func TestNativePostgreSQLArchiveInputProfileRequiresOwnedParentRecipe(t *testing.T) {
	raw, a := nativeArchiveFixture(t, "")
	out, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	p := out.Profile
	v := nativeBackupVerifierTaskFixture()
	v.NativePostgreSQLBackupVerifier.BackupParent = true
	if _, ok := v.nativeArchiveInput(p); !ok {
		t.Fatal("fixed verifier parent input refused")
	}
	v.NativePostgreSQLBackupVerifier.BackupParent = false
	if _, ok := v.nativeArchiveInput(p); ok {
		t.Fatal("direct root input silently rewritten")
	}
	restore := nativeRecoveryFixture(NativePostgreSQLRestore)
	if _, ok := restore.nativeArchiveInput(p); !ok {
		t.Fatal("fresh target native tree input refused")
	}
	p.Artifact.UID++
	if _, ok := restore.nativeArchiveInput(p); ok {
		t.Fatal("foreign original numeric owner accepted")
	}
	if _, ok := nativeRecoveryFixture(NativePostgreSQLBackup).nativeArchiveInput(out.Profile); ok {
		t.Fatal("backup output accepted as import input")
	}
}
