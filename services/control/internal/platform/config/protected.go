package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The protected operator configuration document bound is a wire invariant.
const MaximumProtectedConfigurationBytes = 256 << 10

// LoadProtected reads one immutable private operator snapshot. Existing ordinary
// Load behavior is preserved for its historical callers.
func LoadProtected(path string) (Config, error) {
	failure := func() (Config, error) { return Config{}, errors.New("CONFIG_PRIVATE_SNAPSHOT_REQUIRED") }
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return failure()
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return failure()
	}
	root, e := openPrivateDirectory(parent)
	if e != nil {
		return failure()
	}
	defer root.Close()
	owned := func(i os.FileInfo, single bool) bool {
		s, ok := i.Sys().(*syscall.Stat_t)
		return ok && uint64(s.Uid) == uint64(os.Geteuid()) && (!single || s.Nlink == 1)
	}
	directory, e := root.Stat(".")
	if e != nil || directory.Mode() != os.ModeDir|0700 || !owned(directory, false) {
		return failure()
	}
	file, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return failure()
	}
	defer file.Close()
	before, e := file.Stat()
	if e != nil || before.Mode() != 0600 || !owned(before, true) || before.Size() < 1 || before.Size() > MaximumProtectedConfigurationBytes {
		return failure()
	}
	raw, e := io.ReadAll(io.LimitReader(file, MaximumProtectedConfigurationBytes+1))
	after, se := file.Stat()
	current, le := root.Lstat(filepath.Base(path))
	if e != nil || se != nil || le != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || after.Mode() != 0600 || current.Mode() != 0600 || !owned(after, true) || !owned(current, true) || before.Size() != int64(len(raw)) || after.Size() != before.Size() || current.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return failure()
	}
	return Decode(raw)
}

// LoadProtectedWithEnvironment reads the same bounded, pinned private file
// protocol used by serving-policy references before applying the existing typed
// decoder and explicit environment authority. Ordinary LoadProtected is retained
// unchanged for its historical callers. Filesystem syscalls are not a hard
// context-cancellation boundary, even with bounded regular-file reads.
func LoadProtectedWithEnvironment(path, environmentFile string) (Config, error) {
	raw, e := readPrivateFile(path, MaximumProtectedConfigurationBytes, "CONFIG_PRIVATE_SNAPSHOT_REQUIRED")
	if e != nil {
		return Config{}, e
	}
	return decodeWithEnvironment(raw, environmentFile)
}
