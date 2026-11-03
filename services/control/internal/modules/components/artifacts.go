package components

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"reflect"
)

func (s *Service) OpenArtifact() (*os.File, error) {
	file, _, e := s.OpenArtifactVersion(wire.NodeExporterVersion)
	return file, e
}
func (s *Service) OpenArtifactVersion(version string) (*os.File, wire.Recipe, error) {
	return s.OpenArtifactVersionContext(context.Background(), version)
}
func (s *Service) artifactPath(version string) string {
	if s.CatalogDirectory != "" {
		return filepath.Join(s.CatalogDirectory, version, "linux-amd64", "node_exporter-"+version+".linux-amd64.tar.gz")
	}
	if version == wire.NodeExporterVersion {
		return s.ArchiveFile
	}
	return ""
}
func (s *Service) OpenArtifactVersionContext(ctx context.Context, version string) (*os.File, wire.Recipe, error) {
	recipe, ok := wire.RecipeForVersion(version)
	if !ok {
		return nil, recipe, errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	path := s.artifactPath(version)
	info, e := os.Lstat(path)
	if e != nil {
		return nil, recipe, errors.New("COMPONENT_ASSET_UNAVAILABLE")
	}
	if !info.Mode().IsRegular() || info.Size() != recipe.ArchiveBytes || info.Mode().Perm()&0022 != 0 {
		return nil, recipe, errors.New("COMPONENT_ASSET_INVALID")
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, recipe, errors.New("COMPONENT_ASSET_UNAVAILABLE")
	}
	opened, e := file.Stat()
	if e != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, recipe, errors.New("COMPONENT_ASSET_INVALID")
	}
	hash := sha256.New()
	count, e := io.Copy(hash, io.LimitReader(componentContextReader{ctx, file}, recipe.ArchiveBytes+1))
	if e != nil || count != recipe.ArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != recipe.ArchiveSHA256 {
		if ctx != nil && ctx.Err() != nil {
			file.Close()
			return nil, recipe, componentError(ctx.Err())
		}
		file.Close()
		return nil, recipe, errors.New("COMPONENT_ASSET_INVALID")
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		file.Close()
		return nil, recipe, errors.New("COMPONENT_ASSET_INVALID")
	}
	if e = s.checkArtifactDescriptor(file, opened, version); e != nil {
		file.Close()
		return nil, recipe, e
	}
	return file, recipe, nil
}
func (s *Service) TaskArtifact(ctx context.Context, tx pgx.Tx, n nodes.Node, t nodes.Task) (httpserver.Download, error) {
	command, e := s.Nodes.ReadComponentCommand(ctx, tx, n, t)
	if e != nil {
		return httpserver.Download{}, e
	}
	if command.Action != "install" && command.Action != "upgrade" {
		return httpserver.Download{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	file, recipe, e := s.OpenArtifactVersion(command.Version)
	if e != nil {
		return httpserver.Download{}, e
	}
	return httpserver.Download{File: file, Size: recipe.ArchiveBytes, SHA256: recipe.ArchiveSHA256}, nil
}

type componentContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r componentContextReader) Read(b []byte) (int, error) {
	if r.ctx == nil {
		return 0, errors.New("VALIDATION_FAILED")
	}
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(b)
}
func (s *Service) checkArtifactDescriptor(file *os.File, verified os.FileInfo, version string) error {
	if file == nil || verified == nil {
		return errors.New("COMPONENT_ASSET_INVALID")
	}
	now, e := file.Stat()
	if e != nil {
		return errors.New("COMPONENT_ASSET_INVALID")
	}
	path, e := os.Lstat(s.artifactPath(version))
	if e != nil || !now.Mode().IsRegular() || now.Mode().Perm()&0022 != 0 || !os.SameFile(verified, now) || !os.SameFile(now, path) || verified.Size() != now.Size() || verified.ModTime() != now.ModTime() || verified.Mode() != now.Mode() {
		return errors.New("COMPONENT_ASSET_INVALID")
	}
	return nil
}

// DownloadArtifact owns both authority commits around the lock-free bounded
// hash. A failed final fence/commit closes the descriptor before any projection.
func (s *Service) DownloadArtifact(ctx context.Context, id, credential, taskID, lease string) (httpserver.Download, error) {
	var command wire.ComponentCommand
	if _, e := s.repository(); e != nil {
		return httpserver.Download{}, e
	}
	e := s.Nodes.WithLeaseExecutor(ctx, id, credential, taskID, lease, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task) error {
		var e error
		command, e = s.Nodes.ReadComponentCommandWithin(ctx, q, n, t)
		if e != nil {
			return e
		}
		if command.Action != "install" && command.Action != "upgrade" {
			return errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		return nil
	})
	if e != nil {
		return httpserver.Download{}, componentError(e)
	}
	file, recipe, e := s.OpenArtifactVersionContext(ctx, command.Version)
	if e != nil {
		return httpserver.Download{}, componentError(e)
	}
	verified, e := file.Stat()
	if e != nil {
		file.Close()
		return httpserver.Download{}, errors.New("COMPONENT_ASSET_INVALID")
	}
	e = s.Nodes.WithLeaseExecutor(ctx, id, credential, taskID, lease, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task) error {
		current, e := s.Nodes.ReadComponentCommandWithin(ctx, q, n, t)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(current, command) {
			return errors.New("AGENT_LEASE_STALE")
		}
		return s.checkArtifactDescriptor(file, verified, command.Version)
	})
	if e != nil {
		file.Close()
		return httpserver.Download{}, componentError(e)
	}
	return httpserver.Download{File: file, Size: recipe.ArchiveBytes, SHA256: recipe.ArchiveSHA256}, nil
}
