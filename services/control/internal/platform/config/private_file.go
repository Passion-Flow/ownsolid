package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

func privateFileOwned(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky|os.ModeSymlink) != 0 {
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

// Darwin and Linux expose change time under different stat field names. A
// missing native fact is a refusal, not permission to drop this integrity guard.
func samePrivateChangeTime(before, after os.FileInfo) bool {
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	if !aok || !bok {
		return false
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		x, y := reflect.ValueOf(a).Elem().FieldByName(name), reflect.ValueOf(b).Elem().FieldByName(name)
		if x.IsValid() && y.IsValid() && x.CanInterface() && y.CanInterface() {
			return reflect.DeepEqual(x.Interface(), y.Interface())
		}
	}
	return false
}

func unchangedPrivateFile(before, after os.FileInfo) bool {
	return privateFileOwned(before, false) && privateFileOwned(after, false) &&
		os.SameFile(before, after) && before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime()) && samePrivateChangeTime(before, after)
}

// Keep the literal final dot: the checked parent must remain a directory during
// kernel path resolution. Join/Clean would remove it and permit a replacement
// FIFO to block OpenRoot before its later directory-type check.
func openPrivateDirectory(parent string) (*os.Root, error) {
	return os.OpenRoot(parent + string(filepath.Separator) + ".")
}

// Pin a private directory and ordinary file; no path error or secret content escapes this boundary.
func readPrivateFile(path string, maximum int64, code string) ([]byte, error) {
	bad := errors.New(code)
	if maximum < 1 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, bad
	}
	parent := filepath.Dir(path)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return nil, bad
	}
	beforeDirectory, e := os.Lstat(parent)
	if e != nil || !privateFileOwned(beforeDirectory, true) {
		return nil, bad
	}
	root, e := openPrivateDirectory(parent)
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
	if e != nil || !privateFileOwned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !unchangedPrivateFile(before, opened) {
		return nil, bad
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximum+1))
	if e != nil || int64(len(raw)) != before.Size() {
		return nil, bad
	}
	after, e := file.Stat()
	if e != nil || !unchangedPrivateFile(before, after) {
		return nil, bad
	}
	current, e := root.Lstat(name)
	if e != nil || !unchangedPrivateFile(before, current) {
		return nil, bad
	}
	currentDirectory, e := os.Lstat(parent)
	if e != nil || !os.SameFile(beforeDirectory, currentDirectory) || !privateFileOwned(currentDirectory, true) {
		return nil, bad
	}
	return raw, nil
}
