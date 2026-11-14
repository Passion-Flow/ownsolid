package sso

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

var errPrivateFileAbsent = errors.New("SSO_PRIVATE_FILE_ABSENT")

func privateOwned(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && owner.Nlink == 1
}

// Pin a private directory and ordinary file; no path error or secret content escapes this boundary.
func readPrivateFile(path string, maximum int64) ([]byte, error) {
	bad := errors.New("SSO_PRIVATE_FILE_INVALID")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, bad
	}
	parent := filepath.Dir(path)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return nil, bad
	}
	beforeDirectory, e := os.Lstat(parent)
	if e != nil || !privateOwned(beforeDirectory, true) {
		return nil, bad
	}
	root, e := os.OpenRoot(parent + string(filepath.Separator) + ".")
	if e != nil {
		return nil, bad
	}
	defer root.Close()
	openedDirectory, e := root.Stat(".")
	if e != nil || !os.SameFile(beforeDirectory, openedDirectory) {
		return nil, bad
	}
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		currentDirectory, statErr := os.Lstat(parent)
		if statErr == nil && os.SameFile(beforeDirectory, currentDirectory) && privateOwned(currentDirectory, true) {
			return nil, errPrivateFileAbsent
		}
	}
	if e != nil || !privateOwned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) || !privateOwned(opened, false) {
		return nil, bad
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximum+1))
	if e != nil || int64(len(raw)) != before.Size() {
		return nil, bad
	}
	after, e := file.Stat()
	if e != nil || !privateOwned(after, false) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, bad
	}
	current, e := root.Lstat(name)
	if e != nil || !os.SameFile(before, current) || !privateOwned(current, false) || current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) {
		return nil, bad
	}
	currentDirectory, e := os.Lstat(parent)
	if e != nil || !os.SameFile(beforeDirectory, currentDirectory) || !privateOwned(currentDirectory, true) {
		return nil, bad
	}
	return raw, nil
}
