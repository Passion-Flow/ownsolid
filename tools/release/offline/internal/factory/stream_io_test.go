package factory

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func streamTestFile(t *testing.T) *os.File {
	t.Helper()
	f, e := os.CreateTemp(t.TempDir(), "stream-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestStreamWritebackFailureStopsBeforeNextWindow(t *testing.T) {
	for _, phase := range []string{"sync", "advice"} {
		t.Run(phase, func(t *testing.T) {
			f := streamTestFile(t)
			ops := nativeStreamIO()
			syncs, advice := 0, 0
			ops.sync = func(f *os.File) error {
				syncs++
				if phase == "sync" {
					return errors.New("synthetic writeback failure")
				}
				return f.Sync()
			}
			ops.discard = func(f *os.File, offset, length int64) error {
				advice++
				if offset != 0 || length != streamIOWindowBytes || syncs != 1 {
					t.Fatal("advice preceded writeback or exceeded processed range")
				}
				return errors.New("synthetic descriptor advice failure")
			}
			s, e := newDescriptorStream(context.Background(), f, ops)
			if e != nil {
				t.Fatal(e)
			}
			w := &descriptorWriter{s}
			n, e := w.Write(make([]byte, 2*streamIOWindowBytes+7))
			if n != int(streamIOWindowBytes) || e == nil {
				t.Fatal("failed checkpoint accepted later bytes", n, e)
			}
			if m, next := w.Write([]byte("later")); m != 0 || next != e || w.finish() != e {
				t.Fatal("failed stream resumed")
			}
			info, _ := f.Stat()
			if info.Size() != streamIOWindowBytes || syncs != 1 || phase == "sync" && advice != 0 || phase == "advice" && advice != 1 {
				t.Fatal("unexpected bytes or repeated failed effect", info.Size(), syncs, advice)
			}
		})
	}
}

func TestStreamCancelledCheckpointCannotAdvance(t *testing.T) {
	f := streamTestFile(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := nativeStreamIO()
	advice := 0
	ops.sync = func(f *os.File) error { cancel(); return f.Sync() }
	ops.discard = func(*os.File, int64, int64) error { advice++; return nil }
	s, e := newDescriptorStream(ctx, f, ops)
	if e != nil {
		t.Fatal(e)
	}
	w := &descriptorWriter{s}
	n, e := w.Write(make([]byte, 2*streamIOWindowBytes))
	if n != int(streamIOWindowBytes) || e == nil || e.Error() != "RELEASE_DEADLINE_OR_INTERRUPTION" || advice != 0 {
		t.Fatal("cancelled sync advanced into another effect", n, e, advice)
	}
	if m, next := w.Write([]byte("later")); m != 0 || next != e || w.finish() != e {
		t.Fatal("cancelled stream resumed")
	}
}

func TestStreamReaderAdvisesOnlyConsumedAlignedRangesAndPreservesBytes(t *testing.T) {
	f := streamTestFile(t)
	raw := bytes.Repeat([]byte("0123456789abcdef"), int(streamIOWindowBytes/8))
	if _, e := f.Write(raw); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Seek(1, io.SeekStart); e != nil {
		t.Fatal(e)
	}
	before, _ := f.Stat()
	ops := nativeStreamIO()
	previous := int64(os.Getpagesize())
	calls := 0
	ops.discard = func(file *os.File, offset, length int64) error {
		position, _ := file.Seek(0, io.SeekCurrent)
		page := int64(os.Getpagesize())
		if offset != previous || length <= 0 || offset%page != 0 || length%page != 0 || offset+length > position {
			t.Fatal("unread, partial, repeated or whole-file advice", offset, length, position)
		}
		previous = offset + length
		calls++
		return discardStreamRange(file, offset, length)
	}
	s, e := newDescriptorStream(context.Background(), f, ops)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := io.ReadAll(&descriptorReader{s})
	after, _ := f.Stat()
	if e != nil || !bytes.Equal(actual, raw[1:]) || calls < 1 || !sameRegularSnapshot(before, after, int64(len(raw)), true, 1) {
		t.Fatal("stream bytes/custody changed", e, calls)
	}
}

func TestStreamReaderAdviceFailureAndClosedDescriptorRefuse(t *testing.T) {
	f := streamTestFile(t)
	if _, e := f.Write(make([]byte, streamIOWindowBytes+1)); e != nil {
		t.Fatal(e)
	}
	f.Seek(0, io.SeekStart)
	ops := nativeStreamIO()
	ops.discard = func(*os.File, int64, int64) error { return errors.New("synthetic advice failure") }
	s, e := newDescriptorStream(context.Background(), f, ops)
	if e != nil {
		t.Fatal(e)
	}
	r := &descriptorReader{s}
	n, e := r.Read(make([]byte, 2*streamIOWindowBytes))
	if n != int(streamIOWindowBytes) || e == nil {
		t.Fatal("read bypassed checkpoint", n, e)
	}
	if n, again := r.Read(make([]byte, 1)); n != 0 || again != e {
		t.Fatal("failed reader resumed")
	}
	f.Close()
	if _, e = newStreamReader(context.Background(), f); e == nil {
		t.Fatal("closed descriptor admitted")
	}
	if _, e = newStreamWriter(context.Background(), nil); e == nil {
		t.Fatal("missing descriptor admitted")
	}
}

func TestStreamNativeArchiveMultipleOutputWindowsAndTamper(t *testing.T) {
	c, m, _ := setup(t)
	c.MaximumAssetBytes, c.MaximumTotalBytes = 32<<20, 64<<20
	block := make([]byte, 1<<20)
	if _, e := rand.Read(block); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(c.ArtifactRoot, "stream-payload")
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.New()
	for i := 0; i < 9; i++ {
		if _, e = io.MultiWriter(f, h).Write(block); e != nil {
			t.Fatal(e)
		}
	}
	if f.Sync() != nil || f.Close() != nil {
		t.Fatal("source flush")
	}
	m.Assets = append(m.Assets, wire.Asset{ID: "stream-payload", Path: "stream-payload", Kind: "model", Size: 9 << 20, SHA256: hex.EncodeToString(h.Sum(nil)), Licenses: []string{"license"}})
	put(t, c.ManifestFile, Encode(m))
	r, e := Build(context.Background(), c)
	if e != nil || r.Status != "passed_signed_archive_full_readback" {
		t.Fatal(r, e)
	}
	info, _ := os.Stat(c.OutputFile)
	if info.Size() <= 2*streamIOWindowBytes {
		t.Fatal("fixture did not cross multiple compressed output windows", info.Size())
	}
	checked, e := Inspect(context.Background(), c, c.OutputFile)
	if e != nil || checked.ArchiveSHA256 != r.ArchiveSHA256 || checked.ManifestSHA256 != r.ManifestSHA256 {
		t.Fatal("independent final readback", e)
	}
	q, e := os.OpenFile(c.OutputFile, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = q.WriteAt([]byte{0}, info.Size()-1); e != nil || q.Truncate(info.Size()-1) != nil || q.Sync() != nil || q.Close() != nil {
		t.Fatal("explicit synthetic truncation", e)
	}
	if _, e = Inspect(context.Background(), c, c.OutputFile); e == nil {
		t.Fatal("truncated streamed archive accepted")
	}
}
