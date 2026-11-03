package imageregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
)

// OperatorSnapshotRoot records physical custody, never permission to select a
// host path from HTTP. Source and fresh target identities remain distinct.
type OperatorSnapshotRoot struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
}

type operatorFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type operatorStamp struct {
	identity OperatorSnapshotRoot
	mode     os.FileMode
	size     int64
	modified time.Time
	changed  [2]int64
}

func operatorObserve(path string, info os.FileInfo) (operatorStamp, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.IsDir() && info.Mode() != os.ModeDir|0700 || !info.IsDir() && (info.Mode() != 0600 || stat.Nlink != 1) {
		return operatorStamp{}, failure("REGISTRY_SNAPSHOT_CUSTODY_INVALID")
	}
	v := reflect.ValueOf(stat).Elem()
	for _, field := range []string{"Ctim", "Ctimespec"} {
		stamp := v.FieldByName(field)
		if stamp.IsValid() && stamp.Kind() == reflect.Struct {
			sec, nano := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
			if sec.CanInt() && nano.CanInt() {
				return operatorStamp{OperatorSnapshotRoot{path, uint64(stat.Dev), uint64(stat.Ino), stat.Uid, stat.Gid}, info.Mode(), info.Size(), info.ModTime(), [2]int64{sec.Int(), nano.Int()}}, nil
			}
		}
	}
	return operatorStamp{}, failure("REGISTRY_SNAPSHOT_CUSTODY_INVALID")
}

func operatorRoot(path string) (*os.Root, OperatorSnapshotRoot, error) {
	bad := failure("REGISTRY_SNAPSHOT_CUSTODY_INVALID")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return nil, OperatorSnapshotRoot{}, bad
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return nil, OperatorSnapshotRoot{}, bad
	}
	before, e := os.Lstat(path)
	if e != nil || !before.IsDir() {
		return nil, OperatorSnapshotRoot{}, bad
	}
	stamp, e := operatorObserve(path, before)
	if e != nil {
		return nil, OperatorSnapshotRoot{}, e
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, OperatorSnapshotRoot{}, bad
	}
	opened, e := root.Stat(".")
	current, ce := os.Lstat(path)
	if e != nil || ce != nil || !os.SameFile(before, opened) || !os.SameFile(before, current) {
		root.Close()
		return nil, OperatorSnapshotRoot{}, bad
	}
	return root, stamp.identity, nil
}

type operatorTree struct {
	root     *os.Root
	identity OperatorSnapshotRoot
	stamps   map[string]operatorStamp
	files    []operatorFile
	bytes    int64
}

func (t *operatorTree) close() {
	if t != nil && t.root != nil {
		t.root.Close()
	}
}
func (t *operatorTree) check(ctx context.Context) error {
	for name, expected := range t.stamps {
		if e := ctx.Err(); e != nil {
			return e
		}
		info, e := t.root.Lstat(name)
		if e != nil {
			return failure("REGISTRY_SNAPSHOT_CHANGED")
		}
		actual, e := operatorObserve(name, info)
		if e != nil || actual != expected {
			return failure("REGISTRY_SNAPSHOT_CHANGED")
		}
	}
	root, id, e := operatorRoot(t.identity.Path)
	if e != nil {
		return e
	}
	root.Close()
	if id != t.identity {
		return failure("REGISTRY_SNAPSHOT_CHANGED")
	}
	return nil
}

