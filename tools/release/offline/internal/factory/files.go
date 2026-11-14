package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

func fail(code string) error { return errors.New(code) }
func within(root, name string) bool {
	r, e := filepath.Rel(root, name)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && r != "."
}
func canonical(name string) bool {
	p, e := filepath.EvalSymlinks(name)
	return e == nil && filepath.IsAbs(name) && filepath.Clean(name) == name && p == name
}
func owned(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	v, ok := info.Sys().(*syscall.Stat_t)
	return ok && v != nil && int(v.Uid) == os.Geteuid()
}
func openRegular(name string, maximum int64, private bool) (*os.File, error) {
	return openRegularLinks(name, maximum, private, 1)
}
func openRegularLinks(name string, maximum int64, private bool, links uint64) (*os.File, error) {
	root, before, e := pinRegularInput(name, maximum, private, links)
	if e != nil {
		return nil, e
	}
	f, e := openPinnedRegular(root, filepath.Base(name), before, maximum, private, links)
	if closeError := root.Close(); closeError != nil {
		if f != nil {
			f.Close()
		}
		return nil, fail("RELEASE_INPUT_CHANGED")
	}
	return f, e
}

func regularInput(info os.FileInfo, maximum int64, private bool, links uint64) bool {
	if info == nil || maximum < 1 || !info.Mode().IsRegular() || !owned(info) || info.Size() < 1 || info.Size() > maximum || info.Mode().Perm()&0022 != 0 || private && info.Mode().Perm()&0077 != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st != nil && (links == 1 || links == 2) && uint64(st.Nlink) == links
}

// Change time has different native field names on Darwin and Linux. Unknown
// native facts refuse a strict snapshot rather than falling back to mtime only.
func sameRegularChangeTime(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return false
	}
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	if !aok || !bok || a == nil || b == nil {
		return false
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		x := reflect.ValueOf(a).Elem().FieldByName(name)
		y := reflect.ValueOf(b).Elem().FieldByName(name)
		if x.IsValid() && y.IsValid() && x.CanInterface() && y.CanInterface() {
			return reflect.DeepEqual(x.Interface(), y.Interface())
		}
	}
	return false
}

