package modelartifact

import (
	"archive/tar"
	"io"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type archiveCountingWriter struct{ bytes int64 }

func (w *archiveCountingWriter) Write(p []byte) (int, error) {
	w.bytes += int64(len(p))
	return len(p), nil
}

type archiveZeroReader struct{}

func (archiveZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestMinimumArchiveBytesMatchesActualUSTARSizing(t *testing.T) {
	var counted archiveCountingWriter
	w := tar.NewWriter(&counted)
	for _, file := range wire.ApprovedModel().Files {
		if e := w.WriteHeader(&tar.Header{Name: file.Path, Size: file.SizeBytes, Mode: 0444, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if n, e := io.CopyN(w, archiveZeroReader{}, file.SizeBytes); e != nil || n != file.SizeBytes {
			t.Fatal("actual bounded-memory USTAR sizing", e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if MinimumArchiveBytes() != counted.bytes || counted.bytes != 1519216128 {
		t.Fatalf("USTAR protocol size got=%d expected=%d", MinimumArchiveBytes(), counted.bytes)
	}
}
