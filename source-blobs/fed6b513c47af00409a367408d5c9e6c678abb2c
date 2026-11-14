package components

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func rootOwned(i os.FileInfo) bool { s, ok := i.Sys().(*syscall.Stat_t); return ok && s.Uid == 0 }
func secureDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("COMPONENT_STATE_INVALID")
	}
	for current := path; ; current = filepath.Dir(current) {
		i, e := os.Lstat(current)
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 || !rootOwned(i) {
			return errors.New("COMPONENT_CONFLICT")
		}
		if current == "/" {
			break
		}
	}
	return nil
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func writeNew(path string, b []byte, mode os.FileMode) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = syncDirectory(filepath.Dir(path))
	}
	return e
}
func replaceJSON(path string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".component-journal-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
func hashFile(path string) (string, error) {
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Size() > 128*1024*1024 || i.Mode().Perm()&0022 != 0 || !rootOwned(i) {
		return "", errors.New("COMPONENT_CONFLICT")
	}
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(i, actual) {
		return "", errors.New("COMPONENT_CONFLICT")
	}
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, 128*1024*1024+1)); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func extractArchive(archive, destination string) (map[string]string, error) {
	return extractArchiveVersion(archive, destination, wire.NodeExporterVersion)
}
func extractArchiveVersion(archive, destination, version string) (map[string]string, error) {
	recipe, approved := wire.RecipeForVersion(version)
	if !approved {
		return nil, errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	f, e := os.Open(archive)
	if e != nil {
		return nil, errors.New("COMPONENT_ASSET_UNAVAILABLE")
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, recipe.ArchiveBytes+1))
	if e != nil || n != recipe.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != recipe.ArchiveSHA256 {
		return nil, errors.New("COMPONENT_ASSET_INVALID")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	g, e := gzip.NewReader(f)
	if e != nil {
		return nil, errors.New("COMPONENT_ARCHIVE_INVALID")
	}
	defer g.Close()
	g.Multistream(false)
	t := tar.NewReader(io.LimitReader(g, 140*1024*1024))
	prefix := "node_exporter-" + version + ".linux-amd64/"
	expected := map[string]bool{"node_exporter": true, "LICENSE": true, "NOTICE": true}
	result := map[string]string{}
	for {
		header, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errors.New("COMPONENT_ARCHIVE_INVALID")
		}
		if header.Typeflag == tar.TypeDir && header.Name == prefix {
			continue
		}
		name := filepath.Base(header.Name)
		if !expected[name] || header.Name != prefix+name || result[name] != "" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 128*1024*1024 || header.Mode&07000 != 0 {
			return nil, errors.New("COMPONENT_ARCHIVE_INVALID")
		}
		path := filepath.Join(destination, name)
		out, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return nil, errors.New("COMPONENT_IO_FAILED")
		}
		hash := sha256.New()
		written, e := io.CopyN(io.MultiWriter(out, hash), t, header.Size)
		if e == nil && written == header.Size {
			e = out.Sync()
		}
		ce := out.Close()
		if e != nil || ce != nil {
			return nil, errors.New("COMPONENT_IO_FAILED")
		}
		mode := os.FileMode(0644)
		if name == "node_exporter" {
			mode = 0755
		}
		if os.Chmod(path, mode) != nil {
			return nil, errors.New("COMPONENT_IO_FAILED")
		}
		result[name] = hex.EncodeToString(hash.Sum(nil))
	}
	if len(result) != 3 {
		return nil, errors.New("COMPONENT_ARCHIVE_INVALID")
	}
	b, e := elf.Open(filepath.Join(destination, "node_exporter"))
	if e != nil {
		return nil, errors.New("COMPONENT_ARCHITECTURE_MISMATCH")
	}
	defer b.Close()
	if b.Class != elf.ELFCLASS64 || b.Machine != elf.EM_X86_64 {
		return nil, errors.New("COMPONENT_ARCHITECTURE_MISMATCH")
	}
	return result, syncDirectory(destination)
}
