package factory

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type ImageLayer struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size_bytes"`
	DiffID    string `json:"diff_id"`
}
type ImageInventory struct {
	ManifestDigest    string       `json:"manifest_digest"`
	ConfigDigest      string       `json:"config_digest"`
	OS                string       `json:"os"`
	Architecture      string       `json:"architecture"`
	DiffIDs           []string     `json:"diff_ids"`
	Layers            []ImageLayer `json:"layers"`
	ExpandedBytes     int64        `json:"expanded_bytes"`
	manifestMediaType string
	manifestBytes     int64
	configMediaType   string
	configBytes       int64
}

// RuntimeBlob identifies a config, layer or manifest from the selected OCI image.
type RuntimeBlob struct {
	Digest, MediaType string
	Size              int64
	Manifest          bool
}

type runtimeBlobLocation struct {
	RuntimeBlob
	offset int64
}

type ImageSource struct {
	file      *os.File
	before    os.FileInfo
	asset     wire.Asset
	config    Config
	inventory ImageInventory
	runtime   []runtimeBlobLocation
}
type ImportReceipt struct {
	SourceSHA256    string `json:"source_sha256"`
	TransportSHA256 string `json:"transport_sha256"`
	TransportBytes  int64  `json:"transport_bytes"`
	ExpandedBytes   int64  `json:"expanded_bytes"`
}

func (s *ImageSource) Close() error { return s.file.Close() }
func (s *ImageSource) Inventory() ImageInventory {
	out := s.inventory
	out.DiffIDs = append([]string(nil), out.DiffIDs...)
	out.Layers = append([]ImageLayer(nil), out.Layers...)
	return out
}

// RuntimeBlobs returns the config and distinct layers before the selected manifest.
func (s *ImageSource) RuntimeBlobs() []RuntimeBlob {
	out := make([]RuntimeBlob, len(s.runtime))
	for i, b := range s.runtime {
		out[i] = b.RuntimeBlob
	}
	return out
}

// OpenRuntimeBlob reads the exact bytes at the verified archive offset.
func (s *ImageSource) OpenRuntimeBlob(digest string) (*io.SectionReader, error) {
	if err := s.CheckUnchanged(); err != nil {
		return nil, err
	}
	for _, b := range s.runtime {
		if b.Digest == digest {
			return io.NewSectionReader(s.file, b.offset, b.Size), nil
		}
	}
	return nil, fail("RELEASE_IMPORT_RUNTIME_BLOB_MISSING")
}

// CheckUnchanged checks the pinned archive descriptor after a consuming operation.
func (s *ImageSource) CheckUnchanged() error {
	current, err := s.file.Stat()
	if err != nil || !sameRegularSnapshot(s.before, current, s.asset.Size, true, 1) {
		return fail("RELEASE_IMPORT_IMAGE_SOURCE_CHANGED")
	}
	return nil
}

func OpenImageSource(ctx context.Context, c Config, filename string, a wire.Asset, maximumExpandedBytes int64) (*ImageSource, error) {
	return openImageSource(ctx, c, filename, a, maximumExpandedBytes, nil)
}

