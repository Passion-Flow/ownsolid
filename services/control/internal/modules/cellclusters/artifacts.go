package cellclusters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// artifactStore serves the approved RKE2 files from the directory the offline bundle
// filled. Each file is hashed whole once, and again whenever it changes, before any range
// of it is served: a corrupt archive is refused at control instead of failing every join.
type artifactStore struct {
	directory string
	mu        sync.Mutex
	verified  map[string]artifactStamp
}

// artifactStamp identifies the version of a file that was hashed.
type artifactStamp struct {
	size     int64
	modified time.Time
}

func newArtifactStore(directory string) *artifactStore {
	return &artifactStore{directory: directory, verified: map[string]artifactStamp{}}
}

// contextReader stops a long read when its request ends.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if e := c.ctx.Err(); e != nil {
		return 0, e
	}
	return c.r.Read(p)
}

// open opens an approved artifact through a root that refuses links out of the directory,
// and checks it is the regular, unshared, unwritable-by-others file of the approved size.
func (s *artifactStore) open(artifact wire.KubernetesArtifact) (*os.File, fs.FileInfo, error) {
	root, e := os.OpenRoot(s.directory)
	if e != nil {
		return nil, nil, errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	defer root.Close()
	info, e := root.Lstat(artifact.Name)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() != artifact.SizeBytes {
		return nil, nil, errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	file, e := root.Open(artifact.Name)
	if e != nil {
		return nil, nil, errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	current, e := file.Stat()
	if e != nil || !os.SameFile(info, current) {
		file.Close()
		return nil, nil, errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	return file, current, nil
}

// verify hashes the whole file unless this version of it was already verified.
func (s *artifactStore) verify(ctx context.Context, artifact wire.KubernetesArtifact, file *os.File, info fs.FileInfo) error {
	stamp := artifactStamp{size: info.Size(), modified: info.ModTime()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verified[artifact.Name] == stamp {
		return nil
	}
	hash := sha256.New()
	if _, e := io.Copy(hash, contextReader{ctx, io.NewSectionReader(file, 0, artifact.SizeBytes)}); e != nil {
		return errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		delete(s.verified, artifact.Name)
		return errors.New("KUBERNETES_ARTIFACT_INVALID")
	}
	s.verified[artifact.Name] = stamp
	return nil
}

// Range returns one requested range of an approved artifact with its SHA-256. The caller
// closes the download's file.
func (s *artifactStore) Range(ctx context.Context, input wire.KubernetesArtifactRequest) (httpserver.Download, error) {
	if e := input.Validate(); e != nil {
		return httpserver.Download{}, e
	}
	artifact, _ := wire.KubernetesArtifactForName(input.Name)
	return s.rangeOf(ctx, artifact, input.Offset, input.Length)
}

// rangeOf reads length bytes at offset of a verified artifact.
func (s *artifactStore) rangeOf(ctx context.Context, artifact wire.KubernetesArtifact, offset, length int64) (httpserver.Download, error) {
	file, info, e := s.open(artifact)
	if e != nil {
		return httpserver.Download{}, e
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	if e = s.verify(ctx, artifact, file, info); e != nil {
		return httpserver.Download{}, e
	}
	hash := sha256.New()
	count, e := io.Copy(hash, contextReader{ctx, io.NewSectionReader(file, offset, length)})
	if e != nil || count != length {
		return httpserver.Download{}, errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	// A file replaced while it was read would pair a stale hash with new bytes.
	after, e := file.Stat()
	if e != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return httpserver.Download{}, errors.New("KUBERNETES_ARTIFACT_UNAVAILABLE")
	}
	keep = true
	return httpserver.Download{File: file, Size: length, Offset: offset, SHA256: hex.EncodeToString(hash.Sum(nil)), FileName: artifact.Name, ContentType: "application/octet-stream"}, nil
}
