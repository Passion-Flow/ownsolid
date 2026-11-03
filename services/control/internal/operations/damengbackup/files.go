package damengbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type FileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
}

func identity(info os.FileInfo) FileIdentity {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileIdentity{}
	}
	return FileIdentity{uint64(s.Dev), uint64(s.Ino), s.Uid, s.Gid}
}
func sha(raw []byte) string        { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func jsonBytes(value any) []byte   { raw, _ := json.Marshal(value); return append(raw, '\n') }
func fingerprint(value any) string { return sha(jsonBytes(value)) }

// Private operation roots are owner-only. Every ancestor must resolve without
// symlinks; file descriptors and os.Root confine subsequent relative operations.
func noSymlinks(path string) error {
	if !canonical(path) {
		return fail("PATH_INVALID")
	}
	for p := path; p != "/"; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || i.Mode()&os.ModeSymlink != 0 {
			return fail("PATH_INVALID")
		}
		if p != path && (!i.IsDir() || i.Mode().Perm()&0022 != 0 && i.Mode()&os.ModeSticky == 0 || identity(i).UID != 0 && identity(i).UID != uint32(os.Geteuid())) {
			return fail("PATH_INVALID")
		}
	}
	return nil
}
func owned(info os.FileInfo, private, directory bool) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) && (private || s.Uid != 0) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700 && s.Uid == uint32(os.Geteuid())
	}
	mask := os.FileMode(0022)
	if private {
		mask = 0077
	}
	return info.Mode().IsRegular() && s.Nlink == 1 && info.Mode().Perm()&mask == 0 && info.Mode().Perm()&0400 != 0
}
func openPrivateRoot(path string) (*os.Root, error) {
	if noSymlinks(path) != nil {
		return nil, fail("ROOT_INVALID")
	}
	r, e := os.OpenRoot(path)
	if e != nil {
		return nil, fail("ROOT_INVALID")
	}
	i, e := r.Stat(".")
	if e != nil || !owned(i, true, true) {
		r.Close()
		return nil, fail("ROOT_INVALID")
	}
	return r, nil
}
func readPrivate(path string, maximum int64) ([]byte, error) {
	if noSymlinks(path) != nil {
		return nil, fail("PRIVATE_FILE_INVALID")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, fail("PRIVATE_FILE_INVALID")
	}
	defer f.Close()
	i, e := f.Stat()
	before, le := os.Lstat(path)
	if e != nil || le != nil || !os.SameFile(i, before) || !owned(i, true, false) || i.Size() < 1 || i.Size() > maximum {
		return nil, fail("PRIVATE_FILE_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	after, se := f.Stat()
	current, ce := os.Lstat(path)
	if e != nil || se != nil || ce != nil || !os.SameFile(i, current) || !owned(after, true, false) || !owned(current, true, false) || after.Mode() != i.Mode() || current.Mode() != i.Mode() || len(raw) == 0 || int64(len(raw)) != i.Size() || after.Size() != i.Size() || current.Size() != i.Size() || after.ModTime() != i.ModTime() || current.ModTime() != i.ModTime() {
		return nil, fail("PRIVATE_FILE_CHANGED")
	}
	return raw, nil
}
func syncRoot(r *os.Root) error {
	f, e := r.Open(".")
	if e != nil {
		return fail("DIRECTORY_SYNC_FAILED")
	}
	defer f.Close()
	if f.Sync() != nil {
		return fail("DIRECTORY_SYNC_FAILED")
	}
	return nil
}
func writeNew(r *os.Root, name string, raw []byte) error {
	f, e := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fail("DOCUMENT_EXISTS_OR_WRITE_FAILED")
	}
	n, e := f.Write(raw)
	if e == nil && n != len(raw) {
		e = io.ErrShortWrite
	}
	se := f.Sync()
	ce := f.Close()
	if e != nil || se != nil || ce != nil {
		return fail("DOCUMENT_DURABILITY_UNCERTAIN")
	}
	return syncRoot(r)
}
func readDocument(r *os.Root, name string, value any) ([]byte, error) {
	f, e := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, fail("DOCUMENT_REQUIRED")
	}
	defer f.Close()
	i, e := f.Stat()
	l, le := r.Lstat(name)
	if e != nil || le != nil || !os.SameFile(i, l) || !owned(i, true, false) || i.Size() > maximumDocumentBytes {
		return nil, fail("DOCUMENT_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximumDocumentBytes+1))
	after, se := f.Stat()
	current, ce := r.Lstat(name)
	if e != nil || se != nil || ce != nil || !os.SameFile(i, current) || after.Size() != i.Size() || after.ModTime() != i.ModTime() || int64(len(raw)) != i.Size() || strictJSON(raw, value) != nil || !bytes.Equal(raw, jsonBytes(value)) {
		return nil, fail("DOCUMENT_INVALID")
	}
	return raw, nil
}
func newRoot(path string) (*os.Root, error) {
	if !canonical(path) || len(path) > 190 {
		return nil, fail("ROOT_INVALID")
	}
	p, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer p.Close()
	if p.Mkdir(filepath.Base(path), 0700) != nil {
		return nil, fail("FRESH_ROOT_REQUIRED")
	}
	if e = syncRoot(p); e != nil {
		return nil, e
	}
	return openPrivateRoot(path)
}
func requireSpace(path string, peak int64, reserve uint64) error {
	var s syscall.Statfs_t
	if peak < 0 || syscall.Statfs(path, &s) != nil {
		return fail("CAPACITY_UNAVAILABLE")
	}
	free := uint64(s.Bavail) * uint64(s.Bsize)
	if free < reserve || uint64(peak) > free-reserve {
		return fail("CAPACITY_INSUFFICIENT")
	}
	return nil
}

type contextReader struct {
	ctx   context.Context
	input io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.input.Read(p)
}
func archive(ctx context.Context, r *os.Root, maximum int64, expected string) (string, int64, error) {
	f, e := r.OpenFile("database.dmp", os.O_RDWR, 0)
	if e != nil {
		return "", 0, fail("ARCHIVE_REQUIRED")
	}
	defer f.Close()
	i, e := f.Stat()
	l, le := r.Lstat("database.dmp")
	if e != nil || le != nil || !os.SameFile(i, l) || !owned(i, false, false) || identity(i).UID != uint32(os.Geteuid()) || i.Size() < 1 || i.Size() > maximum {
		return "", 0, fail("ARCHIVE_INVALID")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(contextReader{ctx, f}, maximum+1))
	after, se := f.Stat()
	d := hex.EncodeToString(h.Sum(nil))
	if e != nil || se != nil || n != i.Size() || after.Size() != i.Size() || after.ModTime() != i.ModTime() || expected != "" && d != expected || f.Chmod(0600) != nil || f.Sync() != nil {
		return "", 0, fail("ARCHIVE_CHANGED")
	}
	if syncRoot(r) != nil {
		return "", 0, fail("ARCHIVE_DURABILITY_UNCERTAIN")
	}
	return d, n, nil
}

// Completed archives are observations. A read-only source mount must never be
// chmodded, opened writable, or resealed while checking original proof.
func readArchive(ctx context.Context, r *os.Root, maximum int64, expected string) (string, int64, error) {
	f, e := r.OpenFile("database.dmp", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return "", 0, fail("ARCHIVE_REQUIRED")
	}
	defer f.Close()
	before, e := f.Stat()
	named, ne := r.Lstat("database.dmp")
	if e != nil || ne != nil || !owned(before, true, false) || before.Mode().Perm() != 0600 || !os.SameFile(before, named) || before.Size() < 1 || before.Size() > maximum || !digest.MatchString(expected) {
		return "", 0, fail("ARCHIVE_INVALID")
	}
	h := newHash()
	n, e := io.Copy(h, io.LimitReader(contextReader{ctx, f}, maximum+1))
	after, ae := f.Stat()
	current, ce := r.Lstat("database.dmp")
	value := hashString(h)
	if e != nil || ae != nil || ce != nil || n != before.Size() || !os.SameFile(before, current) || after.Size() != before.Size() || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() || current.Mode() != before.Mode() || value != expected {
		return "", 0, fail("ARCHIVE_CHANGED")
	}
	return value, n, nil
}
