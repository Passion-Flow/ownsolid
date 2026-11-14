package mysqlstore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const MaximumConfigurationBytes int64 = 64 << 10
const MaximumPasswordBytes int64 = 4096
const MaximumCABytes int64 = 1 << 20

func owned(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.Mode() == os.ModeDir|0700
	}
	return info.Mode() == 0600 && identity.Nlink == 1
}

// Pin an owned private directory/file and verify both identities after bounded IO.
func privateInput(path string, maximum int64) ([]byte, error) {
	bad := errors.New("MYSQL_PRIVATE_INPUT_INVALID")
	if !privatePath(path) {
		return nil, bad
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, bad
	}
	directory, e := os.Lstat(parent)
	if e != nil || !owned(directory, true) {
		return nil, bad
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, bad
	}
	defer root.Close()
	openedDirectory, e := root.Stat(".")
	if e != nil || !os.SameFile(directory, openedDirectory) {
		return nil, bad
	}
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || !owned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) || !owned(opened, false) {
		return nil, bad
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximum+1))
	if e != nil || int64(len(raw)) != before.Size() {
		return nil, bad
	}
	after, e := file.Stat()
	current, ce := root.Lstat(name)
	currentDirectory, de := os.Lstat(parent)
	if e != nil || ce != nil || de != nil || !owned(after, false) || !owned(current, false) || !owned(currentDirectory, true) || !os.SameFile(before, after) || !os.SameFile(before, current) || !os.SameFile(directory, currentDirectory) || after.Size() != before.Size() || current.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return nil, bad
	}
	return raw, nil
}
