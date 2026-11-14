package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/google/uuid"
)

const storeSchema = 1
const markerFile = "namespace.json"
const maximumNamespaceBytes int64 = 4096
const lockFile = "owner.lock"

type storeNamespace struct {
	SchemaVersion int    `json:"schema_version"`
	StoreID       string `json:"store_id"`
	TenantID      string `json:"tenant_id"`
}
type Store struct {
	verification chan struct{}
	root         *os.Root
	lock         *os.File
	path         string
	namespace    storeNamespace
	minimumFree  int64
	mu           sync.Mutex
	closed       bool
}

// InitializeStore only adopts an explicitly selected existing empty directory. It does not create a missing mount or modify populated storage.
func InitializeStore(path, tenant string, uid, gid int) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !wire.ValidID(tenant) || uid < 0 || gid < 0 || uid > 2147483647 || gid > 2147483647 {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() && !(os.Geteuid() == 0 && int(owner.Uid) == uid) {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	if os.Geteuid() != 0 && (uid != os.Geteuid() || gid != os.Getegid()) {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	directory, e := os.Open(path)
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer directory.Close()
	current, e := directory.Stat()
	if e != nil || !os.SameFile(info, current) {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	entries, e := directory.ReadDir(1)
	if len(entries) != 0 {
		return failure("DATASET_STORE_NOT_EMPTY")
	}
	if e != nil && e != io.EOF {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if directory.Chmod(0700) != nil || directory.Chown(uid, gid) != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	for _, name := range []string{"objects", "attempts", "versions"} {
		p := filepath.Join(path, name)
		if os.Mkdir(p, 0700) != nil || os.Chown(p, uid, gid) != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	marker := storeNamespace{storeSchema, uuid.NewString(), tenant}
	raw, _ := json.Marshal(marker)
	for name, content := range map[string][]byte{markerFile: append(raw, '\n'), lockFile: {}} {
		p := filepath.Join(path, name)
		if e = writeNew(p, content); e != nil {
			return e
		}
		if os.Chown(p, uid, gid) != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	if directory.Sync() != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return nil
}

func OpenStore(path, tenant string, minimumFree int64) (*Store, error) {
	return openStore(path, tenant, minimumFree, false)
}

// OpenStoreForVerification retains the same exclusive ownership lock using a
// read-only descriptor. The caller enforces readonly mounts; this is not a
// general mutation sandbox and never initializes a missing namespace.
func OpenStoreForVerification(path, tenant string, minimumFree int64) (*Store, error) {
	return openStore(path, tenant, minimumFree, true)
}

func openStore(path, tenant string, minimumFree int64, verification bool) (*Store, error) {
	if !wire.ValidID(tenant) || minimumFree < 4096 {
		return nil, failure("DATASET_STORAGE_PATH_INVALID")
	}
	marker, e := readStoreNamespace(path, tenant)
	if e != nil {
		return nil, e
	}
	lockMode := os.O_RDWR
	if verification {
		lockMode = os.O_RDONLY
	}
	lock, e := os.OpenFile(filepath.Join(path, lockFile), lockMode, 0)
	if e != nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, failure("DATASET_STORE_IN_USE")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		lock.Close()
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return &Store{verification: make(chan struct{}, 1), root: root, lock: lock, path: path, namespace: marker, minimumFree: minimumFree}, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	e := s.root.Close()
	if s.lock.Close() != nil && e == nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return e
}
func (s *Store) Namespace() string { return s.namespace.StoreID }
func (s *Store) ensureDirectory(relative string) error {
	info, e := s.root.Lstat(relative)
	if os.IsNotExist(e) {
		if e = s.root.Mkdir(relative, 0700); e != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		if e = s.sync(filepath.Dir(relative)); e != nil {
			return e
		}
	}
	_, e = privatePath(filepath.Join(s.path, relative), s.path, true, 0)
	if e != nil {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	_ = info
	return nil
}
func (s *Store) sync(relative string) error {
	file, e := s.root.Open(relative)
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	if file.Sync() != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return nil
}
func (s *Store) reserve(bytes int64) error {
	var stat syscall.Statfs_t
	if syscall.Fstatfs(int(s.lock.Fd()), &stat) != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if bytes < 0 || uint64(bytes) > available || uint64(s.minimumFree) > available-uint64(bytes) {
		return failure("DATASET_STORAGE_FULL")
	}
	return nil
}
func objectPath(id string) string { return "objects/" + id + "/source.jsonl" }
func (s *Store) Source(id, digest string, size int64) (string, error) {
	if !wire.ValidID(id) || !wire.ValidDigest(digest) || size < 1 {
		return "", failure("DATASET_FILE_INVALID")
	}
	path := filepath.Join(s.path, objectPath(id))
	if _, e := privatePath(path, s.path, false, size); e != nil {
		return "", failure("DATASET_FILE_INVALID")
	}
	actual, n, e := fileDigest(path, size)
	if e != nil || actual != digest || n != size {
		return "", failure("DATASET_FILE_INVALID")
	}
	return path, nil
}

type boundedContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r boundedContextReader) Read(b []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	return r.reader.Read(b)
}
func (s *Store) PutSource(ctx context.Context, id, uploadID, digest string, size int64, source io.Reader) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !wire.ValidID(id) || !wire.ValidID(uploadID) || !wire.ValidDigest(digest) || size < 1 {
		return "", failure("DATASET_FILE_INVALID")
	}
	if existing, e := s.Source(id, digest, size); e == nil {
		return existing, nil
	}
	if e := s.reserve(size); e != nil {
		return "", e
	}
	if e := s.ensureDirectory("objects/" + id); e != nil {
		return "", e
	}
	temporary := "objects/" + id + "/upload-" + uploadID + ".jsonl"
	file, e := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", failure("DATASET_UPLOAD_BUSY")
	}
	hash := sha256.New()
	n, copyError := io.Copy(io.MultiWriter(file, hash), boundedContextReader{ctx, io.LimitReader(source, size+1)})
	if copyError == nil {
		copyError = file.Sync()
	}
	closeError := file.Close()
	// Failed and uncertain owned attempts are retained; no unrelated data or competing upload is removed.
	if copyError != nil || closeError != nil || n != size {
		return "", failure("DATASET_UPLOAD_INCOMPLETE")
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", failure("DATASET_SOURCE_CHANGED")
	}
	if e = s.root.Link(temporary, objectPath(id)); e != nil {
		if existing, check := s.Source(id, digest, size); check == nil {
			return existing, nil
		}
		return "", failure("DATASET_SOURCE_CHANGED")
	}
	if e = s.sync("objects/" + id); e != nil {
		return "", e
	}
	return s.Source(id, digest, size)
}

// CreateAttempt reserves and stages the protected input for a standard attempt.
func (s *Store) CreateAttempt(job, attempt string, input io.Reader, digest string, size int64, maximumOutput int64) (string, string, string, error) {
	return s.createAttempt(job, attempt, input, digest, size, maximumOutput, false)
}

func (s *Store) createAttempt(job, attempt string, input io.Reader, digest string, size, maximumOutput int64, withTest bool) (string, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !wire.ValidID(job) || !wire.ValidID(attempt) || !wire.ValidDigest(digest) || size < 1 || maximumOutput < 4096 {
		return "", "", "", failure("DATASET_ATTEMPT_STALE")
	}
	// The result, manifest, and five standard assets each have the output limit.
	outputFiles := int64(7)
	if withTest {
		outputFiles++
	}
	if e := s.reserve(size + maximumOutput*outputFiles); e != nil {
		return "", "", "", e
	}
	if e := s.ensureDirectory("attempts/" + job); e != nil {
		return "", "", "", e
	}
	relative := "attempts/" + job + "/" + attempt
	if s.root.Mkdir(relative, 0700) != nil {
		return "", "", "", failure("DATASET_OUTPUT_CONFLICT")
	}
	if e := s.sync("attempts/" + job); e != nil {
		return "", "", "", e
	}
	path := relative + "/input.jsonl"
	file, e := s.root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", "", "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, size+1))
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", "", "", failure("DATASET_SOURCE_CHANGED")
	}
	if s.root.Mkdir(relative+"/output", 0700) != nil {
		return "", "", "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if e = s.sync(relative); e != nil {
		return "", "", "", e
	}
	scope := filepath.Join(s.path, relative)
	return scope, filepath.Join(scope, "input.jsonl"), filepath.Join(scope, "output"), nil
}

// readStoreNamespace neither initializes storage nor acquires writer ownership.
func readStoreNamespace(path, tenant string) (storeNamespace, error) {
	if !wire.ValidID(tenant) {
		return storeNamespace{}, failure("DATASET_STORAGE_PATH_INVALID")
	}
	if _, e := privatePath(path, "", true, 0); e != nil {
		return storeNamespace{}, failure("DATASET_STORAGE_PATH_INVALID")
	}
	directory, e := os.Open(path)
	if e != nil {
		return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	entries, e := directory.ReadDir(6)
	closeError := directory.Close()
	if e != nil && e != io.EOF || closeError != nil || len(entries) != 5 {
		return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	allowed := map[string]bool{markerFile: true, lockFile: true, "objects": true, "attempts": true, "versions": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
		}
	}
	if _, e = privatePath(filepath.Join(path, markerFile), path, false, maximumNamespaceBytes); e != nil {
		return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	raw, e := readNamespaceFile(filepath.Join(path, markerFile))
	var marker storeNamespace
	if e != nil || wire.DecodeStrict(raw, &marker) != nil || marker.SchemaVersion != storeSchema || marker.TenantID != tenant || !wire.ValidID(marker.StoreID) {
		return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	for _, name := range []string{"objects", "attempts", "versions"} {
		if _, e = privatePath(filepath.Join(path, name), path, true, 0); e != nil {
			return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
		}
	}
	if _, e = privatePath(filepath.Join(path, lockFile), path, false, 0); e != nil {
		return storeNamespace{}, failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	return marker, nil
}

func readNamespaceFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maximumNamespaceBytes+1))
}
