package artifactstores

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

const snapshotSchema = 1
const maximumSnapshotManifestBytes = 16384
const snapshotBlockBytes = 512

type SnapshotManifest struct {
	Schema        int    `json:"schema"`
	TenantID      string `json:"tenant_id"`
	WorkspaceID   string `json:"workspace_id"`
	ObjectID      string `json:"object_id"`
	NamespaceID   string `json:"namespace_id"`
	BackendSHA256 string `json:"backend_sha256"`
	Purpose       string `json:"purpose"`
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"size_bytes"`
}
type countWriter struct {
	writer io.Writer
	count  int64
}

func (w *countWriter) Write(b []byte) (int, error) {
	n, e := w.writer.Write(b)
	w.count += int64(n)
	return n, e
}
func writeSnapshot(w io.Writer, manifest []byte, source io.ReadSeeker, size int64) error {
	if _, e := source.Seek(0, io.SeekStart); e != nil {
		return e
	}
	archive := tar.NewWriter(w)
	for _, entry := range []struct {
		name   string
		size   int64
		source io.Reader
	}{{"manifest.json", int64(len(manifest)), bytes.NewReader(manifest)}, {"content.bin", size, io.LimitReader(source, size)}} {
		if e := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: entry.size, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			return e
		}
		if n, e := io.Copy(archive, entry.source); e != nil || n != entry.size {
			if e != nil {
				return e
			}
			return fail("OBJECT_SNAPSHOT_INVALID")
		}
	}
	return archive.Close()
}

// Snapshot exports one exact verified private object and a canonical manifest.
// It complements a database-aware backup; it is not a live database copy or a
// promise to reconstruct remote effects from a metadata-only backup.
func (s *Service) Snapshot(ctx context.Context, a identity.Auth, workspace, object string) (httpserver.Download, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return httpserver.Download{}, e
	}
	defer done()
	if !admin(a) {
		return httpserver.Download{}, fail("ACCESS_DENIED")
	}
	source, o, e := s.verifiedSource(ctx, a, workspace, object, "", "read")
	if e != nil {
		return httpserver.Download{}, e
	}
	defer s.Stage.Discard(source)
	manifest := SnapshotManifest{snapshotSchema, o.Tenant, o.WorkspaceID, o.ID, o.StoreID, s.Stage.Identity(), o.Purpose, o.SHA256, o.ExpectedBytes}
	raw, e := json.Marshal(manifest)
	if e != nil || len(raw) > maximumSnapshotManifestBytes {
		return httpserver.Download{}, fail("OBJECT_SNAPSHOT_INVALID")
	}
	hash := sha256.New()
	counter := &countWriter{writer: hash}
	if e = writeSnapshot(snapshotContextWriter{ctx, counter}, raw, source.File, o.ExpectedBytes); e != nil {
		return httpserver.Download{}, fail("OBJECT_SNAPSHOT_INVALID")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	reader, writer := io.Pipe()
	produced := make(chan error, 1)
	go func() {
		e := writeSnapshot(snapshotContextWriter{ctx, writer}, raw, source.File, o.ExpectedBytes)
		writer.CloseWithError(e)
		produced <- e
	}()
	archived, e := s.Stage.Write(ctx, uuid.NewString(), counter.count, digest, reader)
	reader.CloseWithError(e)
	producerError := <-produced
	if e != nil {
		return httpserver.Download{}, safe(e)
	}
	defer func() {
		if archived.File != nil {
			s.Stage.Discard(archived)
		}
	}()
	if producerError != nil {
		return httpserver.Download{}, fail("OBJECT_SNAPSHOT_INVALID")
	}
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !admin(a) {
			return fail("ACCESS_DENIED")
		}
		current, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if current.State != "ready" || current.SHA256 != o.SHA256 || current.ETag == nil || o.ETag == nil || *current.ETag != *o.ETag {
			return fail("OBJECT_CHANGED")
		}
		if e = liveWithin(ctx, q, current, false); e != nil {
			return e
		}
		return s.Stage.Verify(archived)
	})
	if e != nil {
		return httpserver.Download{}, e
	}
	if ctx.Err() != nil {
		return httpserver.Download{}, safe(ctx.Err())
	}
	file, e := s.Stage.Detach(archived)
	if e != nil {
		return httpserver.Download{}, safe(e)
	}
	archived.File = nil
	if ctx.Err() != nil {
		file.Close()
		return httpserver.Download{}, safe(ctx.Err())
	}
	return httpserver.Download{File: file, Size: counter.count, SHA256: digest, FileName: o.ID + ".tar", ContentType: "application/x-tar"}, nil
}

// VerifySnapshot reads the entire strict two-entry archive. It never extracts
// caller paths or grants publication/restore authority from a manifest alone.
func VerifySnapshot(ctx context.Context, source io.Reader, expected SnapshotManifest) error {
	if ctx == nil || source == nil || expected.Schema != snapshotSchema || !id(expected.TenantID) || !id(expected.WorkspaceID) || !id(expected.ObjectID) || !id(expected.NamespaceID) || expected.Bytes < 1 || !objectstore.ValidDigest(expected.SHA256) || !objectstore.ValidDigest(expected.BackendSHA256) || expected.Purpose != "model_bundle" && expected.Purpose != "dataset_source" {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	reader := snapshotContextReader{ctx, source}
	canonical, _ := json.Marshal(expected)
	if len(canonical) > maximumSnapshotManifestBytes || snapshotHeader(reader, "manifest.json", int64(len(canonical))) != nil {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	raw := make([]byte, len(canonical))
	if _, e := io.ReadFull(reader, raw); e != nil || !bytes.Equal(raw, canonical) || snapshotPadding(reader, int64(len(canonical))) != nil {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	if e := snapshotHeader(reader, "content.bin", expected.Bytes); e != nil {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	hash := sha256.New()
	n, e := io.CopyN(hash, reader, expected.Bytes)
	if e != nil || n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return fail("OBJECT_DIGEST_MISMATCH")
	}
	if snapshotPadding(reader, expected.Bytes) != nil || snapshotZeros(reader, 2*snapshotBlockBytes) != nil {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	var tail [1]byte
	if n, e := reader.Read(tail[:]); n != 0 || e != io.EOF {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	return nil
}

// Compare raw blocks before a tar reader can consume transparent extension
// entries. The accepted envelope uses the existing writer's exact USTAR header.
func snapshotHeader(source io.Reader, name string, size int64) error {
	var expected bytes.Buffer
	writer := tar.NewWriter(&expected)
	if e := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil || expected.Len() != snapshotBlockBytes {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	var actual [snapshotBlockBytes]byte
	if _, e := io.ReadFull(source, actual[:]); e != nil || !bytes.Equal(actual[:], expected.Bytes()) {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	return nil
}

func snapshotPadding(source io.Reader, size int64) error {
	return snapshotZeros(source, int((snapshotBlockBytes-size%snapshotBlockBytes)%snapshotBlockBytes))
}

func snapshotZeros(source io.Reader, count int) error {
	var data [2 * snapshotBlockBytes]byte
	if count < 0 || count > len(data) {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	if _, e := io.ReadFull(source, data[:count]); e != nil {
		return fail("OBJECT_SNAPSHOT_INVALID")
	}
	for _, b := range data[:count] {
		if b != 0 {
			return fail("OBJECT_SNAPSHOT_INVALID")
		}
	}
	return nil
}

type snapshotContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r snapshotContextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.source.Read(b)
}

type snapshotContextWriter struct {
	ctx    context.Context
	target io.Writer
}

func (w snapshotContextWriter) Write(b []byte) (int, error) {
	if e := w.ctx.Err(); e != nil {
		return 0, e
	}
	return w.target.Write(b)
}
