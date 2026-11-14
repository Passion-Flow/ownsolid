// Package snapshotio bounds unsynchronized snapshot writes without owning paths or state.
package snapshotio

import (
	"errors"
	"io"
)

// MaximumUnsyncedBytes is one existing large-artifact transfer chunk. It is an
// internal writeback invariant, not a file-size limit or a deployment setting.
const MaximumUnsyncedBytes = 8 * 1024 * 1024

var ErrInvalidInput = errors.New("SNAPSHOT_COPY_INPUT_INVALID")

type File interface {
	io.Writer
	Sync() error
}

// Copy retains io.Copy's count/error and file-before-checksum ordering, adding a
// Sync barrier at every complete artifact chunk. The caller still owns the final
// file Sync, permissions, Close, directory Sync and any publication decision.
// A failure retains partial output; Copy never retries, removes or publishes it.
func Copy(destination File, checksum io.Writer, source io.Reader) (int64, error) {
	if destination == nil || checksum == nil || source == nil {
		return 0, ErrInvalidInput
	}
	return io.Copy(io.MultiWriter(&writer{file: destination}, checksum), source)
}

type writer struct {
	file    File
	pending int
}

func (w *writer) Write(p []byte) (total int, err error) {
	for len(p) > 0 {
		part := p[:min(len(p), MaximumUnsyncedBytes-w.pending)]
		n, err := w.file.Write(part)
		if n < 0 || n > len(part) {
			return total, io.ErrShortWrite
		}
		total += n
		w.pending += n
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n != len(part) {
			return total, io.ErrShortWrite
		}
		if w.pending == MaximumUnsyncedBytes {
			if err := w.file.Sync(); err != nil {
				return total, err
			}
			w.pending = 0
		}
	}
	return total, nil
}
