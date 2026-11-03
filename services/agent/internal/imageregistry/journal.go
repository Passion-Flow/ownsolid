package imageregistry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

var registryID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func privateMetadata(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}

func readPrivateJSON(root *os.Root, name string, maximum int64, target any) error {
	bad := errors.New("REGISTRY_METADATA_INVALID")
	if maximum < 1 || filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return bad
	}
	info, e := root.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || !privateMetadata(info) || info.Size() < 1 || info.Size() > maximum {
		return bad
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return bad
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) || !privateMetadata(opened) {
		return bad
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	current, statErr := root.Lstat(name)
	if e != nil || statErr != nil || !os.SameFile(opened, current) || !privateMetadata(current) || int64(len(raw)) > maximum || release.StrictJSON(raw, target) != nil {
		return bad
	}
	return nil
}

func privateJob(root *os.Root, job string) error {
	info, e := root.Lstat(job)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !privateMetadata(info) {
		return errors.New("REGISTRY_STORE_INVALID")
	}
	return nil
}

func createPrivateJob(root *os.Root, job string) error {
	if e := root.Mkdir(job, 0700); e != nil {
		return e
	}
	directory, e := root.Open(".")
	if e != nil {
		return e
	}
	defer directory.Close()
	return directory.Sync()
}

// ReadOfflineManifest reads only an explicitly selected private local artifact.
// Structural validity is not signature approval; the real loader verifies proof.
func ReadOfflineManifest(c Config, filename string) (wire.RegistryOfflineManifest, error) {
	var m wire.RegistryOfflineManifest
	if c.Validate() != nil || !filepath.IsAbs(filename) || filepath.Clean(filename) != filename || state.Directory(filepath.Dir(filename)) != nil {
		return m, errors.New("REGISTRY_METADATA_INVALID")
	}
	root, e := os.OpenRoot(filepath.Dir(filename))
	if e != nil {
		return m, errors.New("REGISTRY_METADATA_INVALID")
	}
	defer root.Close()
	if readPrivateJSON(root, filepath.Base(filename), c.Verification.MaximumMetadataBytes, &m) != nil || m.Validate() != nil {
		return wire.RegistryOfflineManifest{}, errors.New("REGISTRY_METADATA_INVALID")
	}
	return m, nil
}
