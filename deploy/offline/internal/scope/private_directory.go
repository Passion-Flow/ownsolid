package scope

import (
	"os"
	"path/filepath"
)

func openPrivateDirectory(name string) (*os.Root, error) {
	before, e := os.Lstat(name)
	if e != nil || !canonical(name) || !samePrivateDirectory(before, before) {
		return nil, failure("OFFLINE_SCOPE_PRIVATE_DIRECTORY_REQUIRED")
	}
	return openPinnedPrivateDirectory(name, before)
}

// Keep the terminal dot so a replacement FIFO is never opened as a stream.
// A captured observation is passed explicitly; tests need no mutation hooks.
func openPinnedPrivateDirectory(name string, before os.FileInfo) (*os.Root, error) {
	if !canonical(name) || !samePrivateDirectory(before, before) {
		return nil, failure("OFFLINE_SCOPE_DIRECTORY_CHANGED")
	}
	root, e := os.OpenRoot(name + string(filepath.Separator) + ".")
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_DIRECTORY_CHANGED")
	}
	opened, oe := root.Stat(".")
	current, ce := os.Lstat(name)
	if oe != nil || ce != nil || !canonical(name) || !samePrivateDirectory(before, opened) || !samePrivateDirectory(before, current) {
		root.Close()
		return nil, failure("OFFLINE_SCOPE_DIRECTORY_CHANGED")
	}
	return root, nil
}
