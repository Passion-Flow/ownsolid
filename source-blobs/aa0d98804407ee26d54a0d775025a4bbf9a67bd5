package mysqlbackup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type heldRoot struct {
	root     *os.Root
	file     *os.File
	identity RootIdentity
}
type freshRoots struct{ held []heldRoot }

func rootIdentity(path string, info os.FileInfo) (RootIdentity, error) {
	if !owned(info, true) {
		return RootIdentity{}, errors.New("MYSQL_RESTORE_PRIVATE_ROOT_REQUIRED")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return RootIdentity{}, errors.New("MYSQL_RESTORE_PRIVATE_ROOT_REQUIRED")
	}
	return RootIdentity{path, uint64(stat.Dev), uint64(stat.Ino), stat.Uid, stat.Gid}, nil
}
func acquireFreshRoots(paths []string) (*freshRoots, error) {
	if len(paths) != 4 {
		return nil, errors.New("MYSQL_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
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
			return fail(errors.New("MYSQL_RESTORE_ROOT_LOCK_UNAVAILABLE"))
		}
		if syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			file.Close()
			root.Close()
			return fail(errors.New("MYSQL_RESTORE_ROOT_LOCK_UNAVAILABLE"))
		}
		info, e := file.Stat()
		if e != nil {
			file.Close()
			root.Close()
			return fail(errors.New("MYSQL_RESTORE_PRIVATE_ROOT_REQUIRED"))
		}
		identity, e := rootIdentity(path, info)
		if e != nil {
			file.Close()
			root.Close()
			return fail(e)
		}
		roots.held = append(roots.held, heldRoot{root, file, identity})
	}
	if e := roots.Check(true); e != nil {
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
		return errors.New("MYSQL_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
	}
	for _, held := range r.held {
		actual, e := held.file.Stat()
		if e != nil {
			return errors.New("MYSQL_RESTORE_ROOT_CHANGED")
		}
		named, e := os.Lstat(held.identity.Path)
		if e != nil {
			return errors.New("MYSQL_RESTORE_ROOT_CHANGED")
		}
		resolved, e := filepath.EvalSymlinks(held.identity.Path)
		if e != nil || resolved != held.identity.Path {
			return errors.New("MYSQL_RESTORE_ROOT_CHANGED")
		}
		opened, e := held.root.Stat(".")
		if e != nil {
			return errors.New("MYSQL_RESTORE_ROOT_CHANGED")
		}
		id, e := rootIdentity(held.identity.Path, actual)
		if e != nil || id != held.identity || !owned(named, true) || !owned(opened, true) || !os.SameFile(actual, named) || !os.SameFile(actual, opened) {
			return errors.New("MYSQL_RESTORE_ROOT_CHANGED")
		}
		if empty {
			reader, e := held.root.Open(".")
			if e != nil {
				return errors.New("MYSQL_RESTORE_FRESH_STORES_REQUIRED")
			}
			entries, readError := reader.ReadDir(1)
			closeError := reader.Close()
			if len(entries) != 0 || readError != nil && readError != io.EOF || closeError != nil {
				return errors.New("MYSQL_RESTORE_FRESH_STORES_REQUIRED")
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
			result = errors.Join(result, errors.New("MYSQL_RESTORE_ROOT_UNLOCK_FAILED"))
		}
		fileError, rootError := held.file.Close(), held.root.Close()
		if fileError != nil || rootError != nil {
			result = errors.Join(result, errors.New("MYSQL_RESTORE_ROOT_CLOSE_FAILED"))
		}
	}
	r.held = nil
	return result
}
