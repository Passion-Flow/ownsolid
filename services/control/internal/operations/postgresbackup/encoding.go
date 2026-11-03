package postgresbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func canonical(v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, errors.New("POSTGRES_BACKUP_DOCUMENT_INVALID")
	}
	return append(raw, '\n'), nil
}
func writeDocument(root *os.Root, name string, v any, maximum int64) error {
	raw, e := canonical(v)
	if e != nil {
		return e
	}
	if int64(len(raw)) > maximum {
		return errors.New("POSTGRES_BACKUP_DOCUMENT_LIMIT_REACHED")
	}
	return writeNew(root, name, raw)
}

func readPrivateBytes(path string, maximum int64) ([]byte, error) {
	if maximum < 1 || maximum > 16<<20 {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_INVALID")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_INVALID")
	}
	root, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || !owned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_INVALID")
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_INVALID")
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_CHANGED")
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	after, ae := f.Stat()
	named, ne := root.Lstat(name)
	if e != nil || ae != nil || ne != nil || int64(len(raw)) != before.Size() {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_CHANGED")
	}
	a, ae := observeTree(before)
	b, be := observeTree(after)
	c, ce := observeTree(named)
	if ae != nil || be != nil || ce != nil || a != b || a != c {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_CHANGED")
	}
	parent, pe := os.Lstat(filepath.Dir(path))
	openedParent, oe := root.Stat(".")
	resolved, re := filepath.EvalSymlinks(filepath.Dir(path))
	if pe != nil || oe != nil || re != nil || resolved != filepath.Dir(path) || !owned(parent, true) || !os.SameFile(parent, openedParent) {
		return nil, errors.New("POSTGRES_BACKUP_PRIVATE_FILE_CHANGED")
	}
	return raw, nil
}

func hashStream(ctx context.Context, output io.Writer, input io.Reader, maximum int64) (string, int64, error) {
	if ctx == nil || maximum < 0 {
		return "", 0, errors.New("POSTGRES_BACKUP_STREAM_LIMIT_INVALID")
	}
	h := sha256.New()
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return "", total, e
		}
		n, e := input.Read(buffer)
		if n > 0 {
			if int64(n) > maximum-total {
				return "", total, errors.New("POSTGRES_BACKUP_STREAM_LIMIT_REACHED")
			}
			if output != nil {
				written, we := output.Write(buffer[:n])
				if we != nil || written != n {
					return "", total, errors.New("POSTGRES_BACKUP_STREAM_WRITE_FAILED")
				}
			}
			h.Write(buffer[:n])
			total += int64(n)
		}
		if e == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), total, nil
		}
		if e != nil {
			return "", total, errors.New("POSTGRES_BACKUP_STREAM_READ_FAILED")
		}
	}
}
