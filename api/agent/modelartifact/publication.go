package modelartifact

import (
	"errors"
	"os"
	"path/filepath"
)

// PublishDirectory atomically publishes a verified directory without replacing
// even an empty directory or a symlink at the destination. Callers own content
// verification, durable intent, parent fsync and metadata reconciliation.
func PublishDirectory(root *os.Root, source, destination string) error {
	if root == nil || !filepath.IsLocal(source) || !filepath.IsLocal(destination) || filepath.Clean(source) != source || filepath.Clean(destination) != destination || source == "." || destination == "." || source == destination {
		return errors.New("MODEL_PUBLICATION_PATH_INVALID")
	}
	info, e := root.Lstat(source)
	if e != nil {
		return e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("MODEL_PUBLICATION_PATH_INVALID")
	}
	from, e := root.Open(filepath.Dir(source))
	if e != nil {
		return e
	}
	defer from.Close()
	to, e := root.Open(filepath.Dir(destination))
	if e != nil {
		return e
	}
	defer to.Close()
	return publishDirectory(int(from.Fd()), filepath.Base(source), int(to.Fd()), filepath.Base(destination))
}