func openImageSource(ctx context.Context, c Config, filename string, a wire.Asset, maximumExpandedBytes int64, selection map[string]string) (*ImageSource, error) {
	if ctx == nil || ctx.Err() != nil || mounted(c.MountRoot) != nil || !within(c.MountRoot, filename) || a.Kind != "image_archive" || a.Size < 1 || a.Size > c.MaximumAssetBytes || !wire.ValidSHA256(a.SHA256) || maximumExpandedBytes < 1 {
		return nil, fail("RELEASE_IMPORT_IMAGE_INPUT_INVALID")
	}
	f, e := openRegular(filename, a.Size, true)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			f.Close()
		}
	}()
	before, e := f.Stat()
	if e != nil || !regularInput(before, a.Size, true, 1) {
		return nil, fail("RELEASE_IMPORT_IMAGE_SOURCE_CHANGED")
	}
	input, e := newStreamReader(ctx, f)
	if e != nil {
		return nil, e
	}
	sum := sha256.New()
	counter := &countReader{r: io.TeeReader(input, sum)}
	var inventory ImageInventory
	if e = validateImageInventory(ctx, counter, a, c, &inventory, selection); e != nil {
		return nil, e
	}
	if counter.n != a.Size || hex.EncodeToString(sum.Sum(nil)) != a.SHA256 {
		return nil, fail("RELEASE_IMPORT_IMAGE_SOURCE_HASH_INVALID")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, fail("RELEASE_IMPORT_IMAGE_SEEK_FAILED")
	}
	remaining := maximumExpandedBytes
	layers := map[string]ImageLayer{}
	multiplicity := map[string]int64{}
	for _, layer := range inventory.Layers {
		name := "blobs/sha256/" + strings.TrimPrefix(layer.Digest, "sha256:")
		if previous, ok := layers[name]; ok && previous != layer {
			return nil, fail("RELEASE_IMPORT_REPEATED_LAYER_CONFLICT")
		}
		layers[name] = layer
		multiplicity[name]++
	}
	observed := map[string]bool{}
	runtime := []runtimeBlobLocation{
		{RuntimeBlob: RuntimeBlob{Digest: inventory.ConfigDigest, MediaType: inventory.configMediaType, Size: inventory.configBytes}},
	}
	unique := map[string]bool{inventory.ConfigDigest: true}
	for _, layer := range inventory.Layers {
		if !unique[layer.Digest] {
			unique[layer.Digest] = true
			runtime = append(runtime, runtimeBlobLocation{RuntimeBlob: RuntimeBlob{Digest: layer.Digest, MediaType: layer.MediaType, Size: layer.Size}})
		}
	}
	runtime = append(runtime, runtimeBlobLocation{RuntimeBlob: RuntimeBlob{
		Digest: inventory.ManifestDigest, MediaType: inventory.manifestMediaType, Size: inventory.manifestBytes, Manifest: true,
	}})
	runtimeIndex := make(map[string]int, len(runtime))
	for i, b := range runtime {
		runtimeIndex["blobs/sha256/"+strings.TrimPrefix(b.Digest, "sha256:")] = i
	}
	sum.Reset()
	input, e = newStreamReader(ctx, f)
	if e != nil {
		return nil, e
	}
	counter = &countReader{r: io.TeeReader(input, sum)}
	archive := tar.NewReader(counter)
	for {
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fail("RELEASE_IMPORT_LAYER_ARCHIVE_INVALID")
		}
		if i, ok := runtimeIndex[header.Name]; ok {
			if runtime[i].Size != header.Size || runtime[i].offset != 0 {
				return nil, fail("RELEASE_IMPORT_RUNTIME_BLOB_INVALID")
			}
			runtime[i].offset = counter.n
		}
		layer, ok := layers[header.Name]
		if !ok {
			continue
		}
		if observed[header.Name] {
			return nil, fail("RELEASE_IMPORT_LAYER_DUPLICATE")
		}
		observed[header.Name] = true
		var input io.Reader = archive
		var compressed *gzip.Reader
		switch layer.MediaType {
		case "application/vnd.oci.image.layer.v1.tar":
		case "application/vnd.oci.image.layer.v1.tar+gzip", "application/vnd.docker.image.rootfs.diff.tar.gzip":
			compressed, e = gzip.NewReader(archive)
			if e != nil {
				return nil, fail("RELEASE_IMPORT_LAYER_GZIP_INVALID")
			}
			input = compressed
		default:
			return nil, fail("RELEASE_IMPORT_LAYER_ENCODING_UNSUPPORTED")
		}
		allowance := remaining / multiplicity[header.Name]
		limit := allowance
		if limit < math.MaxInt64 {
			limit++
		}
		digest := sha256.New()
		n, e := io.CopyBuffer(digest, io.LimitReader(checkedReader{ctx, input}, limit), make([]byte, 65536))
		if compressed != nil {
			compressed.Close()
		}
		if e != nil || n > allowance {
			return nil, fail("RELEASE_IMPORT_LAYER_EXPANSION_OR_READ_FAILED")
		}
		if "sha256:"+hex.EncodeToString(digest.Sum(nil)) != layer.DiffID {
			return nil, fail("RELEASE_IMPORT_LAYER_DIFF_ID_INVALID")
		}
		remaining -= n * multiplicity[header.Name]
	}
	tail, e := io.ReadAll(io.LimitReader(counter, 10241))
	if e != nil || len(tail) > 10240 || len(bytes.Trim(tail, "\x00")) != 0 || counter.n != a.Size || hex.EncodeToString(sum.Sum(nil)) != a.SHA256 || len(observed) != len(layers) {
		return nil, fail("RELEASE_IMPORT_LAYER_SOURCE_CHANGED")
	}
	after, e := f.Stat()
	pathAfter, pe := os.Lstat(filename)
	if e != nil || pe != nil || !sameRegularSnapshot(before, after, a.Size, true, 1) || !sameRegularSnapshot(before, pathAfter, a.Size, true, 1) {
		return nil, fail("RELEASE_IMPORT_IMAGE_SOURCE_CHANGED")
	}
	inventory.ExpandedBytes = maximumExpandedBytes - remaining
	for _, b := range runtime {
		if b.offset == 0 {
			return nil, fail("RELEASE_IMPORT_RUNTIME_BLOB_MISSING")
		}
	}
	success = true
	return &ImageSource{file: f, before: before, asset: a, config: c, inventory: inventory, runtime: runtime}, nil
}

