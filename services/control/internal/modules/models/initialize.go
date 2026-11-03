package models

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// InitializeStore changes ownership only for an explicit existing empty directory.
// It never creates a missing mount/root or adopts populated storage.
func InitializeStore(path string, uid, gid int) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || uid < 0 || gid < 0 || uid > 2147483647 || gid > 2147483647 {
		return fail("MODEL_PATH_INVALID")
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return fail("MODEL_PATH_INVALID")
	}
	before, e := os.Lstat(path)
	if e != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return fail("MODEL_PATH_INVALID")
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() && !(os.Geteuid() == 0 && int(owner.Uid) == uid) {
		return fail("MODEL_PATH_INVALID")
	}
	directory, e := os.Open(path)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer directory.Close()
	current, e := directory.Stat()
	if e != nil || !os.SameFile(before, current) {
		return fail("MODEL_PATH_INVALID")
	}
	entries, e := directory.ReadDir(1)
	if len(entries) != 0 {
		return fail("MODEL_STORE_NOT_EMPTY")
	}
	if e != nil && e != io.EOF {
		return fail("MODEL_IO_FAILED")
	}
	if os.Geteuid() != 0 && (uid != os.Geteuid() || gid != os.Getegid()) {
		return fail("MODEL_PATH_INVALID")
	}
	if e = directory.Chmod(0700); e != nil {
		return fail("MODEL_IO_FAILED")
	}
	if int(owner.Uid) != uid || int(owner.Gid) != gid {
		if e = directory.Chown(uid, gid); e != nil {
			return fail("MODEL_IO_FAILED")
		}
	}
	if directory.Sync() != nil {
		return fail("MODEL_IO_FAILED")
	}
	return nil
}
