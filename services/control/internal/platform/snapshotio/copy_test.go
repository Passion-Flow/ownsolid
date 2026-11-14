package snapshotio

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type observedFile struct {
	*os.File
	barriers []int64
	failSync int
	writeErr error
	short    bool
}

var errInjected = errors.New("injected snapshot IO failure")

func (f *observedFile) Write(p []byte) (int, error) {
	if f.short || f.writeErr != nil {
		n, err := f.File.Write(p[:min(3, len(p))])
		if err != nil {
			return n, err
		}
		return n, f.writeErr
	}
	return f.File.Write(p)
}

func (f *observedFile) Sync() error {
	position, err := f.File.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	f.barriers = append(f.barriers, position)
	if f.failSync == len(f.barriers) {
		return errInjected
	}
	return f.File.Sync()
}

func newFile(t *testing.T) *observedFile {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "snapshot"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return &observedFile{File: f}
}

func checkBytes(t *testing.T, f *observedFile, data []byte) {
	t.Helper()
	actual, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("snapshot bytes differ", err)
	}
}

func TestLargeWriteRetainsBytesHashAndChunkBarriers(t *testing.T) {
	data := bytes.Repeat([]byte{0x51}, 3*MaximumUnsyncedBytes+29)
	f := newFile(t)
	hash := sha256.New()
	n, err := Copy(f, hash, bytes.NewReader(data)) // Reader.WriteTo submits one large write.
	if err != nil || n != int64(len(data)) || !bytes.Equal(hash.Sum(nil), sum(data)) {
		t.Fatal("large copy/count/hash", n, err)
	}
	if !reflect.DeepEqual(f.barriers, []int64{MaximumUnsyncedBytes, 2 * MaximumUnsyncedBytes, 3 * MaximumUnsyncedBytes}) {
		t.Fatal("large write crossed an unsynchronized chunk", f.barriers)
	}
	checkBytes(t, f, data)
}

type fragmentedReader struct{ reader *bytes.Reader }

func (r fragmentedReader) Read(p []byte) (int, error) {
	return r.reader.Read(p[:min(len(p), 7919)])
}

func TestFragmentedReadsKeepTheSameDurabilityBound(t *testing.T) {
	data := bytes.Repeat([]byte{0xa3}, 2*MaximumUnsyncedBytes+41)
	f := newFile(t)
	n, err := Copy(f, io.Discard, fragmentedReader{bytes.NewReader(data)})
	if err != nil || n != int64(len(data)) || !reflect.DeepEqual(f.barriers, []int64{MaximumUnsyncedBytes, 2 * MaximumUnsyncedBytes}) {
		t.Fatal("fragmented copy", n, err, f.barriers)
	}
	checkBytes(t, f, data)
}

func TestBarrierFailureStopsWithoutRetryOrFalseCompletion(t *testing.T) {
	f := newFile(t)
	f.failSync = 2
	data := bytes.Repeat([]byte{0x37}, 3*MaximumUnsyncedBytes)
	n, err := Copy(f, io.Discard, bytes.NewReader(data))
	if !errors.Is(err, errInjected) || n != 2*MaximumUnsyncedBytes || len(f.barriers) != 2 {
		t.Fatal("barrier error/count/retry", n, err, f.barriers)
	}
	checkBytes(t, f, data[:2*MaximumUnsyncedBytes])
}

func TestPartialWritesPreserveErrorsAndNeverSpin(t *testing.T) {
	for _, injected := range []error{nil, errInjected} {
		t.Run(map[bool]string{true: "write-error", false: "short-write"}[injected != nil], func(t *testing.T) {
			f := newFile(t)
			f.short, f.writeErr = true, injected
			n, err := Copy(f, io.Discard, bytes.NewReader([]byte("partial snapshot")))
			wanted := injected
			if wanted == nil {
				wanted = io.ErrShortWrite
			}
			if n != 3 || !errors.Is(err, wanted) || len(f.barriers) != 0 {
				t.Fatal("partial write result", n, err, f.barriers)
			}
			checkBytes(t, f, []byte("par"))
		})
	}
}

type failingReader struct{ reader *bytes.Reader }

func (r failingReader) Read(p []byte) (int, error) {
	if r.reader.Len() == 0 {
		return 0, errInjected
	}
	return r.reader.Read(p)
}

func TestSourceFailureRetainsOnlyItsObservedPrefix(t *testing.T) {
	data := bytes.Repeat([]byte{0x19}, MaximumUnsyncedBytes)
	f := newFile(t)
	n, err := Copy(f, io.Discard, failingReader{bytes.NewReader(data)})
	if n != MaximumUnsyncedBytes || !errors.Is(err, errInjected) || !reflect.DeepEqual(f.barriers, []int64{MaximumUnsyncedBytes}) {
		t.Fatal("source failure/count/barrier", n, err, f.barriers)
	}
	checkBytes(t, f, data)
}

func TestInvalidDependenciesFailBeforeWriting(t *testing.T) {
	f := newFile(t)
	for _, input := range []struct {
		file File
		hash io.Writer
		src  io.Reader
	}{{nil, io.Discard, bytes.NewReader(nil)}, {f, nil, bytes.NewReader(nil)}, {f, io.Discard, nil}} {
		if n, err := Copy(input.file, input.hash, input.src); n != 0 || !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid copy input", n, err)
		}
	}
	checkBytes(t, f, nil)
}

func TestResidualAndFinalMetadataSyncRemainCallerOwned(t *testing.T) {
	f := newFile(t)
	data := []byte("retained residual")
	n, err := Copy(f, io.Discard, bytes.NewReader(data))
	if err != nil || n != int64(len(data)) || len(f.barriers) != 0 {
		t.Fatal("unexpected residual/final barrier", n, err, f.barriers)
	}
	if err := f.Chmod(0444); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil || !reflect.DeepEqual(f.barriers, []int64{int64(len(data))}) {
		t.Fatal("caller final synchronization", err, f.barriers)
	}
	checkBytes(t, f, data)
}

func sum(data []byte) []byte {
	value := sha256.Sum256(data)
	return value[:]
}
