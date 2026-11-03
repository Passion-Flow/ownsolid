package factory

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// importFixture contains a real gzip/tar byte stream with explicitly synthetic payloads.
func importFixture(t *testing.T, fault string) (Config, wire.Asset, string, int64) {
	t.Helper()
	c, _, _ := setup(t)
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	payload := []byte("Explicit synthetic immutable layer")
	tw.WriteHeader(&tar.Header{Name: "synthetic", Mode: 0644, Size: int64(len(payload)), Typeflag: tar.TypeReg})
	tw.Write(payload)
	tw.Close()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(layer.Bytes())
	gz.Close()
	diff := "sha256:" + wire.Digest(layer.Bytes())
	if fault == "diff-id" {
		diff = "sha256:" + strings.Repeat("b", 64)
	}
	blob := append([]byte(nil), compressed.Bytes()...)
	if fault == "gzip-crc" {
		blob[len(blob)-5] ^= 1
	}
	media := "application/vnd.oci.image.layer.v1.tar+gzip"
	if fault == "encoding" {
		media = "application/vnd.oci.image.layer.v1.tar+zstd"
	}
	config := Encode(map[string]any{"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	configID := "sha256:" + wire.Digest(config)
	blobID := "sha256:" + wire.Digest(blob)
	manifest := Encode(map[string]any{"schemaVersion": 2, "config": descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: configID, Size: int64(len(config))}, "layers": []descriptor{{MediaType: media, Digest: blobID, Size: int64(len(blob))}}})
	manifestID := "sha256:" + wire.Digest(manifest)
	index := Encode(map[string]any{"schemaVersion": 2, "annotations": map[string]string{"untrusted-name": "synthetic-name"}, "manifests": []any{map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": manifestID, "size": len(manifest), "annotations": map[string]string{"io.containerd.image.name": "synthetic/existing:conflicting", "org.opencontainers.image.ref.name": "conflicting"}, "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}})
	compat := Encode([]any{map[string]any{"Config": "blobs/sha256/" + strings.TrimPrefix(configID, "sha256:"), "RepoTags": []string{"synthetic/existing:conflicting"}, "Layers": []string{"blobs/sha256/" + strings.TrimPrefix(blobID, "sha256:")}}})
	var output bytes.Buffer
	archive := tar.NewWriter(&output)
	for _, p := range []struct {
		name string
		raw  []byte
	}{{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {"index.json", index}, {"manifest.json", compat}, {"blobs/sha256/" + strings.TrimPrefix(configID, "sha256:"), config}, {"blobs/sha256/" + strings.TrimPrefix(manifestID, "sha256:"), manifest}, {"blobs/sha256/" + strings.TrimPrefix(blobID, "sha256:"), blob}} {
		archive.WriteHeader(&tar.Header{Name: p.name, Mode: 0644, Size: int64(len(p.raw)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR})
		archive.Write(p.raw)
	}
	archive.Close()
	name := filepath.Join(c.ArtifactRoot, "synthetic-image.tar")
	put(t, name, output.Bytes())
	asset := wire.Asset{ID: "synthetic-image", Path: "synthetic-image.tar", Kind: "image_archive", Size: int64(output.Len()), SHA256: wire.Digest(output.Bytes()), Licenses: []string{"license"}, ImageID: manifestID, ImageConfigDigest: configID, Architecture: "amd64"}
	return c, asset, name, int64(layer.Len())
}
func tarFiles(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	archive := tar.NewReader(bytes.NewReader(raw))
	for {
		h, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(archive)
		if e != nil {
			t.Fatal(e)
		}
		files[h.Name] = data
	}
	return files
}
func TestTaglessTransportPreservesContentAndPinsExpandedLayers(t *testing.T) {
	c, a, name, expanded := importFixture(t, "")
	source, e := OpenImageSource(context.Background(), c, name, a, expanded)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	inventory := source.Inventory()
	if inventory.ManifestDigest != a.ImageID || inventory.ConfigDigest != a.ImageConfigDigest || inventory.ExpandedBytes != expanded {
		t.Fatal(inventory)
	}
	var transport bytes.Buffer
	receipt, e := source.WriteTagless(context.Background(), &transport)
	if e != nil || receipt.SourceSHA256 != a.SHA256 || receipt.TransportSHA256 != wire.Digest(transport.Bytes()) || receipt.TransportBytes != int64(transport.Len()) {
		t.Fatal(receipt, e)
	}
	original, _ := os.ReadFile(name)
	before := tarFiles(t, original)
	after := tarFiles(t, transport.Bytes())
	if _, ok := after["manifest.json"]; ok {
		t.Fatal("legacy named-tag map leaked")
	}
	var index map[string]any
	if json.Unmarshal(after["index.json"], &index) != nil || bytes.Contains(after["index.json"], []byte("annotations")) {
		t.Fatal("naming annotations leaked")
	}
	for path, raw := range before {
		if strings.HasPrefix(path, "blobs/") && !bytes.Equal(raw, after[path]) {
			t.Fatal("content blob changed", path)
		}
	}
}
func TestImportLayerIntegrityEncodingAndExpansionFailures(t *testing.T) {
	for _, fault := range []string{"diff-id", "gzip-crc", "encoding", "expansion", "source-hash", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c, a, name, expanded := importFixture(t, fault)
			maximum := expanded
			if fault == "expansion" {
				maximum--
			}
			if fault == "source-hash" {
				a.SHA256 = strings.Repeat("b", 64)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancelled" {
				cancel()
			}
			if source, e := OpenImageSource(ctx, c, name, a, maximum); e == nil {
				source.Close()
				t.Fatal("invalid image admitted")
			}
		})
	}
}

type failedImportWriter struct{}

func (failedImportWriter) Write([]byte) (int, error) {
	return 0, errors.New("explicit synthetic write failure")
}
func TestTaglessSourceMutationAndConsumerFailure(t *testing.T) {
	for _, fault := range []string{"source-changed", "writer", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c, a, name, expanded := importFixture(t, "")
			source, e := OpenImageSource(context.Background(), c, name, a, expanded)
			if e != nil {
				t.Fatal(e)
			}
			defer source.Close()
			var destination io.Writer = io.Discard
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "source-changed":
				raw, _ := os.ReadFile(name)
				raw[0] ^= 1
				put(t, name, raw)
			case "writer":
				destination = failedImportWriter{}
			case "cancelled":
				cancel()
			}
			if _, e = source.WriteTagless(ctx, destination); e == nil {
				t.Fatal("failed producer/consumer was reported complete")
			}
		})
	}
}
