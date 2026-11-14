package releases

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"reflect"
	"syscall"
)

func unavailable() error { return errors.New("RELEASE_VIEW_UNAVAILABLE") }
func changeTime(i os.FileInfo) any {
	v := reflect.ValueOf(i.Sys())
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}
	v = v.Elem()
	for _, name := range []string{"Ctim", "Ctimespec"} {
		f := v.FieldByName(name)
		if f.IsValid() && f.CanInterface() {
			return f.Interface()
		}
	}
	return nil
}
func fileIdentity(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime() == b.ModTime() && changeTime(a) != nil && reflect.DeepEqual(changeTime(a), changeTime(b))
}
func ownedFile(i os.FileInfo, uid int) bool {
	st, ok := i.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid && st.Nlink == 1 && i.Mode() == 0444 && i.Size() > 0 && i.Size() <= view.MaximumBytes
}
func ownedDirectory(i os.FileInfo, uid int) bool {
	st, ok := i.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid && i.Mode() == os.ModeDir|0755
}

// The original journal never enters the control container. A distinct publisher
// UID owns this read-only directory mount; same-UID serving is deliberately refused.
func readSnapshot(c Config) (view.Snapshot, error) { return readSnapshotForUID(c, os.Geteuid()) }

// The explicit UID is an internal observation seam for deterministic filesystem
// controls. Production always supplies the actual EUID above, never config/input.
func readSnapshotForUID(c Config, readerUID int) (view.Snapshot, error) {
	var s view.Snapshot
	if c.Validate() != nil || readerUID < 1 || c.PublisherUID == readerUID {
		return s, unavailable()
	}
	resolved, e := filepath.EvalSymlinks(c.Directory)
	if e != nil || resolved != c.Directory {
		return s, unavailable()
	}
	dir, e := os.Lstat(c.Directory)
	if e != nil || !ownedDirectory(dir, c.PublisherUID) {
		return s, unavailable()
	}
	r, e := os.OpenRoot(c.Directory)
	if e != nil {
		return s, unavailable()
	}
	defer r.Close()
	rootFile, e := r.Open(".")
	if e != nil {
		return s, unavailable()
	}
	rootInfo, e := rootFile.Stat()
	rootFile.Close()
	if e != nil || !os.SameFile(dir, rootInfo) {
		return s, unavailable()
	}
	dirty := func() bool { _, e := r.Lstat(view.DirtyFile); return !os.IsNotExist(e) }
	if dirty() {
		return s, unavailable()
	}
	before, e := r.Lstat(view.SnapshotFile)
	if e != nil || !ownedFile(before, c.PublisherUID) {
		return s, unavailable()
	}
	f, e := r.OpenFile(view.SnapshotFile, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return s, unavailable()
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !fileIdentity(before, opened) {
		return s, unavailable()
	}
	raw, e := io.ReadAll(io.LimitReader(f, view.MaximumBytes+1))
	if e != nil || int64(len(raw)) != before.Size() {
		return s, unavailable()
	}
	after, e := f.Stat()
	if e != nil || !fileIdentity(before, after) {
		return s, unavailable()
	}
	named, e := r.Lstat(view.SnapshotFile)
	if e != nil || !fileIdentity(before, named) {
		return s, unavailable()
	}
	actual, e := os.Lstat(c.Directory)
	if e != nil || !os.SameFile(dir, actual) || !ownedDirectory(actual, c.PublisherUID) || dirty() {
		return s, unavailable()
	}
	if wire.StrictJSON(bytes.TrimSpace(raw), &s) != nil || s.Validate() != nil || s.ScopeID != c.ScopeID || s.ManifestSHA256 != c.ManifestSHA256 || s.ConfigurationSHA256 != c.ConfigurationSHA256 {
		return view.Snapshot{}, unavailable()
	}
	return s, nil
}
