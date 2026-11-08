package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// binaryFiles is every regular file the RKE2 binary tarball may hold, with the mode it is
// installed with under /usr/local. Anything else in the tarball refuses the install, so a
// substituted archive cannot place a file anywhere else.
var binaryFiles = map[string]os.FileMode{
	"bin/rke2":                               0o755,
	"bin/rke2-killall.sh":                    0o755,
	"bin/rke2-uninstall.sh":                  0o755,
	"lib/systemd/system/rke2-server.service": 0o644,
	"lib/systemd/system/rke2-server.env":     0o644,
	"lib/systemd/system/rke2-agent.service":  0o644,
	"lib/systemd/system/rke2-agent.env":      0o644,
	"share/rke2/rke2-cis-sysctl.conf":        0o644,
	"share/rke2/LICENSE.txt":                 0o644,
}

// maximumBinaryFileBytes bounds one file unpacked from the tarball; the rke2 binary is
// about 130 MB.
const maximumBinaryFileBytes = 512 << 20

// verifyArtifact checks a staged file against its pinned size and SHA-256.
func verifyArtifact(path string, artifact wire.KubernetesArtifact) error {
	file, e := os.Open(path)
	if e != nil {
		return errors.New("KUBERNETES_ASSET_UNAVAILABLE")
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != artifact.SizeBytes {
		return errors.New("KUBERNETES_ASSET_INVALID")
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, file); e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return errors.New("KUBERNETES_ASSET_INVALID")
	}
	return nil
}

// extractBinary unpacks the allowed files of the binary tarball into destination, each
// written to a temporary name and renamed into place.
func extractBinary(archive, destination string) error {
	file, e := os.Open(archive)
	if e != nil {
		return errors.New("KUBERNETES_ASSET_UNAVAILABLE")
	}
	defer file.Close()
	compressed, e := gzip.NewReader(file)
	if e != nil {
		return errors.New("KUBERNETES_ARCHIVE_INVALID")
	}
	reader := tar.NewReader(compressed)
	seen := map[string]bool{}
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.New("KUBERNETES_ARCHIVE_INVALID")
		}
		name := filepath.Clean(header.Name)
		if header.Typeflag == tar.TypeDir {
			continue
		}
		mode, allowed := binaryFiles[name]
		if !allowed || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maximumBinaryFileBytes || seen[name] {
			return errors.New("KUBERNETES_ARCHIVE_INVALID")
		}
		seen[name] = true
		if e = writeFile(filepath.Join(destination, name), io.LimitReader(reader, header.Size), header.Size, mode); e != nil {
			return e
		}
	}
	if len(seen) != len(binaryFiles) {
		return errors.New("KUBERNETES_ARCHIVE_INVALID")
	}
	return nil
}

// copyFile places a verified image archive where RKE2 imports it at start.
func copyFile(source, destination string, size int64) error {
	file, e := os.Open(source)
	if e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	defer file.Close()
	return writeFile(destination, file, size, 0o600)
}

// writeFile writes exactly size bytes to a temporary file beside path, syncs it and
// renames it over path, so a crash leaves either the old file or the whole new one.
func writeFile(path string, content io.Reader, size int64, mode os.FileMode) error {
	failed := errors.New("KUBERNETES_IO_FAILED")
	if e := os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
		return failed
	}
	suffix := make([]byte, 8)
	if _, e := rand.Read(suffix); e != nil {
		return failed
	}
	temporary := path + ".ownsolid-" + hex.EncodeToString(suffix)
	file, e := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return failed
	}
	written, e := io.Copy(file, content)
	if e == nil && written != size {
		e = errors.New("short")
	}
	if e == nil {
		e = file.Chmod(mode)
	}
	if e == nil {
		e = file.Sync()
	}
	if closed := file.Close(); e == nil {
		e = closed
	}
	if e == nil {
		e = os.Rename(temporary, path)
	}
	if e != nil {
		os.Remove(temporary)
		return failed
	}
	return nil
}

// writeJSON writes a value as a file only root can read.
func writeJSON(path string, value any) error {
	content, e := json.Marshal(value)
	if e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	return writeBytes(path, content)
}

func writeBytes(path string, content []byte) error {
	return writeFile(path, bytes.NewReader(content), int64(len(content)), 0o600)
}
