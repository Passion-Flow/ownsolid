package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type chunk struct {
	offset, size int64
	digest       string
}
type chunkReader struct {
	ctx       context.Context
	root      *os.Root
	id        string
	chunks    []chunk
	index     int
	file      *os.File
	remaining int64
	hash      hash.Hash
}

func (r *chunkReader) Close() error {
	if r.file != nil {
		e := r.file.Close()
		r.file = nil
		return e
	}
	return nil
}
func (r *chunkReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.file == nil {
		if r.index == len(r.chunks) {
			return 0, io.EOF
		}
		value := r.chunks[r.index]
		file, err := r.openChunk(value)
		if err != nil {
			return 0, err
		}
		r.file = file
		r.remaining = value.size
		r.hash = sha256.New()
	}
	limit := min(int64(len(p)), r.remaining)
	n, e := r.file.Read(p[:limit])
	r.remaining -= int64(n)
	r.hash.Write(p[:n])
	if r.remaining == 0 {
		if hex.EncodeToString(r.hash.Sum(nil)) != r.chunks[r.index].digest {
			r.Close()
			return n, fail("MODEL_CHUNK_INVALID")
		}
		if closeError := r.Close(); closeError != nil {
			return n, closeError
		}
		r.index++
		return n, nil
	}
	if e == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, e
}

// ReadAt visits small archive regions without moving the authenticated stream
// used by Read. The repository has already checked the contiguous chunk layout.
func (r *chunkReader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, fail("MODEL_CHUNK_INVALID")
	}
	total := 0
	for len(p) > 0 {
		if err := r.ctx.Err(); err != nil {
			return total, err
		}
		index := offset / ChunkBytes
		if index >= int64(len(r.chunks)) {
			return total, io.EOF
		}
		value := r.chunks[index]
		position := offset - value.offset
		if position >= value.size {
			return total, io.EOF
		}
		file, err := r.openChunk(value)
		if err != nil {
			return total, err
		}
		n, err := file.ReadAt(p[:min(int64(len(p)), value.size-position)], position)
		closeErr := file.Close()
		total += n
		if err != nil {
			return total, err
		}
		if closeErr != nil {
			return total, closeErr
		}
		p = p[n:]
		offset += int64(n)
	}
	return total, nil
}

func (r *chunkReader) openChunk(value chunk) (*os.File, error) {
	path := chunkPath(r.id, value.offset)
	info, owned := ownedRegular(r.root, path)
	if !owned || info.Size() != value.size || info.Mode().Perm()&0077 != 0 {
		return nil, fail("MODEL_CHUNK_INVALID")
	}
	file, err := r.root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fail("MODEL_CHUNK_INVALID")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != value.size || opened.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, fail("MODEL_CHUNK_INVALID")
	}
	owner, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		file.Close()
		return nil, fail("MODEL_CHUNK_INVALID")
	}
	return file, nil
}

// ProcessNext reconciles one durably queued import under a database session advisory lock.
// A process crash releases the lock; a fresh process verifies existing immutable publication.
func (s *Service) ProcessNext(ctx context.Context) (bool, error) { return s.processNext(ctx) }
func safeImportError(e error) string {
	known := map[string]bool{"MODEL_RECOVERY_REQUIRED": true, "MODEL_IMPORT_STATE_CONFLICT": true, "MODEL_PATH_INVALID": true, "MODEL_FILE_SET_INVALID": true, "MODEL_FILE_INVALID": true, "MODEL_FILE_CHANGED": true, "MODEL_DIGEST_MISMATCH": true, "MODEL_CANCELLED": true, "MODEL_CONFIGURATION_INVALID": true, "MODEL_TOKENIZER_INVALID": true, "MODEL_SAFETENSORS_INVALID": true, "MODEL_STAGE_INVALID": true, "MODEL_ARCHIVE_INVALID": true, "MODEL_IO_FAILED": true, "MODEL_CLEANUP_CONFLICT": true, "MODEL_CHUNK_INVALID": true, "MODEL_STORAGE_UNAVAILABLE": true, "MODEL_PUBLICATION_CONFLICT": true, "MODEL_ACTOR_UNAVAILABLE": true, "MODEL_IMPORT_INCOMPLETE": true}
	if known[e.Error()] {
		return e.Error()
	}
	return "MODEL_IMPORT_FAILED"
}

// Run processes imports and reconciles eligible staging cleanup.
// Configure the service before starting it. Run returns after cancellation and
// completion of the current bounded pass. The caller must join Run before
// closing the repository or model store. An active uploader retains its lock
// and request context independently of this worker's cancellation.
func (s *Service) Run(ctx context.Context) {
	var cleanupCursor, expiryCursor string
	for ctx.Err() == nil {
		progress, e := s.ProcessNext(ctx)
		if e != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "model_import_worker_failed", "error_code", e.Error())
		}
		cleanedThrough, cleanupError := s.cleanupNextImport(ctx, cleanupCursor)
		if cleanupError != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "model_import_cleanup_failed", "import_id", cleanedThrough, "error_code", cleanupError.Error())
		}
		cleanupCursor = cleanedThrough
		expiredThrough, expiryError := s.expireNextImport(ctx, expiryCursor)
		if expiryError != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "model_import_expiry_failed", "import_id", expiredThrough, "error_code", expiryError.Error())
		}
		expiryCursor = expiredThrough
		if progress && e == nil {
			continue
		}
		timer := time.NewTimer(config.Seconds(s.Identity.Config.Policy().Models.ImportPollSeconds))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
