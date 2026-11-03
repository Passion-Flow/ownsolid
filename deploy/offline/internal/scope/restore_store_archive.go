package scope

import (
	"os"
	"path/filepath"
	"syscall"
)

// Private directory admission deliberately retains a terminal dot. os.Root
// preserves that spelling in File.Name, while the Engine archive boundary
// requires a canonical absolute name. Open both through the pinned directory
// and by canonical name, and require the same directory and file identities.
// The Engine still owns complete mode/owner/link/size/hash validation.
func openRestoreStoreArchive(root *os.Root, role string) (*os.File, error) {
	allowed := false
	for _, kind := range restoredStoreCatalog() {
		allowed = allowed || kind.role == role
	}
	if root == nil || !allowed {
		return nil, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_PROFILE_REQUIRED")
	}
	directory := filepath.Clean(root.Name())
	pinned, e := root.Stat(".")
	named, ne := os.Lstat(directory)
	if e != nil || ne != nil || !canonical(directory) || !samePrivateDirectory(pinned, named) {
		return nil, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_DIRECTORY_CHANGED")
	}
	name := role + ".tar"
	anchored, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_UNAVAILABLE")
	}
	defer anchored.Close()
	file, e := os.OpenFile(filepath.Join(directory, name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_UNAVAILABLE")
	}
	a, ae := anchored.Stat()
	b, be := file.Stat()
	after, de := os.Lstat(directory)
	if ae != nil || be != nil || de != nil || !os.SameFile(a, b) || !samePrivateDirectory(pinned, after) {
		file.Close()
		return nil, failure("OFFLINE_SCOPE_RESTORE_STORES_ARCHIVE_CHANGED")
	}
	return file, nil
}
