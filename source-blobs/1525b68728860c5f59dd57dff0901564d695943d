package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
)

const importLockDirectory = "import-locks"

// File ownership survives SQL connection cancellation. Keep the inode: unlinking
// a live lock file would let another process lock a different file description.
func withImportFileLock(ctx context.Context, directory, id string, operation func() error) error {
	if ctx == nil || !validID(id) || id == uuid.Nil.String() || operation == nil {
		return fail("MODEL_PATH_INVALID")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	root, e := openStore(directory)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = root.Mkdir(importLockDirectory, 0700); e != nil && !os.IsExist(e) {
		return fail("MODEL_IO_FAILED")
	}
	parent, e := root.Lstat(importLockDirectory)
	if e != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm() != 0700 {
		return fail("MODEL_PATH_INVALID")
	}
	owner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return fail("MODEL_PATH_INVALID")
	}
	path := filepath.Join(importLockDirectory, ".lock-"+id)
	file, e := root.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return fail("MODEL_PATH_INVALID")
	}
	defer file.Close()
	info, e := file.Stat()
	current, pathError := root.Lstat(path)
	if e != nil || pathError != nil || !info.Mode().IsRegular() || info.Size() != 0 || info.Mode().Perm() != 0600 || !os.SameFile(info, current) {
		return fail("MODEL_PATH_INVALID")
	}
	owner, ok = info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return fail("MODEL_PATH_INVALID")
	}
	if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			return fail("MODEL_IMPORT_BUSY")
		}
		return fail("MODEL_STORAGE_UNAVAILABLE")
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	current, e = root.Lstat(path)
	if e != nil || !os.SameFile(info, current) {
		return fail("MODEL_PATH_INVALID")
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return operation()
}
