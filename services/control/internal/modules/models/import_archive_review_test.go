package models

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/google/uuid"
)

type archiveReviewFile struct {
	name string
	mode int64
	body []byte
}

func writeReviewArchive(t *testing.T, files []archiveReviewFile) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, file := range files {
		header := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Format: tar.FormatUSTAR}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func reviewTestManifest(files []archiveReviewFile, size int) importManifest {
	catalog := wire.ModelCatalog{Format: "safetensors"}
	for _, file := range files {
		hash := sha256.Sum256(file.body)
		catalog.Files = append(catalog.Files, wire.ModelFile{Path: file.name, SizeBytes: int64(len(file.body)), SHA256: hex.EncodeToString(hash[:])})
	}
	return importManifest{catalog: catalog, minimumArchiveBytes: int64(size)}
}

type countedArchiveReview struct {
	*bytes.Reader
	bytesRead int
}

func (r *countedArchiveReview) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.Reader.ReadAt(p, offset)
	r.bytesRead += n
	return n, err
}

func TestReviewArchiveRejectsUnsafeMembersBeforeExtraction(t *testing.T) {
	const tensorBytes = 2 * 1024 * 1024
	header := []byte(`{"weight":{"dtype":"F32","shape":[524288],"data_offsets":[0,2097152]}}`)
	weights := make([]byte, 8+len(header)+tensorBytes)
	binary.LittleEndian.PutUint64(weights, uint64(len(header)))
	copy(weights[8:], header)
	files := []archiveReviewFile{
		{name: "config.json", mode: 0644, body: []byte(`{"model_type":"qwen3"}`)},
		{name: "model.safetensors", mode: 0644, body: weights},
		{name: "tokenizer_config.json", mode: 0644, body: []byte(`{"tokenizer_class":"Qwen2Tokenizer"}`)},
	}
	original := writeReviewArchive(t, files)
	manifest := reviewTestManifest(files, len(original))
	for _, data := range [][]byte{original, writeReviewArchive(t, []archiveReviewFile{files[2], files[1], files[0]})} {
		source := &countedArchiveReview{Reader: bytes.NewReader(data)}
		if err := manifest.reviewArchive(source, int64(len(data))); err != nil {
			t.Fatalf("review rejected the complete approved layout: %v", err)
		}
		if source.bytesRead >= len(data)-tensorBytes {
			t.Fatalf("review read bulk tensor data: %d of %d bytes", source.bytesRead, len(data))
		}
	}

	for _, tc := range []struct {
		name string
		edit func([]archiveReviewFile) []archiveReviewFile
		want string
	}{
		{name: "late executable", edit: func(files []archiveReviewFile) []archiveReviewFile {
			files[2].name, files[2].mode = "modeling.py", 0755
			return files
		}, want: "MODEL_ARCHIVE_INVALID"},
		{name: "executable mode on approved filename", edit: func(files []archiveReviewFile) []archiveReviewFile {
			files[0].mode = 0755
			return files
		}, want: "MODEL_ARCHIVE_INVALID"},
		{name: "renamed pickle", edit: func(files []archiveReviewFile) []archiveReviewFile {
			files[1].body = make([]byte, len(files[1].body))
			copy(files[1].body, []byte{0x80, 0x04, 'N', '.'})
			return files
		}, want: "MODEL_SAFETENSORS_INVALID"},
		{name: "modified loader selection", edit: func(files []archiveReviewFile) []archiveReviewFile {
			files[0].body = []byte(`{"model_type":"other"}`)
			return files
		}, want: "MODEL_DIGEST_MISMATCH"},
		{name: "duplicate member", edit: func(files []archiveReviewFile) []archiveReviewFile {
			files[2] = files[0]
			return files
		}, want: "MODEL_ARCHIVE_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := writeReviewArchive(t, tc.edit(append([]archiveReviewFile(nil), files...)))
			if err := manifest.reviewArchive(bytes.NewReader(data), int64(len(data))); err == nil || err.Error() != tc.want {
				t.Fatalf("archive review: %v, expected %s", err, tc.want)
			}
		})
	}
	for _, flag := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeDir, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNUSparse} {
		data := bytes.Clone(original)
		data[importArchiveTypeOffset] = flag
		if err := manifest.reviewArchive(bytes.NewReader(data), int64(len(data))); err == nil || err.Error() != "MODEL_ARCHIVE_INVALID" {
			t.Fatalf("unsupported header type %q: %v", flag, err)
		}
	}
	for _, data := range [][]byte{
		original[:len(original)-1],
		append(bytes.Clone(original), 'X'),
		append(bytes.Clone(original), make([]byte, importArchiveMaxTrailer)...),
	} {
		if err := manifest.reviewArchive(bytes.NewReader(data), int64(len(data))); err == nil || err.Error() != "MODEL_ARCHIVE_INVALID" {
			t.Fatalf("invalid termination was admitted: %v", err)
		}
	}
}

func TestArchiveReviewReadsAcrossChunksWithoutAdvancingStream(t *testing.T) {
	dir := t.TempDir()
	id := uuid.NewString()
	if err := os.MkdirAll(filepath.Join(dir, "imports", id), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first := make([]byte, ChunkBytes)
	first[len(first)-1] = 'a'
	second := []byte("bc")
	var chunks []chunk
	for i, data := range [][]byte{first, second} {
		offset := int64(i) * ChunkBytes
		hash := sha256.Sum256(data)
		if err = os.WriteFile(filepath.Join(dir, chunkPath(id, offset)), data, 0600); err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk{offset: offset, size: int64(len(data)), digest: hex.EncodeToString(hash[:])})
	}
	reader := &chunkReader{ctx: t.Context(), root: root, id: id, chunks: chunks}
	defer reader.Close()
	var content [3]byte
	if n, err := reader.ReadAt(content[:], ChunkBytes-1); err != nil || n != 3 || string(content[:]) != "abc" {
		t.Fatalf("chunk boundary read: %q, %d, %v", content, n, err)
	}
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, append(first, second...)) {
		t.Fatalf("review moved or weakened the authenticated stream: %d bytes, %v", len(got), err)
	}
}
