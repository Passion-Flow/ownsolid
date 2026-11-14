package modelartifact

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func encodedTensor(header string, data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint64(len(header)))
	b.WriteString(header)
	b.Write(data)
	return b.Bytes()
}
func TestSafetensorsRejectsAmbiguousAndUnboundedLayouts(t *testing.T) {
	valid := encodedTensor(`{"weight":{"dtype":"BF16","shape":[2],"data_offsets":[0,4]}}`, make([]byte, 4))
	if e := VerifySafetensors(bytes.NewReader(valid), int64(len(valid))); e != nil {
		t.Fatal(e)
	}
	for _, header := range []string{
		`{"a":{"dtype":"BF16","shape":[2],"data_offsets":[0,4]},"a":{"dtype":"BF16","shape":[2],"data_offsets":[0,4]}}`,
		`{"a":{"dtype":"BF16","shape":[2],"data_offsets":[1,5]}}`,
		`{"a":{"dtype":"BF16","shape":[9223372036854775807,2],"data_offsets":[0,4]}}`,
		`{"a":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]},"b":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]}}`,
		`{"a":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]}}`,
		`{"__metadata__":{"invalid":1},"a":{"dtype":"BF16","shape":[2],"data_offsets":[0,4]}}`,
	} {
		data := encodedTensor(header, make([]byte, 4))
		if VerifySafetensors(bytes.NewReader(data), int64(len(data))) == nil {
			t.Fatal("ambiguous/corrupt tensor layout accepted")
		}
	}
	var oversized bytes.Buffer
	binary.Write(&oversized, binary.LittleEndian, uint64(2*1024*1024))
	if VerifySafetensors(&oversized, 3*1024*1024) == nil {
		t.Fatal("unbounded header accepted")
	}
}
func TestModelArchiveRejectsUnapprovedPathsTypesAndTruncation(t *testing.T) {
	for _, header := range []tar.Header{{Name: "../escape", Mode: 0444, Size: 1}, {Name: "LICENSE", Mode: 0444, Typeflag: tar.TypeSymlink, Linkname: "/unowned"}, {Name: "LICENSE", Mode: 0755, Size: 11343}, {Name: "LICENSE", Mode: 0444, Size: 1}} {
		var b bytes.Buffer
		writer := tar.NewWriter(&b)
		if e := writer.WriteHeader(&header); e != nil {
			t.Fatal(e)
		}
		writer.Write([]byte("x"))
		writer.Close()
		stage := t.TempDir()
		if ExtractArchive(context.Background(), &b, stage) == nil {
			t.Fatal("unsafe archive accepted")
		}
		files, _ := os.ReadDir(stage)
		if len(files) != 0 {
			t.Fatal("rejected metadata wrote a file")
		}
	}
	if ExtractArchive(context.Background(), bytes.NewReader(nil), t.TempDir()) == nil {
		t.Fatal("empty package accepted")
	}
	stage := t.TempDir()
	os.WriteFile(filepath.Join(stage, "unowned"), []byte("preserve"), 0600)
	if ExtractArchive(context.Background(), bytes.NewReader(nil), stage) == nil {
		t.Fatal("nonempty stage adopted")
	}
}
