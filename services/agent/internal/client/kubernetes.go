package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"regexp"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

var kubernetesTaskPath = regexp.MustCompile(`^/agent/v1/nodes/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/tasks/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/` + wire.KubernetesArtifactsPath + `$`)

// KubernetesArtifact downloads one approved RKE2 artifact to destination in bounded ranges
// (ADR 0051). It writes to destination.partial first and resumes from the whole ranges a
// previous attempt left there, so an interrupted 500 MB download is not started over. The
// file is renamed into place only once its size and SHA-256 match the approved release.
func (c *Client) KubernetesArtifact(ctx context.Context, path, lease string, artifact wire.KubernetesArtifact, destination string) error {
	approved, ok := wire.KubernetesArtifactForName(artifact.Name)
	if !ok || approved != artifact || !kubernetesTaskPath.MatchString(path) {
		return errors.New("KUBERNETES_ASSET_INVALID")
	}
	return c.kubernetesRanges(ctx, path, lease, artifact, destination)
}

// kubernetesRanges fetches the artifact's ranges; the caller has checked it is approved.
func (c *Client) kubernetesRanges(ctx context.Context, path, lease string, artifact wire.KubernetesArtifact, destination string) error {
	partial := destination + ".partial"
	file, e := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0o600)
	if e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	defer file.Close()
	digest, offset, e := resume(file)
	if e != nil {
		return e
	}
	for offset < artifact.SizeBytes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		input := wire.KubernetesArtifactRequest{LeaseToken: lease, Name: artifact.Name, Offset: offset, Length: min(wire.KubernetesTransferChunkBytes, artifact.SizeBytes-offset)}
		content, e := c.rangeWithRetry(ctx, path, input, input.Length)
		if e != nil {
			return e
		}
		if _, e = file.Write(content); e != nil {
			return errors.New("KUBERNETES_IO_FAILED")
		}
		digest.Write(content)
		offset += int64(len(content))
	}
	if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		file.Close()
		os.Remove(partial)
		return errors.New("KUBERNETES_ASSET_INVALID")
	}
	if e = file.Sync(); e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	if e = os.Rename(partial, destination); e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	return nil
}

// resume keeps the whole ranges of a partial file, drops a torn last range, and returns the
// running digest and the offset to continue from.
func resume(file *os.File) (hash.Hash, int64, error) {
	failed := errors.New("KUBERNETES_IO_FAILED")
	info, e := file.Stat()
	if e != nil {
		return nil, 0, failed
	}
	kept := info.Size() - info.Size()%wire.KubernetesTransferChunkBytes
	if e = file.Truncate(kept); e != nil {
		return nil, 0, failed
	}
	digest := sha256.New()
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return nil, 0, failed
	}
	if _, e = io.CopyN(digest, file, kept); e != nil {
		return nil, 0, failed
	}
	return digest, kept, nil
}