func operatorRead(ctx context.Context, t *operatorTree, name string, maximum int64, output io.Writer) (operatorFile, error) {
	bad := failure("REGISTRY_SNAPSHOT_CHANGED")
	before, e := t.root.Lstat(name)
	if e != nil || before.IsDir() || before.Size() < 1 || before.Size() > maximum {
		return operatorFile{}, bad
	}
	stamp, e := operatorObserve(name, before)
	if e != nil {
		return operatorFile{}, e
	}
	f, e := t.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return operatorFile{}, bad
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil {
		return operatorFile{}, bad
	}
	actual, e := operatorObserve(name, opened)
	if e != nil || actual != stamp {
		return operatorFile{}, bad
	}
	h := sha256.New()
	var writer io.Writer = h
	if output != nil {
		writer = io.MultiWriter(h, output)
	}
	n, e := io.Copy(writer, &contextReader{ctx, io.LimitReader(f, before.Size()+1)})
	after, ae := f.Stat()
	current, ce := t.root.Lstat(name)
	if e != nil || ae != nil || ce != nil || n != before.Size() {
		return operatorFile{}, bad
	}
	for _, info := range []os.FileInfo{after, current} {
		actual, e = operatorObserve(name, info)
		if e != nil || actual != stamp {
			return operatorFile{}, bad
		}
	}
	if expected, ok := t.stamps[name]; ok && expected != stamp {
		return operatorFile{}, bad
	}
	t.stamps[name] = stamp
	return operatorFile{name, hex.EncodeToString(h.Sum(nil)), n}, nil
}

// scanOperatorTree pins every directory/file and rechecks the entire observed
// set. Rooted nonblocking, no-follow opens refuse FIFO/link substitution.
func scanOperatorTree(ctx context.Context, path string, c Config) (_ *operatorTree, result error) {
	root, id, e := operatorRoot(path)
	if e != nil {
		return nil, e
	}
	t := &operatorTree{root: root, identity: id, stamps: map[string]operatorStamp{}, files: []operatorFile{}}
	defer func() {
		if result != nil {
			t.close()
		}
	}()
	budget := c.Verification.MaximumManifestBytes
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if depth > 3 {
			return failure("REGISTRY_SNAPSHOT_INVALID")
		}
		info, e := root.Lstat(name)
		if e != nil {
			return e
		}
		stamp, e := operatorObserve(name, info)
		if e != nil {
			return e
		}
		budget -= int64(len(name) + 256)
		if budget < 0 {
			return failure("REGISTRY_CAPACITY_REQUIRED")
		}
		if !info.IsDir() {
			if len(t.files) >= c.Verification.MaximumArchiveEntries {
				return failure("REGISTRY_CAPACITY_REQUIRED")
			}
			file, e := operatorRead(ctx, t, name, c.MaximumRetainedBytes-t.bytes, nil)
			if e != nil {
				return e
			}
			t.files = append(t.files, file)
			t.bytes += file.Bytes
			return nil
		}
		t.stamps[name] = stamp
		f, e := root.Open(name + "/.")
		if e != nil {
			return e
		}
		opened, e := f.Stat()
		if e != nil {
			f.Close()
			return e
		}
		actual, e := operatorObserve(name, opened)
		if e != nil || actual != stamp {
			f.Close()
			return failure("REGISTRY_SNAPSHOT_CHANGED")
		}
		names := []string{}
		for {
			entries, re := f.ReadDir(128)
			for _, entry := range entries {
				n := entry.Name()
				if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\\x00") {
					f.Close()
					return failure("REGISTRY_SNAPSHOT_INVALID")
				}
				budget -= int64(len(n) + 32)
				if budget < 0 {
					f.Close()
					return failure("REGISTRY_CAPACITY_REQUIRED")
				}
				names = append(names, n)
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				f.Close()
				return re
			}
		}
		if e = f.Close(); e != nil {
			return e
		}
		sort.Strings(names)
		for _, n := range names {
			if e = walk(filepath.Join(name, n), depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	if e = walk(".", 0); e != nil {
		return nil, e
	}
	if e = t.check(ctx); e != nil {
		return nil, e
	}
	sort.Slice(t.files, func(i, j int) bool { return t.files[i].Path < t.files[j].Path })
	return t, nil
}

func operatorCopy(ctx context.Context, source *operatorTree, target *os.Root, expected operatorFile) error {
	f, e := target.OpenFile(expected.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return failure("REGISTRY_NEW_SNAPSHOT_REQUIRED")
	}
	actual, re := operatorRead(ctx, source, expected.Path, expected.Bytes, f)
	if re == nil && actual != expected {
		re = failure("REGISTRY_SNAPSHOT_CHANGED")
	}
	if re == nil {
		re = f.Sync()
	}
	return joinedClose(f, re)
}