// WriteTagless preserves the selected runtime blobs with a single platform root;
// source names, wrapper indexes and non-runtime attestations are not imported.
// A caller must treat any stream/hash error after submission as an uncertain effect.
func (s *ImageSource) WriteTagless(ctx context.Context, output io.Writer) (ImportReceipt, error) {
	result := ImportReceipt{}
	if err := s.CheckUnchanged(); err != nil {
		return result, err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return result, fail("RELEASE_IMPORT_IMAGE_SEEK_FAILED")
	}
	sourceHash := sha256.New()
	input, e := newStreamReader(ctx, s.file)
	if e != nil {
		return result, e
	}
	counter := &countReader{r: io.TeeReader(input, sourceHash)}
	archive := tar.NewReader(counter)
	transportHash := sha256.New()
	destination := &countWriter{w: io.MultiWriter(output, transportHash)}
	writer := tar.NewWriter(destination)
	seen := map[string]bool{}
	wanted := map[string]bool{
		"blobs/sha256/" + strings.TrimPrefix(s.inventory.ManifestDigest, "sha256:"): true,
		"blobs/sha256/" + strings.TrimPrefix(s.inventory.ConfigDigest, "sha256:"):   true,
	}
	for _, layer := range s.inventory.Layers {
		wanted["blobs/sha256/"+strings.TrimPrefix(layer.Digest, "sha256:")] = true
	}
	for {
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return result, fail("RELEASE_IMPORT_TRANSPORT_SOURCE_INVALID")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] || (!wire.SafePath(name) && name != "manifest.json") {
			return result, fail("RELEASE_IMPORT_TRANSPORT_ENTRY_INVALID")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" {
				return result, fail("RELEASE_IMPORT_TRANSPORT_ENTRY_INVALID")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > s.asset.Size || len(header.PAXRecords) > 0 || len(header.Xattrs) > 0 {
			return result, fail("RELEASE_IMPORT_TRANSPORT_ENTRY_INVALID")
		}
		if header.Name == "manifest.json" || header.Name == "index.json" {
			continue
		}
		var data io.Reader = archive
		size := header.Size
		if header.Name != "oci-layout" && (!strings.HasPrefix(header.Name, "blobs/sha256/") || !wire.ValidSHA256(strings.TrimPrefix(header.Name, "blobs/sha256/"))) {
			return result, fail("RELEASE_IMPORT_TRANSPORT_ENTRY_INVALID")
		}
		if header.Name != "oci-layout" && !wanted[header.Name] {
			continue
		}
		if e = writer.WriteHeader(&tar.Header{Name: header.Name, Mode: 0644, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			return result, fail("RELEASE_IMPORT_TRANSPORT_WRITE_FAILED")
		}
		n, e := io.CopyBuffer(writer, io.LimitReader(checkedReader{ctx, data}, size+1), make([]byte, 65536))
		if e != nil || n != size {
			return result, fail("RELEASE_IMPORT_TRANSPORT_WRITE_FAILED")
		}
	}
	tail, e := io.ReadAll(io.LimitReader(counter, 10241))
	if e != nil || len(tail) > 10240 || len(bytes.Trim(tail, "\x00")) != 0 || counter.n != s.asset.Size || hex.EncodeToString(sourceHash.Sum(nil)) != s.asset.SHA256 {
		return result, fail("RELEASE_IMPORT_TRANSPORT_SOURCE_CHANGED")
	}
	if err := s.CheckUnchanged(); err != nil {
		return result, fail("RELEASE_IMPORT_TRANSPORT_SOURCE_CHANGED")
	}
	for name := range wanted {
		if !seen[name] {
			return result, fail("RELEASE_IMPORT_TRANSPORT_SOURCE_CHANGED")
		}
	}
	// Select the exact runnable platform manifest, without source index names,
	// wrapper indexes or non-runtime attestations. Runtime blob bytes stay exact.
	index := Encode(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": []any{map[string]any{"mediaType": s.inventory.manifestMediaType, "digest": s.inventory.ManifestDigest, "size": s.inventory.manifestBytes}}})
	if e = writer.WriteHeader(&tar.Header{Name: "index.json", Mode: 0644, Size: int64(len(index)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
		return result, fail("RELEASE_IMPORT_TRANSPORT_WRITE_FAILED")
	}
	if _, e = writer.Write(index); e != nil {
		return result, fail("RELEASE_IMPORT_TRANSPORT_WRITE_FAILED")
	}
	if e = writer.Close(); e != nil {
		return result, fail("RELEASE_IMPORT_TRANSPORT_WRITE_FAILED")
	}
	return ImportReceipt{SourceSHA256: s.asset.SHA256, TransportSHA256: hex.EncodeToString(transportHash.Sum(nil)), TransportBytes: destination.n, ExpandedBytes: s.inventory.ExpandedBytes}, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (w *countWriter) Write(raw []byte) (int, error) {
	n, e := w.w.Write(raw)
	w.n += int64(n)
	return n, e
}
