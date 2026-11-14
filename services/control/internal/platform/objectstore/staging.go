package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const stageSchema = 2
const stageMarker = "namespace.json"

type stageNamespace struct {
	Schema        int    `json:"schema"`
	ID            string `json:"id"`
	Tenant        string `json:"tenant"`
	BackendSHA256 string `json:"backend_sha256"`
}
type Stage struct {
	root        *os.Root
	lock        *os.File
	namespace   stageNamespace
	minimumFree int64
	reserved    uint64
	mu          sync.Mutex
	closed      bool
}
type Staged struct {
	File       *os.File
	ID, SHA256 string
	Bytes      int64
	info       os.FileInfo
}

func backendDigest(c config.ObjectStorageConfiguration) string {
	raw, _ := json.Marshal([]string{c.Backend, c.Endpoint, c.Region, c.Bucket})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func privateOwned(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && owner.Nlink == 1
}
func stagePath(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	before, e := os.Lstat(path)
	if e != nil || !privateOwned(before, true) {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	after, e := root.Stat(".")
	if e != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	return root, nil
}
func stageSync(root *os.Root, path string) error {
	f, e := root.Open(path)
	if e != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	defer f.Close()
	if f.Sync() != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return nil
}

// InitializeStage adopts only a preexisting empty private owned directory. It
// cannot create a missing mount, choose another root or adopt foreign contents.
func InitializeStage(c config.ObjectStorageConfiguration, tenant string) error {
	if c.Validate() != nil {
		return failure("OBJECT_CONFIGURATION_INVALID")
	}
	if _, e := (Ref{tenant, tenant, tenant}).Key(); e != nil {
		return e
	}
	root, e := stagePath(c.StagingRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	dir, e := root.Open(".")
	if e != nil {
		return failure("OBJECT_STAGING_INVALID")
	}
	entries, e := dir.ReadDir(1)
	dir.Close()
	if len(entries) != 0 || e != io.EOF {
		return failure("OBJECT_STAGING_NOT_EMPTY")
	}
	marker, _ := json.Marshal(stageNamespace{stageSchema, uuid.NewString(), tenant, backendDigest(c)})
	for name, raw := range map[string][]byte{stageMarker: marker, "owner.lock": {}} {
		f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return failure("OBJECT_STAGING_FAILED")
		}
		_, e = f.Write(raw)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return failure("OBJECT_STAGING_FAILED")
		}
	}
	if root.Mkdir("files", 0700) != nil || root.Mkdir("records", 0700) != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return stageSync(root, ".")
}
func OpenStage(c config.ObjectStorageConfiguration, tenant string) (*Stage, error) {
	if c.Validate() != nil {
		return nil, failure("OBJECT_CONFIGURATION_INVALID")
	}
	root, e := stagePath(c.StagingRoot)
	if e != nil {
		return nil, e
	}
	keep := false
	defer func() {
		if !keep {
			root.Close()
		}
	}()
	dir, e := root.Open(".")
	if e != nil {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	entries, e := dir.ReadDir(5)
	dir.Close()
	if e != nil && e != io.EOF || len(entries) != 4 {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	allowed := map[string]bool{stageMarker: true, "owner.lock": true, "files": true, "records": true}
	for _, v := range entries {
		if !allowed[v.Name()] {
			return nil, failure("OBJECT_STAGING_INVALID")
		}
		i, e := root.Lstat(v.Name())
		if e != nil || !privateOwned(i, v.Name() == "files" || v.Name() == "records") {
			return nil, failure("OBJECT_STAGING_INVALID")
		}
	}
	marker, e := root.OpenFile(stageMarker, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(marker, 4097))
	marker.Close()
	if e != nil || len(raw) > 4096 {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	var n stageNamespace
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&n) != nil || decoder.Decode(new(any)) != io.EOF || n.Schema != stageSchema || n.Tenant != tenant || n.BackendSHA256 != backendDigest(c) {
		return nil, failure("OBJECT_STAGING_NAMESPACE_MISMATCH")
	}
	if _, e := (Ref{n.Tenant, n.ID, n.ID}).Key(); e != nil {
		return nil, e
	}
	lock, e := root.OpenFile("owner.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	before, _ := root.Lstat("owner.lock")
	after, e := lock.Stat()
	if e != nil || !privateOwned(after, false) || !os.SameFile(before, after) || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		return nil, failure("OBJECT_STAGING_BUSY")
	}
	keep = true
	return &Stage{root: root, lock: lock, namespace: n, minimumFree: c.MinimumFreeBytes}, nil
}
func (s *Stage) Identity() string                                      { return s.namespace.BackendSHA256 }
func ConfigurationIdentity(c config.ObjectStorageConfiguration) string { return backendDigest(c) }
func (s *Stage) Namespace() string                                     { return s.namespace.ID }
func (s *Stage) Tenant() string                                        { return s.namespace.Tenant }
func (s *Stage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	e := s.root.Close()
	if s.lock.Close() != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return e
}
func (s *Stage) reserve(size int64) error {
	var fs syscall.Statfs_t
	if syscall.Fstatfs(int(s.lock.Fd()), &fs) != nil || size < 1 {
		return failure("OBJECT_STAGING_FAILED")
	}
	available, total := uint64(fs.Bavail)*uint64(fs.Bsize), uint64(fs.Blocks)*uint64(fs.Bsize)
	need := uint64(size) + uint64(s.minimumFree) + s.reserved
	if available < need || available-need < total*30/100 {
		return failure("OBJECT_STORAGE_FULL")
	}
	return nil
}
func (s *Stage) Write(ctx context.Context, id string, size int64, digest string, source io.Reader) (Staged, error) {
	var out Staged
	if _, e := (Ref{s.namespace.Tenant, id, id}).Key(); e != nil {
		return out, e
	}
	if ctx == nil || source == nil || size < 1 || !ValidDigest(digest) {
		return out, failure("OBJECT_REQUEST_INVALID")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return out, failure("OBJECT_STORAGE_CLOSED")
	}
	if e := s.reserve(size); e != nil {
		s.mu.Unlock()
		return out, e
	}
	f, e := s.root.OpenFile("files/"+id, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e == nil && syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		e = failure("OBJECT_STAGING_BUSY")
	}
	if e == nil {
		info, statError := f.Stat()
		if statError != nil || !privateOwned(info, false) {
			e = failure("OBJECT_STAGING_INVALID")
		} else {
			e = s.writeRecord(id, size, digest, info)
		}
		if e != nil {
			f.Close()
		} else {
			s.reserved += uint64(size)
		}
	}
	s.mu.Unlock()
	if e != nil {
		return out, failure("OBJECT_STAGING_CONFLICT")
	}
	defer func() { s.mu.Lock(); s.reserved -= uint64(size); s.mu.Unlock() }()
	keep := false
	defer func() {
		if !keep {
			f.Close()
		}
	}()
	hash := sha256.New()
	count, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(contextReader{ctx, source}, size+1))
	if e != nil || count != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return out, failure("OBJECT_DIGEST_MISMATCH")
	}
	if f.Sync() != nil || stageSync(s.root, "files") != nil {
		return out, failure("OBJECT_STAGING_FAILED")
	}
	info, e := f.Stat()
	if e != nil || !privateOwned(info, false) || info.Size() != size {
		return out, failure("OBJECT_STAGING_INVALID")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return out, failure("OBJECT_STAGING_FAILED")
	}
	keep = true
	return Staged{f, id, digest, size, info}, nil
}
func (s *Stage) Verify(v Staged) error {
	if v.File == nil || v.info == nil {
		return failure("OBJECT_STAGING_INVALID")
	}
	now, e := v.File.Stat()
	if e != nil || !privateOwned(now, false) || !os.SameFile(now, v.info) || now.Size() != v.Bytes || !now.ModTime().Equal(v.info.ModTime()) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	record, e := s.readRecord(v.ID)
	if e != nil || record.Bytes != v.Bytes || record.SHA256 != v.SHA256 || !recordMatches(record, now) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	path, e := s.root.Lstat("files/" + v.ID)
	if e != nil || !os.SameFile(path, now) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	return nil
}

// Detach unlinks only this verified owned temporary. The returned descriptor
// remains readable until the HTTP/ingestion consumer closes it, with no path leak.
func (s *Stage) Detach(v Staged) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, failure("OBJECT_STORAGE_CLOSED")
	}
	if e := s.Verify(v); e != nil {
		return nil, e
	}
	if s.root.Remove("files/"+v.ID) != nil || stageSync(s.root, "files") != nil {
		return nil, failure("OBJECT_STAGING_FAILED")
	}
	if e := s.removeRecord(v.ID); e != nil {
		return nil, e
	}
	return v.File, nil
}
func (s *Stage) Discard(v Staged) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.File.Close()
	if s.closed {
		return failure("OBJECT_STORAGE_CLOSED")
	}
	path, e := s.root.Lstat("files/" + v.ID)
	if e != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	if !os.SameFile(path, v.info) || !privateOwned(path, false) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	record, e := s.readRecord(v.ID)
	if e != nil || !recordMatches(record, path) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	if s.root.Remove("files/"+v.ID) != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	if stageSync(s.root, "files") != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return s.removeRecord(v.ID)
}

