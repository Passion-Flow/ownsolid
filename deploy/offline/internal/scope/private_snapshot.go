package scope

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

type privateParent struct {
	path string
	info os.FileInfo
}

func samePrivateDirectory(before, after os.FileInfo) bool {
	if before == nil || after == nil || !owned(before) || !owned(after) || before.Mode() != os.ModeDir|0700 || after.Mode() != before.Mode() || !os.SameFile(before, after) {
		return false
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	return a.Uid == b.Uid && a.Gid == b.Gid
}

func privateParentsUnchanged(root, parent *os.Root, parents []privateParent) bool {
	if root == nil || len(parents) == 0 {
		return false
	}
	info, e := root.Stat(".")
	if e != nil || !samePrivateDirectory(parents[0].info, info) {
		return false
	}
	for _, entry := range parents {
		info, e := os.Lstat(entry.path)
		if e != nil || !canonical(entry.path) || !samePrivateDirectory(entry.info, info) {
			return false
		}
	}
	if parent != nil {
		info, e := parent.Stat(".")
		return e == nil && samePrivateDirectory(parents[len(parents)-1].info, info)
	}
	return true
}

// Pin the immediate directory as well as every named ancestor inside the private
// root. Root confinement alone would permit symlinks to other in-root objects.
func pinPrivateParent(root *os.Root, name string) (*os.Root, []privateParent, error) {
	if root == nil || !filepath.IsLocal(name) || filepath.Clean(name) != name || name == "." {
		return nil, nil, failure("OFFLINE_SCOPE_PRIVATE_REGULAR_FILE_REQUIRED")
	}
	base := filepath.Clean(root.Name())
	info, e := os.Lstat(base)
	if e != nil || !canonical(base) || !samePrivateDirectory(info, info) {
		return nil, nil, failure("OFFLINE_SCOPE_PRIVATE_REGULAR_FILE_REQUIRED")
	}
	parents := []privateParent{{base, info}}
	relative := filepath.Dir(name)
	if relative != "." {
		prefix := ""
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			prefix = filepath.Join(prefix, part)
			info, e := root.Lstat(prefix)
			path := filepath.Join(base, prefix)
			if e != nil || !canonical(path) || !samePrivateDirectory(info, info) {
				return nil, nil, failure("OFFLINE_SCOPE_PRIVATE_REGULAR_FILE_REQUIRED")
			}
			parents = append(parents, privateParent{path, info})
		}
	}
	if !privateParentsUnchanged(root, nil, parents) {
		return nil, nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	// Keep the terminal dot: replacing a checked directory with a FIFO must
	// produce ENOTDIR instead of blocking while opening the FIFO.
	parent, e := root.OpenRoot(relative + string(filepath.Separator) + ".")
	if e != nil {
		return nil, nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	if !privateParentsUnchanged(root, parent, parents) {
		parent.Close()
		return nil, nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	return parent, parents, nil
}

func privateRegular(info os.FileInfo, maximum int64) bool {
	if info == nil || maximum < 1 || !owned(info) || info.Mode() != 0600 || info.Size() < 1 || info.Size() > maximum {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st != nil && st.Nlink == 1
}

func samePrivateChangeTime(before, after os.FileInfo) bool {
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	if !aok || !bok || a == nil || b == nil {
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

func samePrivateSnapshot(before, after os.FileInfo, maximum int64) bool {
	if !privateRegular(before, maximum) || !privateRegular(after, maximum) || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || !samePrivateChangeTime(before, after) {
		return false
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	return a.Uid == b.Uid && a.Gid == b.Gid
}

// The real open boundary also permits deterministic race tests without mutable
// production hooks. Both the descriptor and leaf name must match the snapshot.
func openPrivateSnapshot(root, parent *os.Root, parents []privateParent, name string, before os.FileInfo, maximum int64) (*os.File, error) {
	if name != filepath.Base(name) || !samePrivateSnapshot(before, before, maximum) || !privateParentsUnchanged(root, parent, parents) {
		return nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	f, e := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_FILE_UNAVAILABLE")
	}
	opened, se := f.Stat()
	named, ne := parent.Lstat(name)
	if se != nil || ne != nil || !samePrivateSnapshot(before, opened, maximum) || !samePrivateSnapshot(before, named, maximum) || !privateParentsUnchanged(root, parent, parents) {
		f.Close()
		return nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	return f, nil
}

func readPrivateSnapshot(root, parent *os.Root, parents []privateParent, name string, before os.FileInfo, f *os.File, maximum int64) (raw []byte, err error) {
	defer func() {
		if e := f.Close(); e != nil && err == nil {
			raw, err = nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
		}
	}()
	readLimit := maximum
	if maximum < int64(^uint64(0)>>1) {
		readLimit++
	}
	raw, e := io.ReadAll(io.LimitReader(f, readLimit))
	after, se := f.Stat()
	named, ne := parent.Lstat(name)
	if e != nil || se != nil || ne != nil || int64(len(raw)) != before.Size() || !samePrivateSnapshot(before, after, maximum) || !samePrivateSnapshot(before, named, maximum) || !privateParentsUnchanged(root, parent, parents) {
		return nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	return raw, nil
}

func regular(root *os.Root, name string, maximum int64) (raw []byte, err error) {
	if maximum < 1 {
		return nil, failure("OFFLINE_SCOPE_PRIVATE_REGULAR_FILE_REQUIRED")
	}
	parent, parents, e := pinPrivateParent(root, name)
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := parent.Close(); e != nil && err == nil {
			raw, err = nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
		}
	}()
	leaf := filepath.Base(name)
	before, e := parent.Lstat(leaf)
	if e != nil || !owned(before) || before.Mode() != 0600 || before.Size() < 1 || before.Size() > maximum {
		return nil, failure("OFFLINE_SCOPE_PRIVATE_REGULAR_FILE_REQUIRED")
	}
	if !privateRegular(before, maximum) {
		return nil, failure("OFFLINE_SCOPE_FILE_LINK_REFUSED")
	}
	f, e := openPrivateSnapshot(root, parent, parents, leaf, before, maximum)
	if e != nil {
		return nil, e
	}
	return readPrivateSnapshot(root, parent, parents, leaf, before, f, maximum)
}

func readPrivateInput(c configuration.Config, name string, maximum int64, referenceError string) (raw []byte, err error) {
	if !canonical(name) || !contained(c.Verification.MountRoot, name) || !privateDirectory(filepath.Dir(name)) {
		return nil, failure(referenceError)
	}
	before, e := os.Lstat(filepath.Dir(name))
	if e != nil {
		return nil, failure(referenceError)
	}
	root, e := os.OpenRoot(filepath.Dir(name) + string(filepath.Separator) + ".")
	if e != nil {
		return nil, failure(referenceError)
	}
	defer func() {
		if e := root.Close(); e != nil && err == nil {
			raw, err = nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
		}
	}()
	opened, e := root.Stat(".")
	if e != nil || !samePrivateDirectory(before, opened) {
		return nil, failure("OFFLINE_SCOPE_FILE_CHANGED")
	}
	return regular(root, filepath.Base(name), maximum)
}
