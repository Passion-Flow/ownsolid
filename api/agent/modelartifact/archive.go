package modelartifact

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const tarBlockBytes int64 = 512
const tarTerminalBlocks int64 = 2

// MinimumArchiveBytes includes every approved USTAR header, padding and trailer.
// It is a protocol bound, independent of configurable deployment admission caps.
func MinimumArchiveBytes() int64 {
	minimum := tarTerminalBlocks * tarBlockBytes
	for _, file := range wire.ApprovedModel().Files {
		minimum += tarBlockBytes + ((file.SizeBytes+tarBlockBytes-1)/tarBlockBytes)*tarBlockBytes
	}
	return minimum
}

type countedReader struct {
	source io.Reader
	bytes  int64
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, e := r.source.Read(p)
	r.bytes += int64(n)
	return n, e
}

// ExtractArchive writes only new approved files in the caller's newly owned empty stage.
// The caller retains the stage after a failure for its explicit import recovery policy.
func ExtractArchive(ctx context.Context, source io.Reader, directory string) error {
	entries, e := os.ReadDir(directory)
	info, statError := os.Lstat(directory)
	if e != nil || statError != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || len(entries) != 0 {
		return fail("MODEL_STAGE_INVALID")
	}
	counted := &countedReader{source: contextReader{ctx, source}}
	archive := tar.NewReader(counted)
	seen := map[string]bool{}
	for {
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			if ctx.Err() != nil {
				return fail("MODEL_CANCELLED")
			}
			return fail("MODEL_ARCHIVE_INVALID")
		}
		expected, ok := wire.ModelFileForName(header.Name)
		if !ok || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Linkname != "" || len(header.PAXRecords) != 0 || header.Format != tar.FormatUSTAR || header.Mode&0111 != 0 || header.Size != expected.SizeBytes {
			return fail("MODEL_ARCHIVE_INVALID")
		}
		seen[header.Name] = true
		file, e := os.OpenFile(filepath.Join(directory, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return fail("MODEL_IO_FAILED")
		}
		hash := sha256.New()
		n, copyError := io.Copy(io.MultiWriter(file, hash), contextReader{ctx, archive})
		if copyError == nil {
			copyError = file.Sync()
		}
		if copyError == nil {
			copyError = file.Chmod(0444)
		}
		closeError := file.Close()
		if copyError != nil || closeError != nil || n != expected.SizeBytes {
			if ctx.Err() != nil {
				return fail("MODEL_CANCELLED")
			}
			return fail("MODEL_ARCHIVE_INVALID")
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fail("MODEL_DIGEST_MISMATCH")
		}
	}
	if len(seen) != len(wire.ApprovedModel().Files) || counted.bytes < MinimumArchiveBytes() {
		return fail("MODEL_ARCHIVE_INVALID")
	}
	trailing, e := io.ReadAll(io.LimitReader(counted, 10241))
	if e != nil || len(trailing) > 10240 {
		return fail("MODEL_ARCHIVE_INVALID")
	}
	for _, value := range trailing {
		if value != 0 {
			return fail("MODEL_ARCHIVE_INVALID")
		}
	}
	if e = VerifyDirectory(ctx, directory); e != nil {
		return e
	}
	dir, e := os.Open(directory)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return fail("MODEL_IO_FAILED")
	}
	return nil
}
