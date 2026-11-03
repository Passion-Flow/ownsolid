package factory

import (
	"context"
	"io"
	"os"
)

// streamIOWindowBytes is an algorithmic writeback/advice quantum, not a
// deployment policy. Userspace buffering alone does not bound dirty file pages.
// Kernel advice remains advisory; external resource admission is still required.
const streamIOWindowBytes int64 = 4 << 20

type streamIO struct {
	prepare func(*os.File) error
	sync    func(*os.File) error
	discard func(*os.File, int64, int64) error
}

func nativeStreamIO() streamIO {
	return streamIO{prepareStreamDescriptor, func(f *os.File) error { return f.Sync() }, discardStreamRange}
}

type descriptorStream struct {
	ctx        context.Context
	file       *os.File
	operations streamIO
	position   int64
	released   int64
	failure    error
}

// Callers retain descriptor custody and the existing before/after named-file,
// inode, ctime, owner, mode and link checks. These helpers never open a pathname,
// change bytes outside the sequential write or affect a global cache policy.
func newDescriptorStream(ctx context.Context, f *os.File, operations streamIO) (descriptorStream, error) {
	s := descriptorStream{ctx: ctx, file: f, operations: operations}
	if ctx.Err() != nil {
		return s, fail("RELEASE_DEADLINE_OR_INTERRUPTION")
	}
	if f == nil || operations.prepare == nil || operations.sync == nil || operations.discard == nil {
		return s, fail("RELEASE_STREAM_DESCRIPTOR_INVALID")
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return s, fail("RELEASE_STREAM_DESCRIPTOR_INVALID")
	}
	s.position, e = f.Seek(0, io.SeekCurrent)
	if e != nil || s.position < 0 || operations.prepare(f) != nil {
		return s, fail("RELEASE_STREAM_DESCRIPTOR_POLICY_FAILED")
	}
	// Never advise away a partial page preceding this stream's initial offset.
	page := int64(os.Getpagesize())
	s.released = (s.position + page - 1) / page * page
	return s, nil
}

func (s *descriptorStream) check() error {
	if s.failure == nil && s.ctx.Err() != nil {
		s.failure = fail("RELEASE_DEADLINE_OR_INTERRUPTION")
	}
	return s.failure
}

func (s *descriptorStream) release(writing bool) error {
	if e := s.check(); e != nil {
		return e
	}
	if writing {
		// Dirty pages must be written back before Linux DONTNEED can release
		// them. This checkpoint does not replace the final file/directory fsync.
		if s.operations.sync(s.file) != nil {
			s.failure = fail("RELEASE_STREAM_WRITEBACK_FAILED")
			return s.failure
		}
		if e := s.check(); e != nil {
			return e
		}
	}
	page := int64(os.Getpagesize())
	end := s.position / page * page
	if end > s.released {
		if s.operations.discard(s.file, s.released, end-s.released) != nil {
			s.failure = fail("RELEASE_STREAM_RANGE_ADVICE_FAILED")
			return s.failure
		}
		s.released = end
	}
	return s.check()
}

type descriptorReader struct{ descriptorStream }

func newStreamReader(ctx context.Context, f *os.File) (io.Reader, error) {
	s, e := newDescriptorStream(ctx, f, nativeStreamIO())
	return &descriptorReader{s}, e
}

func (r *descriptorReader) Read(p []byte) (int, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	if int64(len(p)) > streamIOWindowBytes {
		p = p[:streamIOWindowBytes]
	}
	n, e := r.file.Read(p)
	r.position += int64(n)
	if r.position-r.released >= streamIOWindowBytes || e == io.EOF {
		if adviceError := r.release(false); adviceError != nil {
			return n, adviceError
		}
	}
	return n, e
}

type descriptorWriter struct{ descriptorStream }

func newStreamWriter(ctx context.Context, f *os.File) (*descriptorWriter, error) {
	s, e := newDescriptorStream(ctx, f, nativeStreamIO())
	return &descriptorWriter{s}, e
}

func (w *descriptorWriter) Write(p []byte) (written int, err error) {
	if e := w.check(); e != nil {
		return 0, e
	}
	for len(p) > 0 {
		remaining := streamIOWindowBytes - w.position%streamIOWindowBytes
		part := p
		if int64(len(part)) > remaining {
			part = part[:remaining]
		}
		if e := w.check(); e != nil {
			return written, e
		}
		n, e := w.file.Write(part)
		written += n
		w.position += int64(n)
		if e == nil && n != len(part) {
			e = io.ErrShortWrite
		}
		if e != nil {
			w.failure = e
			return written, e
		}
		p = p[n:]
		if w.position%streamIOWindowBytes == 0 {
			if e = w.release(true); e != nil {
				return written, e
			}
		}
	}
	return written, nil
}

func (w *descriptorWriter) finish() error { return w.release(true) }