// OpenVerified resumes only a recorded exact UUID. It locks, hashes and rechecks
// the same private descriptor; an unknown filename never becomes owned metadata.
func (s *Stage) OpenVerified(ctx context.Context, id string, size int64, digest string) (Staged, error) {
	var out Staged
	if _, e := (Ref{s.namespace.Tenant, id, id}).Key(); e != nil {
		return out, e
	}
	if ctx == nil || size < 1 || !ValidDigest(digest) {
		return out, failure("OBJECT_REQUEST_INVALID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return out, failure("OBJECT_STORAGE_CLOSED")
	}
	before, e := s.root.Lstat("files/" + id)
	if e != nil || !privateOwned(before, false) || before.Size() != size {
		return out, failure("OBJECT_STAGING_INVALID")
	}
	file, e := s.root.OpenFile("files/"+id, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return out, failure("OBJECT_STAGING_INVALID")
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) || !privateOwned(opened, false) || syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return out, failure("OBJECT_STAGING_BUSY")
	}
	hash := sha256.New()
	count, e := io.Copy(hash, io.LimitReader(contextReader{ctx, file}, size+1))
	if e != nil || count != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return out, failure("OBJECT_DIGEST_MISMATCH")
	}
	value := Staged{file, id, digest, size, opened}
	if e = s.Verify(value); e != nil {
		return out, e
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return out, failure("OBJECT_STAGING_FAILED")
	}
	keep = true
	return value, nil
}
