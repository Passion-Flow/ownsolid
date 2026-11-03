package artifactstores

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestObjectSnapshotStrictArchiveDigestScopeAndTrailingBytes(t *testing.T) {
	source := []byte("Synthetic snapshot source \u4e16\u754c")
	sum := sha256.Sum256(source)
	manifest := SnapshotManifest{Schema: snapshotSchema, TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ObjectID: uuid.NewString(), NamespaceID: uuid.NewString(), BackendSHA256: strings.Repeat("a", 64), Purpose: "dataset_source", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(source))}
	raw, _ := json.Marshal(manifest)
	var out bytes.Buffer
	if e := writeSnapshot(&out, raw, bytes.NewReader(source), int64(len(source))); e != nil {
		t.Fatal(e)
	}
	if e := VerifySnapshot(context.Background(), bytes.NewReader(out.Bytes()), manifest); e != nil {
		t.Fatal(e)
	}
	other := manifest
	other.WorkspaceID = uuid.NewString()
	if e := VerifySnapshot(context.Background(), bytes.NewReader(out.Bytes()), other); e == nil {
		t.Fatal("foreign workspace accepted")
	}
	if e := VerifySnapshot(context.Background(), bytes.NewReader(append(append([]byte{}, out.Bytes()...), 1)), manifest); e == nil {
		t.Fatal("trailing payload accepted")
	}
	altered := append([]byte{}, out.Bytes()...)
	index := bytes.Index(altered, source)
	if index < 0 {
		t.Fatal("source absent")
	}
	altered[index] ^= 1
	if e := VerifySnapshot(context.Background(), bytes.NewReader(altered), manifest); e == nil {
		t.Fatal("corrupt source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := VerifySnapshot(ctx, bytes.NewReader(out.Bytes()), manifest); e == nil {
		t.Fatal("canceled readback accepted")
	}
}

func TestObjectSnapshotRejectsHiddenExtensionsAndHeaderMetadata(t *testing.T) {
	content := []byte("Synthetic private snapshot")
	sum := sha256.Sum256(content)
	manifest := SnapshotManifest{Schema: snapshotSchema, TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ObjectID: uuid.NewString(), NamespaceID: uuid.NewString(), BackendSHA256: strings.Repeat("b", 64), Purpose: "model_bundle", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(content))}
	raw, _ := json.Marshal(manifest)
	for _, format := range []tar.Format{tar.FormatPAX, tar.FormatGNU} {
		t.Run(format.String(), func(t *testing.T) {
			var output bytes.Buffer
			writer := tar.NewWriter(&output)
			for _, entry := range []struct {
				name string
				body []byte
			}{{"manifest.json", raw}, {"content.bin", content}} {
				header := &tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.body)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: format}
				if format == tar.FormatPAX {
					header.PAXRecords = map[string]string{"comment": "hidden extension"}
				}
				if e := writer.WriteHeader(header); e != nil {
					t.Fatal(e)
				}
				if _, e := writer.Write(entry.body); e != nil {
					t.Fatal(e)
				}
			}
			if e := writer.Close(); e != nil {
				t.Fatal(e)
			}
			if e := VerifySnapshot(context.Background(), bytes.NewReader(output.Bytes()), manifest); e == nil {
				t.Fatal("non-USTAR envelope accepted")
			}
		})
	}
	var output bytes.Buffer
	if e := writeSnapshot(&output, raw, bytes.NewReader(content), int64(len(content))); e != nil {
		t.Fatal(e)
	}
	canonical := output.Bytes()
	second := snapshotBlockBytes + (len(raw)+snapshotBlockBytes-1)/snapshotBlockBytes*snapshotBlockBytes
	mutations := map[string]func([]byte){
		"mode": func(b []byte) { copy(b[100:108], []byte("0000644\x00")); snapshotTestChecksum(b[:snapshotBlockBytes]) },
		"uid":  func(b []byte) { copy(b[108:116], []byte("0000001\x00")); snapshotTestChecksum(b[:snapshotBlockBytes]) },
		"gid": func(b []byte) {
			copy(b[second+116:second+124], []byte("0000001\x00"))
			snapshotTestChecksum(b[second : second+snapshotBlockBytes])
		},
		"mtime": func(b []byte) {
			copy(b[136:148], []byte("00000000001\x00"))
			snapshotTestChecksum(b[:snapshotBlockBytes])
		},
		"owner":            func(b []byte) { b[265] = 'a'; snapshotTestChecksum(b[:snapshotBlockBytes]) },
		"link":             func(b []byte) { b[157] = 'a'; snapshotTestChecksum(b[:snapshotBlockBytes]) },
		"reserved":         func(b []byte) { b[511] = 1; snapshotTestChecksum(b[:snapshotBlockBytes]) },
		"manifest_padding": func(b []byte) { b[snapshotBlockBytes+len(raw)] = 1 },
		"content_padding":  func(b []byte) { b[second+snapshotBlockBytes+len(content)] = 1 },
		"terminal_block":   func(b []byte) { b[len(b)-1] = 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := append([]byte{}, canonical...)
			mutate(altered)
			if e := VerifySnapshot(context.Background(), bytes.NewReader(altered), manifest); e == nil {
				t.Fatal("altered envelope accepted")
			}
		})
	}
	for _, remove := range []int{1, snapshotBlockBytes, 2 * snapshotBlockBytes} {
		if e := VerifySnapshot(context.Background(), bytes.NewReader(canonical[:len(canonical)-remove]), manifest); e == nil {
			t.Fatalf("truncated terminator accepted: %d", remove)
		}
	}
	if e := VerifySnapshot(context.Background(), bytes.NewReader(append(append([]byte{}, canonical...), make([]byte, snapshotBlockBytes)...)), manifest); e == nil {
		t.Fatal("extra zero block accepted")
	}
}

// Recompute a valid tar checksum independently after changing header metadata.
func snapshotTestChecksum(header []byte) {
	copy(header[148:156], []byte("        "))
	var sum int
	for _, b := range header {
		sum += int(b)
	}
	copy(header[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
}
