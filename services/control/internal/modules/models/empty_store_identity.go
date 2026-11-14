package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

type emptyDirectoryStamp struct {
	device, inode uint64
	uid, gid      uint32
	mode          os.FileMode
	size          int64
	modified      time.Time
	changed       [2]int64
}

// Both supported Unix targets expose a Stat_t change timespec under one of
// these ABI field names. Refuse an unsupported ABI instead of dropping ctime.
func emptyChangeTime(info os.FileInfo) ([2]int64, error) {
	v := reflect.ValueOf(info.Sys())
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return [2]int64{}, fail("MODEL_PATH_INVALID")
	}
	v = v.Elem()
	for _, name := range []string{"Ctim", "Ctimespec"} {
		stamp := v.FieldByName(name)
		if stamp.IsValid() && stamp.Kind() == reflect.Struct {
			seconds, nanos := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
			if seconds.IsValid() && seconds.CanInt() && nanos.IsValid() && nanos.CanInt() {
				return [2]int64{seconds.Int(), nanos.Int()}, nil
			}
		}
	}
	return [2]int64{}, fail("MODEL_PATH_INVALID")
}

func emptyStamp(info os.FileInfo, uid, gid int) (emptyDirectoryStamp, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || int(s.Uid) != uid || int(s.Gid) != gid {
		return emptyDirectoryStamp{}, fail("MODEL_PATH_INVALID")
	}
	ctime, e := emptyChangeTime(info)
	if e != nil {
		return emptyDirectoryStamp{}, e
	}
	return emptyDirectoryStamp{uint64(s.Dev), uint64(s.Ino), s.Uid, s.Gid, info.Mode(), info.Size(), info.ModTime(), ctime}, nil
}

// WithEmptyStoreIdentity coordinates fresh-store operators while their caller
// retains the actual installation lease and rejects all model history. It holds
// one canonical directory descriptor through initialization, proof and audit.
// This is an empty directory identity, never a namespace/initialization marker.
// Directory flock does not prevent arbitrary noncooperating same-UID writes.
func WithEmptyStoreIdentity(path string, uid, gid int, initialize bool, operation func(string, func() error) error) (result error) {
	if operation == nil || uid <= 0 || gid <= 0 || uid > 2147483647 || gid > 2147483647 || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
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
	directory, e := os.Open(path)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	opened, e := directory.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return fail("MODEL_PATH_INVALID")
	}
	if syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return fail("MODEL_STORE_IN_USE")
	}
	defer func() {
		if syscall.Flock(int(directory.Fd()), syscall.LOCK_UN) != nil {
			result = errors.Join(result, fail("MODEL_IO_FAILED"))
		}
	}()
	if initialize {
		if e = InitializeStore(path, uid, gid); e != nil {
			return e
		}
	}
	current, e := directory.Stat()
	if e != nil || !os.SameFile(opened, current) {
		return fail("MODEL_PATH_INVALID")
	}
	stamp, e := emptyStamp(current, uid, gid)
	if e != nil {
		return e
	}
	recheck := func() error {
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil || resolved != path {
			return fail("MODEL_PATH_INVALID")
		}
		actual, e := os.Lstat(path)
		if e != nil || !os.SameFile(current, actual) {
			return fail("MODEL_PATH_INVALID")
		}
		actualStamp, e := emptyStamp(actual, uid, gid)
		if e != nil || actualStamp != stamp {
			return fail("MODEL_STORE_CHANGED")
		}
		// An independent open starts at offset zero while SameFile binds it back
		// to the still-locked descriptor. Never accept a consumed ReadDir offset.
		reader, e := os.Open(path)
		if e != nil {
			return fail("MODEL_IO_FAILED")
		}
		info, statError := reader.Stat()
		entries, readError := reader.ReadDir(1)
		closeError := reader.Close()
		if statError != nil || !os.SameFile(current, info) || closeError != nil {
			return fail("MODEL_IO_FAILED")
		}
		if len(entries) != 0 {
			return fail("MODEL_STORE_NOT_EMPTY")
		}
		if readError != nil && readError != io.EOF {
			return fail("MODEL_IO_FAILED")
		}
		opened, e := directory.Stat()
		if e != nil {
			return fail("MODEL_IO_FAILED")
		}
		openedStamp, e := emptyStamp(opened, uid, gid)
		if e != nil || openedStamp != stamp {
			return fail("MODEL_STORE_CHANGED")
		}
		return nil
	}
	if e = recheck(); e != nil {
		return e
	}
	proof, e := json.Marshal(struct {
		Format, Path  string
		Device, Inode uint64
		UID, GID      int
	}{"projectone.model.empty-directory.v1", path, stamp.device, stamp.inode, uid, gid})
	if e != nil {
		return fail("MODEL_PATH_INVALID")
	}
	digest := sha256.Sum256(proof)
	result = operation(hex.EncodeToString(digest[:]), recheck)
	return errors.Join(result, recheck())
}