func sameRegularSnapshot(before, after os.FileInfo, maximum int64, private bool, links uint64) bool {
	if !regularInput(before, maximum, private, links) || !regularInput(after, maximum, private, links) || before.Mode() != after.Mode() || !sameVersion(before, after) || !sameRegularChangeTime(before, after) {
		return false
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	return a.Uid == b.Uid && a.Gid == b.Gid
}

// Directory permissions retain the release-input contract: public asset parents
// need not be private. Only the selected canonical directory identity is pinned.
func regularParentUnchanged(root *os.Root) bool {
	if root == nil {
		return false
	}
	parent := filepath.Clean(root.Name())
	if !canonical(parent) {
		return false
	}
	pinned, e := root.Stat(".")
	current, pe := os.Lstat(parent)
	return e == nil && pe == nil && pinned.IsDir() && current.IsDir() && os.SameFile(pinned, current)
}

func pinRegularInput(name string, maximum int64, private bool, links uint64) (*os.Root, os.FileInfo, error) {
	if maximum < 1 || !canonical(name) {
		return nil, nil, fail("RELEASE_REGULAR_FILE_REQUIRED")
	}
	parent := filepath.Dir(name)
	directory, e := os.Lstat(parent)
	if e != nil || !directory.IsDir() {
		return nil, nil, fail("RELEASE_REGULAR_FILE_REQUIRED")
	}
	// The terminal dot requires directory traversal even if the parent is
	// replaced by a FIFO before OpenRoot. Do not clean it away before opening.
	root, e := os.OpenRoot(parent + string(filepath.Separator) + ".")
	if e != nil {
		return nil, nil, fail("RELEASE_REGULAR_FILE_REQUIRED")
	}
	openedDirectory, e := root.Stat(".")
	if e != nil || !os.SameFile(directory, openedDirectory) || !regularParentUnchanged(root) {
		root.Close()
		return nil, nil, fail("RELEASE_INPUT_CHANGED")
	}
	before, e := root.Lstat(filepath.Base(name))
	if e != nil || !before.Mode().IsRegular() || !owned(before) || before.Size() < 1 || before.Size() > maximum || before.Mode().Perm()&0022 != 0 || private && before.Mode().Perm()&0077 != 0 {
		root.Close()
		return nil, nil, fail("RELEASE_REGULAR_FILE_REQUIRED")
	}
	if st, ok := before.Sys().(*syscall.Stat_t); !ok || st == nil || (links != 1 && links != 2) || uint64(st.Nlink) != links {
		root.Close()
		return nil, nil, fail("RELEASE_HARDLINK_REFUSED")
	}
	return root, before, nil
}

// This is the actual descriptor-open boundary used by release readers. Tests can
// replace a prechecked path before calling it without mutable production hooks.
// Root may resolve an in-root relative symlink despite O_NOFOLLOW; the named
// Lstat and descriptor checks must both pass before any file reaches a reader.
func openPinnedRegular(root *os.Root, name string, before os.FileInfo, maximum int64, private bool, links uint64) (*os.File, error) {
	if name == "." || name == ".." || name != filepath.Base(name) || !sameRegularSnapshot(before, before, maximum, private, links) || !regularParentUnchanged(root) {
		return nil, fail("RELEASE_INPUT_CHANGED")
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, fail("RELEASE_REGULAR_FILE_REQUIRED")
	}
	opened, e := f.Stat()
	current, pe := root.Lstat(name)
	if e != nil || pe != nil || !sameRegularSnapshot(before, opened, maximum, private, links) || !sameRegularSnapshot(before, current, maximum, private, links) || !regularParentUnchanged(root) {
		f.Close()
		return nil, fail("RELEASE_INPUT_CHANGED")
	}
	return f, nil
}
func readRegular(name string, maximum int64, private bool) (raw []byte, err error) {
	root, before, e := pinRegularInput(name, maximum, private, 1)
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := root.Close(); e != nil && err == nil {
			raw, err = nil, fail("RELEASE_INPUT_CHANGED")
		}
	}()
	f, e := openPinnedRegular(root, filepath.Base(name), before, maximum, private, 1)
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := f.Close(); e != nil && err == nil {
			raw, err = nil, fail("RELEASE_INPUT_CHANGED")
		}
	}()
	readLimit := maximum
	if maximum < int64(^uint64(0)>>1) {
		readLimit++
	}
	raw, e = io.ReadAll(io.LimitReader(f, readLimit))
	after, se := f.Stat()
	current, pe := root.Lstat(filepath.Base(name))
	if e != nil || se != nil || pe != nil || int64(len(raw)) != before.Size() || !sameRegularSnapshot(before, after, maximum, private, 1) || !sameRegularSnapshot(before, current, maximum, private, 1) || !regularParentUnchanged(root) {
		return nil, fail("RELEASE_INPUT_CHANGED")
	}
	return raw, nil
}
func sameVersion(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime() == after.ModTime()
}
func privateDirectory(name string) error {
	if !canonical(name) {
		return fail("RELEASE_PRIVATE_DIRECTORY_REQUIRED")
	}
	info, e := os.Stat(name)
	if e != nil || !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return fail("RELEASE_PRIVATE_DIRECTORY_REQUIRED")
	}
	return nil
}
func mounted(root string) error {
	if root == string(filepath.Separator) || !canonical(root) {
		return fail("RELEASE_REAL_MOUNT_REQUIRED")
	}
	var a, b syscall.Stat_t
	if syscall.Stat(root, &a) != nil || syscall.Stat(filepath.Dir(root), &b) != nil || a.Dev == b.Dev {
		return fail("RELEASE_REAL_MOUNT_REQUIRED")
	}
	return nil
}
func outputParent(c Config) (*os.File, error) {
	if mounted(c.MountRoot) != nil || !within(c.MountRoot, c.OutputFile) || filepath.Clean(c.OutputFile) != c.OutputFile || !strings.HasSuffix(c.OutputFile, ".tar.gz") || privateDirectory(filepath.Dir(c.OutputFile)) != nil {
		return nil, fail("RELEASE_NEW_PRIVATE_MOUNTED_OUTPUT_REQUIRED")
	}
	for _, name := range []string{c.OutputFile, c.OutputFile + ".incomplete", c.OutputFile + ".journal.jsonl"} {
		if _, e := os.Lstat(name); !errors.Is(e, os.ErrNotExist) {
			return nil, fail("RELEASE_OUTPUT_OCCUPIED_OR_UNCERTAIN")
		}
	}
	return os.Open(filepath.Dir(c.OutputFile))
}
func diskAdmission(c Config, additional int64) error {
	var s syscall.Statfs_t
	if syscall.Statfs(filepath.Dir(c.OutputFile), &s) != nil {
		return fail("RELEASE_CAPACITY_UNAVAILABLE")
	}
	total := uint64(s.Blocks) * uint64(s.Bsize)
	available := uint64(s.Bavail) * uint64(s.Bsize)
	if !hasConfiguredHeadroom(c, total, available, additional) {
		return fail("RELEASE_CAPACITY_HEADROOM_REQUIRED")
	}
	return nil
}

const minimumHeadroomPercent = 30

// A positive explicit reserve selects peak allocation plus fixed bytes on the
// already verified selected mount. Zero retains the legacy percentage contract.
// Host/internal backing/RAM budgets remain the execution owner's responsibility.
func hasConfiguredHeadroom(c Config, total, available uint64, additional int64) bool {
	if c.MinimumFreeBytes < 0 || c.MinimumFreeBytes > 1<<50 {
		return false
	}
	if c.MinimumFreeBytes == 0 {
		return hasHeadroom(total, available, additional)
	}
	return total > 0 && available <= total && additional >= 0 && uint64(additional) <= available && available-uint64(additional) >= uint64(c.MinimumFreeBytes)
}

func hasHeadroom(total, available uint64, additional int64) bool {
	reserve := total/100*minimumHeadroomPercent + (total%100*minimumHeadroomPercent+99)/100
	return total > 0 && additional >= 0 && uint64(additional) <= available && available-uint64(additional) >= reserve
}

type checkedReader struct {
	ctx context.Context
	r   io.Reader
}

func (r checkedReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, fail("RELEASE_DEADLINE_OR_INTERRUPTION")
	}
	return r.r.Read(p)
}
func streamHash(ctx context.Context, r io.Reader, w io.Writer, expected int64) (string, error) {
	if file, ok := r.(*os.File); ok {
		var e error
		r, e = newStreamReader(ctx, file)
		if e != nil {
			return "", e
		}
	}
	var output *descriptorWriter
	if file, ok := w.(*os.File); ok {
		var e error
		output, e = newStreamWriter(ctx, file)
		if e != nil {
			return "", e
		}
		w = output
	}
	h := sha256.New()
	target := io.Writer(h)
	if w != nil {
		target = io.MultiWriter(w, h)
	}
	n, e := io.CopyBuffer(target, io.LimitReader(checkedReader{ctx, r}, expected+1), make([]byte, 65536))
	if e != nil || n != expected {
		return "", fail("RELEASE_SIZE_OR_READ_FAILED")
	}
	if output != nil {
		if e = output.finish(); e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
