package datasets

import (
	"io"
	"os"
	"path/filepath"
	"syscall"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const maximumInitializationNamespaceBytes int64 = 4096

// VerifyInitializedStore inspects only a fresh empty namespace with the explicitly requested runtime owner.
// A root initializer need not impersonate the runtime UID or open its long-lived private store.
func VerifyInitializedStore(path, tenant string, uid, gid int) (string, error) {
	if !datawire.ValidID(tenant) || uid < 0 || gid < 0 || int64(uid) > 2147483647 || int64(gid) > 2147483647 || uid != os.Geteuid() && os.Geteuid() != 0 {
		return "", failure("DATASET_STORAGE_PATH_INVALID")
	}
	check := func(path string, directory bool, maximum int64) (os.FileInfo, error) {
		actual, e := filepath.EvalSymlinks(path)
		if e != nil || actual != path || !filepath.IsAbs(path) {
			return nil, failure("DATASET_STORAGE_PATH_INVALID")
		}
		info, e := os.Lstat(path)
		if e != nil {
			return nil, failure("DATASET_STORAGE_PATH_INVALID")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(uid) || owner.Gid != uint32(gid) || directory && (!info.IsDir() || info.Mode().Perm() != 0700) || !directory && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600 || owner.Nlink != 1 || info.Size() > maximum) {
			return nil, failure("DATASET_STORAGE_PATH_INVALID")
		}
		return info, nil
	}
	if _, e := check(path, true, 0); e != nil {
		return "", e
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer root.Close()
	dir, e := root.Open(".")
	if e != nil {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	entries, e := dir.ReadDir(6)
	dir.Close()
	if e != nil && e != io.EOF || len(entries) != 5 {
		return "", failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	allowed := map[string]bool{markerFile: true, lockFile: true, "objects": true, "attempts": true, "versions": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return "", failure("DATASET_STORE_NAMESPACE_INVALID")
		}
	}
	for _, name := range []string{"objects", "attempts", "versions"} {
		if _, e = check(filepath.Join(path, name), true, 0); e != nil {
			return "", e
		}
		file, e := root.Open(name)
		if e != nil {
			return "", failure("DATASET_STORAGE_UNAVAILABLE")
		}
		children, readError := file.ReadDir(1)
		file.Close()
		if len(children) > 0 || readError != nil && readError != io.EOF {
			return "", failure("DATASET_STORE_NOT_EMPTY")
		}
	}
	if _, e = check(filepath.Join(path, lockFile), false, 0); e != nil {
		return "", e
	}
	info, e := check(filepath.Join(path, markerFile), false, maximumInitializationNamespaceBytes)
	if e != nil {
		return "", e
	}
	file, e := root.Open(markerFile)
	if e != nil {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return "", failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximumInitializationNamespaceBytes+1))
	var namespace storeNamespace
	if e != nil || datawire.DecodeStrict(raw, &namespace) != nil || namespace.SchemaVersion != 1 || namespace.TenantID != tenant || !datawire.ValidID(namespace.StoreID) {
		return "", failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	return namespace.StoreID, nil
}
