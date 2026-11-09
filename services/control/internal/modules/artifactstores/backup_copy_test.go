package artifactstores

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestBackupArchiveHeaderSupportsLargeObjects(t *testing.T) {
	const size int64 = 12 << 30
	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	if e := writeBackupHeader(archive, "objects/workspace/object", size); e != nil {
		t.Fatal(e)
	}
	// Decode the streamed header without allocating or writing the large body.
	entry, e := tar.NewReader(&buf).Next()
	if e != nil || entry.Size != size || entry.Format != tar.FormatPAX || entry.Mode != 0600 {
		t.Fatalf("large object header did not round trip: %v %v", entry, e)
	}
}
