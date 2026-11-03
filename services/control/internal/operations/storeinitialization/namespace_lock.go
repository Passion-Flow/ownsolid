package storeinitialization

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

type namespaceStamp struct {
	device, inode uint64
	uid, gid      uint32
	mode          os.FileMode
	size          int64
	modified      time.Time
	changed       [2]int64
}

func namespacePathStamp(path string, uid, gid int, directory bool) (namespaceStamp, os.FileInfo, error) {
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return namespaceStamp{}, nil, errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil {
		return namespaceStamp{}, nil, errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != uid || int(owner.Gid) != gid || directory && (!info.IsDir() || info.Mode().Perm() != 0700) || !directory && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600 || owner.Nlink != 1) {
		return namespaceStamp{}, nil, errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	value := reflect.ValueOf(owner).Elem()
	for _, name := range []string{"Ctim", "Ctimespec"} {
		stamp := value.FieldByName(name)
		if stamp.IsValid() && stamp.Kind() == reflect.Struct {
			sec, nano := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
			if sec.IsValid() && sec.CanInt() && nano.IsValid() && nano.CanInt() {
				return namespaceStamp{uint64(owner.Dev), uint64(owner.Ino), owner.Uid, owner.Gid, info.Mode(), info.Size(), info.ModTime(), [2]int64{sec.Int(), nano.Int()}}, info, nil
			}
		}
	}
	return namespaceStamp{}, nil, errors.New("STORE_INITIALIZATION_PATH_INVALID")
}

// All three existing namespace formats use the actual retained owner.lock.
// Opening that exact inode without creation keeps normal store ownership fences.
func withNamespaceLock(o Options, operation func(func() error) error) (result error) {
	if operation == nil {
		return errors.New("STORE_INITIALIZATION_CONTEXT_INVALID")
	}
	paths := []string{o.Root, filepath.Join(o.Root, "owner.lock"), filepath.Join(o.Root, "namespace.json")}
	stamps := make([]namespaceStamp, len(paths))
	infos := make([]os.FileInfo, len(paths))
	for i, p := range paths {
		var e error
		stamps[i], infos[i], e = namespacePathStamp(p, o.UID, o.GID, i == 0)
		if e != nil {
			return e
		}
	}
	if stamps[1].size != 0 {
		return errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	root, e := os.Open(paths[0])
	if e != nil {
		return errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	rootInfo, e := root.Stat()
	if e != nil || !os.SameFile(infos[0], rootInfo) {
		return errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	lock, e := os.OpenFile(paths[1], os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return errors.New("STORE_INITIALIZATION_LOCK_UNAVAILABLE")
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	opened, e := lock.Stat()
	if e != nil || !os.SameFile(infos[1], opened) {
		return errors.New("STORE_INITIALIZATION_PATH_INVALID")
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errors.New("STORE_INITIALIZATION_IN_USE")
	}
	defer func() {
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) != nil {
			result = errors.Join(result, errors.New("STORE_INITIALIZATION_LOCK_RELEASE_FAILED"))
		}
	}()
	recheck := func() error {
		for i, p := range paths {
			actual, info, e := namespacePathStamp(p, o.UID, o.GID, i == 0)
			if e != nil || actual != stamps[i] || !os.SameFile(infos[i], info) {
				return errors.New("STORE_INITIALIZATION_PROOF_CHANGED")
			}
		}
		actual, e := lock.Stat()
		if e != nil || !os.SameFile(infos[1], actual) {
			return errors.New("STORE_INITIALIZATION_PROOF_CHANGED")
		}
		actual, e = root.Stat()
		if e != nil || !os.SameFile(infos[0], actual) {
			return errors.New("STORE_INITIALIZATION_PROOF_CHANGED")
		}
		return nil
	}
	if e = recheck(); e != nil {
		return e
	}
	result = operation(recheck)
	return errors.Join(result, recheck())
}
