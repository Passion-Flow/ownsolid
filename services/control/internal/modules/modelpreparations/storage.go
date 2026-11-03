package modelpreparations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const namespaceFile = "namespace.json"
const ownerLock = "owner.lock"
const uploadIndex = "upload.json"
const maximumNamespaceBytes int64 = 4096

type storeNamespace struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	StoreID       string `json:"store_id"`
	TenantID      string `json:"tenant_id"`
}
type Store struct {
	root         *os.Root
	lock         *os.File
	path         string
	namespace    storeNamespace
	minimumFree  int64
	maximumBytes int64
	maximumFiles int64
	mu           sync.Mutex
	closed       bool
}

func ownedPath(path string, directory bool, maximum int64) (os.FileInfo, error) {
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil || info.Mode().Perm() != 0600 && !directory || directory && (!info.IsDir() || info.Mode().Perm() != 0700) || !directory && (!info.Mode().IsRegular() || info.Size() > maximum) {
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || !directory && owner.Nlink != 1 {
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	return info, nil
}

// InitializeStore adopts only an existing selected empty directory; missing mount roots and populated data are never created/replaced.
func InitializeStore(path, tenant string, uid, gid int) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !datawire.ValidID(tenant) || uid < 0 || gid < 0 || int64(uid) > 2147483647 || int64(gid) > 2147483647 {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() && !(os.Geteuid() == 0 && int(owner.Uid) == uid) || os.Geteuid() != 0 && (uid != os.Geteuid() || gid != os.Getegid()) {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	directory, e := os.Open(path)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer directory.Close()
	current, e := directory.Stat()
	if e != nil || !os.SameFile(info, current) {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer root.Close()
	pinned, e := root.Stat(".")
	if e != nil || !os.SameFile(info, pinned) {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	entries, e := directory.ReadDir(1)
	if len(entries) > 0 {
		return fail("MODEL_PREPARATION_STORE_NOT_EMPTY")
	}
	if e != nil && e != io.EOF {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	if directory.Chmod(0700) != nil || directory.Chown(uid, gid) != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	for _, name := range []string{"uploads", "artifacts"} {
		if root.Mkdir(name, 0700) != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		child, e := root.Open(name)
		if e != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		e = child.Chown(uid, gid)
		if e == nil {
			e = child.Sync()
		}
		closeError := child.Close()
		if e != nil || closeError != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
	}
	namespace := storeNamespace{SchemaVersion: 1, Kind: "model_preparation", StoreID: uuid.NewString(), TenantID: tenant}
	raw, _ := json.Marshal(namespace)
	for name, content := range map[string][]byte{namespaceFile: raw, ownerLock: {}} {
		file, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		e = file.Chown(uid, gid)
		if e == nil {
			_, e = file.Write(content)
		}
		if e == nil {
			e = file.Sync()
		}
		closeError := file.Close()
		if e != nil || closeError != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
	}
	if directory.Sync() != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	current, e = os.Lstat(path)
	if e != nil || !os.SameFile(info, current) {
		return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	return nil
}

func OpenStore(path, tenant string, minimumFree, maximumBytes, maximumFiles int64) (*Store, error) {
	return openStore(path, tenant, minimumFree, maximumBytes, maximumFiles, false)
}

// OpenStoreForVerification uses a read-only existing ownership-lock descriptor
// with the same exclusive lock/namespace checks. It never initializes missing
// state; the caller supplies readonly mounts rather than a mutation sandbox.
func OpenStoreForVerification(path, tenant string, minimumFree, maximumBytes, maximumFiles int64) (*Store, error) {
	return openStore(path, tenant, minimumFree, maximumBytes, maximumFiles, true)
}

func openStore(path, tenant string, minimumFree, maximumBytes, maximumFiles int64, verification bool) (*Store, error) {
	if !datawire.ValidID(tenant) || minimumFree < 4096 || maximumBytes < 1 || maximumBytes > wire.MaximumModelBytes || maximumFiles < 1 || maximumFiles > wire.MaximumModelFiles {
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	if _, e := ownedPath(path, true, 0); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(path)
	if e != nil || len(entries) != 4 {
		return nil, fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	allowed := map[string]bool{namespaceFile: true, ownerLock: true, "uploads": true, "artifacts": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return nil, fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
		}
	}
	for _, name := range []string{"uploads", "artifacts"} {
		if _, e := ownedPath(filepath.Join(path, name), true, 0); e != nil {
			return nil, e
		}
	}
	if _, e = ownedPath(filepath.Join(path, namespaceFile), false, maximumNamespaceBytes); e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(filepath.Join(path, namespaceFile))
	var namespace storeNamespace
	if e != nil || wire.DecodeStrict(raw, &namespace) != nil || namespace.SchemaVersion != 1 || namespace.Kind != "model_preparation" || namespace.TenantID != tenant || !datawire.ValidID(namespace.StoreID) {
		return nil, fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	if _, e = ownedPath(filepath.Join(path, ownerLock), false, 0); e != nil {
		return nil, e
	}
	lockMode := os.O_RDWR
	if verification {
		lockMode = os.O_RDONLY
	}
	lock, e := os.OpenFile(filepath.Join(path, ownerLock), lockMode, 0)
	if e != nil {
		return nil, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		return nil, fail("MODEL_PREPARATION_STORE_IN_USE")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		lock.Close()
		return nil, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	value := &Store{root: root, lock: lock, path: path, namespace: namespace, minimumFree: minimumFree, maximumBytes: maximumBytes, maximumFiles: maximumFiles}
	if e := value.checkBase(); e != nil {
		value.Close()
		return nil, e
	}
	return value, nil
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
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	return e
}
func (s *Store) Namespace() string { return s.namespace.StoreID }

func (s *Store) checkBase() error {
	if s.closed {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	info, e := ownedPath(s.path, true, 0)
	if e != nil {
		return e
	}
	opened, e := s.root.Stat(".")
	if e != nil || !os.SameFile(info, opened) {
		return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	lockInfo, e := ownedPath(filepath.Join(s.path, ownerLock), false, 0)
	if e != nil {
		return e
	}
	held, e := s.lock.Stat()
	if e != nil || !os.SameFile(lockInfo, held) {
		return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	if _, e = ownedPath(filepath.Join(s.path, namespaceFile), false, maximumNamespaceBytes); e != nil {
		return e
	}
	raw, e := s.root.ReadFile(namespaceFile)
	var marker storeNamespace
	if e != nil || wire.DecodeStrict(raw, &marker) != nil || marker != s.namespace {
		return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	entries, e := os.ReadDir(s.path)
	if e != nil || len(entries) != 4 {
		return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	allowed := map[string]bool{namespaceFile: true, ownerLock: true, "uploads": true, "artifacts": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
		}
	}
	for _, name := range []string{"uploads", "artifacts"} {
		if _, e := ownedPath(filepath.Join(s.path, name), true, 0); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) reserve(bytes int64) error {
	var info syscall.Statfs_t
	if bytes < 0 || syscall.Statfs(s.path, &info) != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	free := uint64(info.Bavail) * uint64(info.Bsize)
	if free < uint64(s.minimumFree)+uint64(bytes) {
		return fail("MODEL_PREPARATION_STORAGE_FULL")
	}
	return nil
}
func (s *Store) sync(relative string) error {
	file, e := s.root.Open(relative)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	if file.Sync() != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	return nil
}

func (s *Store) readDirectory(relative string, maximum int) ([]os.DirEntry, error) {
	info, e := ownedPath(filepath.Join(s.path, relative), true, 0)
	if e != nil {
		return nil, e
	}
	file, e := s.root.Open(relative)
	if e != nil {
		return nil, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	current, e := file.Stat()
	if e != nil || !os.SameFile(info, current) {
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	entries, e := file.ReadDir(maximum + 1)
	if e != nil && e != io.EOF {
		return nil, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	if len(entries) > maximum {
		return nil, fail("MODEL_PREPARATION_STORAGE_CONFLICT")
	}
	return entries, nil
}
func (s *Store) directory(relative string) error {
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.HasPrefix(relative, "..") {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
		}
	}
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		if _, e := ownedPath(filepath.Join(s.path, parent), true, 0); e != nil {
			return e
		}
	}
	if _, e := s.root.Lstat(relative); os.IsNotExist(e) {
		if e = s.root.Mkdir(relative, 0700); e != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		if e = s.sync(filepath.Dir(relative)); e != nil {
			return e
		}
	} else if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	_, e := ownedPath(filepath.Join(s.path, relative), true, 0)
	return e
}
func (s *Store) newJSON(relative string, value any) error {
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		if _, e := ownedPath(filepath.Join(s.path, parent), true, 0); e != nil {
			return e
		}
	}
	raw, e := json.Marshal(value)
	if e != nil || int64(len(raw)) > wire.MaximumResultBytes {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	file, e := s.root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_CONFLICT")
	}
	_, e = file.Write(raw)
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	return s.sync(filepath.Dir(relative))
}
func (s *Store) checkedFile(relative string, maximum int64) (*os.File, error) {
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.HasPrefix(relative, "..") {
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		if _, e := ownedPath(filepath.Join(s.path, parent), true, 0); e != nil {
			return nil, e
		}
	}
	info, e := ownedPath(filepath.Join(s.path, relative), false, maximum)
	if e != nil {
		return nil, e
	}
	file, e := s.root.Open(relative)
	if e != nil {
		return nil, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	current, e := file.Stat()
	if e != nil || !os.SameFile(info, current) {
		file.Close()
		return nil, fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	return file, nil
}
func fileDigest(ctx context.Context, file *os.File, maximum int64) (string, int64, error) {
	hash := sha256.New()
	reader := contextReader{ctx: ctx, reader: io.NewSectionReader(file, 0, maximum+1)}
	n, e := io.Copy(hash, reader)
	if e := ctx.Err(); e != nil {
		return "", n, e
	}
	if e != nil || n > maximum {
		return "", n, fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(buffer)
}

func chunkName(offset int64) string { return fmt.Sprintf("%016x", offset) }

// VerifyNamespace checks the currently pinned private root, owner lock and immutable tenant marker.
func (s *Store) VerifyNamespace() error { s.mu.Lock(); defer s.mu.Unlock(); return s.checkBase() }
