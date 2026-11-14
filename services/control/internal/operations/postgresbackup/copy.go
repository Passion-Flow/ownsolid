package postgresbackup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

func newDirectory(path string) (*os.Root, error) {
	parent, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer parent.Close()
	if parent.Mkdir(filepath.Base(path), 0700) != nil {
		return nil, errors.New("POSTGRES_BACKUP_DESTINATION_EXISTS")
	}
	if e = syncRoot(parent); e != nil {
		return nil, e
	}
	return openPrivateRoot(path)
}

func copyTree(ctx context.Context, source, destination string, maximumBytes int64, maximumFiles int, maximumMetadata, minimum int64) (TreeReadback, error) {
	before, e := TreeDigest(ctx, source, maximumBytes, maximumFiles, maximumMetadata)
	if e != nil {
		return TreeReadback{}, e
	}
	input, e := openPrivateRoot(source)
	if e != nil {
		return TreeReadback{}, e
	}
	defer input.Close()
	output, e := newDirectory(destination)
	if e != nil {
		return TreeReadback{}, e
	}
	defer output.Close()
	type directoryMode struct {
		name string
		mode os.FileMode
	}
	directories := []directoryMode{{".", 0700}}
	var copiedBytes int64
	copiedFiles, entries := 0, 0
	metadataRemaining := maximumMetadata
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if ctx.Err() != nil {
			return errors.New("POSTGRES_BACKUP_COPY_INTERRUPTED")
		}
		entries++
		metadataRemaining -= int64(len(name) + 128)
		if entries > maximumFiles+int(maximumMetadata/128) || metadataRemaining < 0 || depth > maximumTreeDepth {
			return errors.New("POSTGRES_BACKUP_TREE_LIMIT_REACHED")
		}
		info, e := input.Lstat(name)
		if e != nil {
			return errors.New("POSTGRES_BACKUP_TREE_CHANGED")
		}
		stamp, e := observeTree(info)
		if e != nil {
			return e
		}
		if info.IsDir() {
			if name != "." {
				if output.Mkdir(name, 0700) != nil {
					return errors.New("POSTGRES_BACKUP_EXCLUSIVE_CREATE_FAILED")
				}
				directories = append(directories, directoryMode{name, info.Mode().Perm()})
			}
			directory, e := input.Open(name)
			if e != nil {
				return errors.New("POSTGRES_BACKUP_TREE_CHANGED")
			}
			defer directory.Close()
			opened, e := directory.Stat()
			if e != nil || !os.SameFile(info, opened) {
				return errors.New("POSTGRES_BACKUP_TREE_CHANGED")
			}
			for {
				children, readError := directory.ReadDir(256)
				for _, child := range children {
					part := child.Name()
					if part == "" || part == "." || part == ".." || strings.Contains(part, "/") || !utf8.ValidString(part) || strings.ContainsFunc(part, unicode.IsControl) {
						return errors.New("POSTGRES_BACKUP_TREE_PATH_INVALID")
					}
					if e := walk(filepath.Join(name, part), depth+1); e != nil {
						return e
					}
				}
				if readError == io.EOF {
					break
				}
				if readError != nil {
					return errors.New("POSTGRES_BACKUP_TREE_READ_FAILED")
				}
			}
			return nil
		}
		copiedFiles++
		if copiedFiles > maximumFiles || info.Size() < 0 || info.Size() > maximumBytes-copiedBytes {
			return errors.New("POSTGRES_BACKUP_TREE_LIMIT_REACHED")
		}
		copiedBytes += info.Size()
		in, e := input.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return errors.New("POSTGRES_BACKUP_TREE_CHANGED")
		}
		defer in.Close()
		opened, e := in.Stat()
		if e != nil || !os.SameFile(info, opened) {
			return errors.New("POSTGRES_BACKUP_TREE_CHANGED")
		}
		out, e := output.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return errors.New("POSTGRES_BACKUP_EXCLUSIVE_CREATE_FAILED")
		}
		writer := &boundedOutput{ctx: ctx, file: out, maximum: info.Size(), minimum: minimum}
		_, n, e := hashStream(ctx, writer, in, info.Size())
		after, ae := in.Stat()
		current, ce := input.Lstat(name)
		if e == nil {
			e = out.Chmod(info.Mode().Perm())
		}
		if e == nil {
			e = out.Sync()
		}
		closeError := out.Close()
		if e != nil || ae != nil || ce != nil || closeError != nil || n != info.Size() {
			return errors.New("POSTGRES_BACKUP_COPY_FAILED")
		}
		a, ae := observeTree(after)
		b, be := observeTree(current)
		if ae != nil || be != nil || a != stamp || b != stamp {
			return errors.New("POSTGRES_BACKUP_TREE_CHANGED")
		}
		return nil
	}
	e = walk(".", 0)
	if e != nil {
		return TreeReadback{}, e
	}
	for i := len(directories) - 1; i >= 0; i-- {
		f, e := output.Open(directories[i].name)
		if e != nil {
			return TreeReadback{}, errors.New("POSTGRES_BACKUP_DIRECTORY_SYNC_FAILED")
		}
		// Assemble through private writable directories, then seal descendants
		// bottom-up to their original owner-defined modes before readback.
		e = f.Chmod(directories[i].mode)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil || ce != nil {
			return TreeReadback{}, errors.New("POSTGRES_BACKUP_DIRECTORY_SYNC_FAILED")
		}
	}
	after, e := TreeDigest(ctx, source, maximumBytes, maximumFiles, maximumMetadata)
	if e != nil || after != before {
		return TreeReadback{}, errors.New("POSTGRES_BACKUP_TREE_CHANGED")
	}
	copied, e := TreeDigest(ctx, destination, maximumBytes, maximumFiles, maximumMetadata)
	if e != nil || copied != before {
		return TreeReadback{}, errors.New("POSTGRES_BACKUP_COPY_READBACK_FAILED")
	}
	return copied, nil
}

type boundedOutput struct {
	ctx                     context.Context
	file                    *os.File
	maximum, minimum, count int64
}

func (w *boundedOutput) Write(b []byte) (int, error) {
	if w.ctx == nil || w.ctx.Err() != nil {
		return 0, errors.New("POSTGRES_BACKUP_INTERRUPTED")
	}
	if int64(len(b)) > w.maximum-w.count {
		return 0, errors.New("POSTGRES_BACKUP_STREAM_LIMIT_REACHED")
	}
	if e := requireSpaceFile(w.file, int64(len(b)), w.minimum); e != nil {
		return 0, e
	}
	n, e := w.file.Write(b)
	w.count += int64(n)
	return n, e
}
