// Reuses the existing exact fresh-directory lock/identity protocol without
// changing the MySQL owner or its historical acceptance evidence.
package postgresbackup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RootIdentity = release.PostgreSQLRestoreRoot

func matchExpectedRestoreRoots(expected, actual []RootIdentity) error {
	if expected == nil {
		return nil
	}
	if !release.ValidPostgreSQLRestoreRoots(expected) || len(actual) != len(expected) {
		return errors.New("POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED")
	}
	for i := range expected {
		if expected[i] != actual[i] {
			return errors.New("POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED")
		}
	}
	return nil
}

// No SQL or write occurs here. Native Restore also compares the identities of
// its locked handles, closing the interval between this admission and import.
func admitExpectedRestoreRoots(expected []RootIdentity, paths []string) error {
	if expected == nil {
		return nil
	}
	actual := make([]RootIdentity, 0, len(paths))
	for _, path := range paths {
		info, e := os.Lstat(path)
		if e != nil {
			return errors.New("POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED")
		}
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil || resolved != path {
			return errors.New("POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED")
		}
		id, e := rootIdentity(path, info)
		if e != nil {
			return errors.New("POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED")
		}
		actual = append(actual, id)
	}
	return matchExpectedRestoreRoots(expected, actual)
}

type heldRoot struct {
	root     *os.Root
	file     *os.File
	identity RootIdentity
}
type freshRoots struct{ held []heldRoot }

func rootIdentity(path string, info os.FileInfo) (RootIdentity, error) {
	if !owned(info, true) {
		return RootIdentity{}, errors.New("POSTGRES_RESTORE_PRIVATE_ROOT_REQUIRED")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return RootIdentity{}, errors.New("POSTGRES_RESTORE_PRIVATE_ROOT_REQUIRED")
	}
	return RootIdentity{Path: path, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), UID: stat.Uid, GID: stat.Gid}, nil
}
func acquireFreshRoots(paths []string) (*freshRoots, error) {
	return acquireRestoreRoots(paths, true)
}
func acquireRestoreRoots(paths []string, empty bool) (*freshRoots, error) {
	if len(paths) != 4 {
		return nil, errors.New("POSTGRES_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
	}
	roots := &freshRoots{}
	fail := func(e error) (*freshRoots, error) { return nil, errors.Join(e, roots.Close()) }
	for _, path := range paths {
		root, e := openPrivateRoot(path)
		if e != nil {
			return fail(e)
		}
		file, e := root.Open(".")
		if e != nil {
			root.Close()
			return fail(errors.New("POSTGRES_RESTORE_ROOT_LOCK_UNAVAILABLE"))
		}
		if syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			file.Close()
			root.Close()
			return fail(errors.New("POSTGRES_RESTORE_ROOT_LOCK_UNAVAILABLE"))
		}
		info, e := file.Stat()
		if e != nil {
			file.Close()
			root.Close()
			return fail(errors.New("POSTGRES_RESTORE_PRIVATE_ROOT_REQUIRED"))
		}
		identity, e := rootIdentity(path, info)
		if e != nil {
			file.Close()
			root.Close()
			return fail(e)
		}
		roots.held = append(roots.held, heldRoot{root, file, identity})
	}
	if e := roots.Check(empty); e != nil {
		return fail(e)
	}
	return roots, nil
}
func (r *freshRoots) Identities() []RootIdentity {
	result := []RootIdentity{}
	for _, held := range r.held {
		result = append(result, held.identity)
	}
	return result
}
func (r *freshRoots) Check(empty bool) error {
	if r == nil || len(r.held) != 4 {
		return errors.New("POSTGRES_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
	}
	for _, held := range r.held {
		actual, e := held.file.Stat()
		if e != nil {
			return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
		}
		named, e := os.Lstat(held.identity.Path)
		if e != nil {
			return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
		}
		resolved, e := filepath.EvalSymlinks(held.identity.Path)
		if e != nil || resolved != held.identity.Path {
			return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
		}
		opened, e := held.root.Stat(".")
		if e != nil {
			return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
		}
		id, e := rootIdentity(held.identity.Path, actual)
		if e != nil || id != held.identity || !owned(named, true) || !owned(opened, true) || !os.SameFile(actual, named) || !os.SameFile(actual, opened) {
			return errors.New("POSTGRES_RESTORE_ROOT_CHANGED")
		}
		if empty {
			reader, e := held.root.Open(".")
			if e != nil {
				return errors.New("POSTGRES_RESTORE_FRESH_STORES_REQUIRED")
			}
			entries, readError := reader.ReadDir(1)
			closeError := reader.Close()
			if len(entries) != 0 || readError != nil && readError != io.EOF || closeError != nil {
				return errors.New("POSTGRES_RESTORE_FRESH_STORES_REQUIRED")
			}
		}
	}
	return nil
}
func (r *freshRoots) Close() (result error) {
	if r == nil {
		return nil
	}
	for n := len(r.held) - 1; n >= 0; n-- {
		held := r.held[n]
		if syscall.Flock(int(held.file.Fd()), syscall.LOCK_UN) != nil {
			result = errors.Join(result, errors.New("POSTGRES_RESTORE_ROOT_UNLOCK_FAILED"))
		}
		fileError, rootError := held.file.Close(), held.root.Close()
		if fileError != nil || rootError != nil {
			result = errors.Join(result, errors.New("POSTGRES_RESTORE_ROOT_CLOSE_FAILED"))
		}
	}
	r.held = nil
	return result
}
