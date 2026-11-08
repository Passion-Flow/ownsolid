package cellclusters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestArtifactStoreRefusesWhatIsNotTheApprovedFile(t *testing.T) {
	binary := wire.ApprovedKubernetesRelease().Binary
	request := wire.KubernetesArtifactRequest{LeaseToken: "unused", Name: binary.Name, Offset: 0, Length: wire.KubernetesTransferChunkBytes}
	for name, c := range map[string]struct {
		prepare func(t *testing.T, directory string)
		code    string
	}{
		"missing": {func(*testing.T, string) {}, "KUBERNETES_ARTIFACT_UNAVAILABLE"},
		"short": {func(t *testing.T, d string) {
			if e := os.WriteFile(filepath.Join(d, binary.Name), []byte("short"), 0o444); e != nil {
				t.Fatal(e)
			}
		}, "KUBERNETES_ARTIFACT_UNAVAILABLE"},
		"writable by others": {func(t *testing.T, d string) { sized(t, filepath.Join(d, binary.Name), binary.SizeBytes, 0o666) }, "KUBERNETES_ARTIFACT_UNAVAILABLE"},
		"a link out of the directory": {func(t *testing.T, d string) {
			outside := filepath.Join(t.TempDir(), binary.Name)
			sized(t, outside, binary.SizeBytes, 0o444)
			if e := os.Symlink(outside, filepath.Join(d, binary.Name)); e != nil {
				t.Fatal(e)
			}
		}, "KUBERNETES_ARTIFACT_UNAVAILABLE"},
		"a directory": {func(t *testing.T, d string) {
			if e := os.Mkdir(filepath.Join(d, binary.Name), 0o755); e != nil {
				t.Fatal(e)
			}
		}, "KUBERNETES_ARTIFACT_UNAVAILABLE"},
		"right size, wrong content": {func(t *testing.T, d string) { sized(t, filepath.Join(d, binary.Name), binary.SizeBytes, 0o444) }, "KUBERNETES_ARTIFACT_INVALID"},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			c.prepare(t, directory)
			store := newArtifactStore(directory)
			download, e := store.Range(context.Background(), request)
			if e == nil || e.Error() != c.code || download.File != nil {
				t.Fatalf("wanted %s, observed %v", c.code, e)
			}
			if len(store.verified) != 0 {
				t.Fatal("a refused file was remembered as verified")
			}
		})
	}
	store := newArtifactStore(t.TempDir())
	for _, bad := range []wire.KubernetesArtifactRequest{
		{Name: "rke2.linux-arm64.tar.gz", Length: wire.KubernetesTransferChunkBytes},
		{Name: binary.Name, Offset: 1, Length: wire.KubernetesTransferChunkBytes},
		{Name: binary.Name, Length: wire.KubernetesTransferChunkBytes + 1},
	} {
		if _, e := store.Range(context.Background(), bad); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Errorf("%+v: %v", bad, e)
		}
	}
}

// sized writes a sparse file of the given size and mode.
func sized(t *testing.T, path string, size int64, mode os.FileMode) {
	t.Helper()
	file, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if e != nil {
		t.Fatal(e)
	}
	if e = file.Truncate(size); e != nil {
		t.Fatal(e)
	}
	file.Close()
	if e = os.Chmod(path, mode); e != nil {
		t.Fatal(e)
	}
}

func TestArtifactStoreServesVerifiedRanges(t *testing.T) {
	directory := t.TempDir()
	content := bytes.Repeat([]byte("ownsolid rke2 synthetic artifact "), 4096)
	sum := sha256.Sum256(content)
	artifact := wire.KubernetesArtifact{Name: "synthetic.tar.zst", SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	path := filepath.Join(directory, artifact.Name)
	if e := os.WriteFile(path, content, 0o444); e != nil {
		t.Fatal(e)
	}
	store := newArtifactStore(directory)
	download, e := store.rangeOf(context.Background(), artifact, 4096, 1000)
	if e != nil {
		t.Fatal(e)
	}
	defer download.File.Close()
	part := content[4096 : 4096+1000]
	partSum := sha256.Sum256(part)
	read := make([]byte, 1000)
	if _, e = download.File.ReadAt(read, download.Offset); e != nil && e != io.EOF {
		t.Fatal(e)
	}
	if download.Size != 1000 || download.SHA256 != hex.EncodeToString(partSum[:]) || !bytes.Equal(read, part) || download.ContentType != "application/octet-stream" {
		t.Fatalf("range %+v", download)
	}
	if _, ok := store.verified[artifact.Name]; !ok {
		t.Fatal("a verified file was not remembered")
	}
	// A file changed in place is hashed again, and refused when it no longer matches.
	if e = os.Chmod(path, 0o644); e != nil {
		t.Fatal(e)
	}
	changed := append([]byte(nil), content...)
	changed[0] ^= 1
	if e = os.WriteFile(path, changed, 0o644); e != nil {
		t.Fatal(e)
	}
	later := time.Now().Add(time.Minute)
	if e = os.Chtimes(path, later, later); e != nil {
		t.Fatal(e)
	}
	if _, e = store.rangeOf(context.Background(), artifact, 0, 1000); e == nil || e.Error() != "KUBERNETES_ARTIFACT_INVALID" {
		t.Fatal("a changed file was served", e)
	}
}
