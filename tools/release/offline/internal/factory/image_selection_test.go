package factory

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func selectionFixture(t *testing.T, brokenDiffID bool) (Config, map[string][]byte, imageDocument, []ImageIdentity) {
	t.Helper()
	c, a, name, _ := importFixture(t, "")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, raw)
	delete(files, "manifest.json")
	var index, document imageDocument
	if imageJSON(files["index.json"], &index) != nil || imageJSON(files[selectionBlobPath(a.ImageID)], &document) != nil {
		t.Fatal("invalid test image")
	}
	var config map[string]any
	if err := json.Unmarshal(files[selectionBlobPath(a.ImageConfigDigest)], &config); err != nil {
		t.Fatal(err)
	}
	config["config"] = map[string]any{"Env": []string{"TEST_IMAGE=second"}}
	if brokenDiffID {
		config["rootfs"] = map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + strings.Repeat("a", 64)}}
	}
	configBytes := Encode(config)
	document.Config.Digest = "sha256:" + wire.Digest(configBytes)
	document.Config.Size = int64(len(configBytes))
	manifestBytes := Encode(document)
	manifestDigest := "sha256:" + wire.Digest(manifestBytes)
	files[selectionBlobPath(document.Config.Digest)] = configBytes
	files[selectionBlobPath(manifestDigest)] = manifestBytes
	next := index.Manifests[0]
	next.Digest, next.Size = manifestDigest, int64(len(manifestBytes))
	index.Manifests = append(index.Manifests, next)
	return WithCurrentProfile(c), files, index, []ImageIdentity{
		{ManifestDigest: a.ImageID, ConfigDigest: a.ImageConfigDigest},
		{ManifestDigest: manifestDigest, ConfigDigest: document.Config.Digest},
	}
}

func selectionBlobPath(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

func writeSelectionFixture(t *testing.T, c Config, files map[string][]byte, index imageDocument, identity ImageIdentity) (string, wire.Asset) {
	t.Helper()
	files["index.json"] = Encode(index)
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data := files[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(c.ArtifactRoot, "selected-images.tar")
	put(t, filename, raw.Bytes())
	return filename, wire.Asset{
		Kind: "image_archive", Size: int64(raw.Len()), SHA256: wire.Digest(raw.Bytes()), Architecture: "amd64",
		ImageID: identity.ManifestDigest, ImageConfigDigest: identity.ConfigDigest,
	}
}

func TestCurrentImageSelectionRetainsOneExactImage(t *testing.T) {
	c, files, index, approved := selectionFixture(t, false)
	filename, original := writeSelectionFixture(t, c, files, index, approved[0])
	for i, identity := range approved {
		asset := original
		asset.ImageID, asset.ImageConfigDigest = identity.ManifestDigest, identity.ConfigDigest
		if old, err := OpenImageSource(t.Context(), c, filename, asset, 1<<20); ErrorCode(err) != "RELEASE_OCI_PLATFORM_MANIFEST_IDENTITY_INVALID" || old != nil {
			t.Fatal("single-image reader accepted a second runtime manifest", err)
		}
		source, err := OpenImageSelection(t.Context(), c, filename, asset, approved, math.MaxInt64)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { source.Close() })
		inventory := source.Inventory()
		if inventory.ManifestDigest != identity.ManifestDigest || inventory.ConfigDigest != identity.ConfigDigest || inventory.ExpandedBytes == 0 {
			t.Fatal(inventory)
		}
		var tagless bytes.Buffer
		receipt, err := source.WriteTagless(t.Context(), &tagless)
		if err != nil || receipt.SourceSHA256 != asset.SHA256 || receipt.ExpandedBytes != inventory.ExpandedBytes {
			t.Fatal(receipt, err)
		}
		emitted := tarFiles(t, tagless.Bytes())
		other := approved[1-i]
		if len(emitted) != 2+len(source.RuntimeBlobs()) || emitted[selectionBlobPath(other.ManifestDigest)] != nil || emitted[selectionBlobPath(other.ConfigDigest)] != nil {
			t.Fatal("transport includes an unselected image")
		}
		for _, blob := range source.RuntimeBlobs() {
			reader, err := source.OpenRuntimeBlob(blob.Digest)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(reader)
			name := selectionBlobPath(blob.Digest)
			if err != nil || !bytes.Equal(actual, files[name]) || !bytes.Equal(emitted[name], files[name]) {
				t.Fatal("selected runtime blob changed", name, err)
			}
		}
		transport := filepath.Join(t.TempDir(), "tagless.tar")
		put(t, transport, tagless.Bytes())
		asset.Size, asset.SHA256 = receipt.TransportBytes, receipt.TransportSHA256
		reopened, err := OpenImageSource(t.Context(), c, transport, asset, inventory.ExpandedBytes)
		if err != nil {
			t.Fatal("single-image reader refused selected output", err)
		}
		reopened.Close()
	}
}

func TestCurrentImageSelectionRefusesInvalidInventory(t *testing.T) {
	for _, test := range []struct{ name, code string }{
		{"empty-approval", "RELEASE_OCI_IMAGE_SELECTION_INVALID"},
		{"duplicate-approval", "RELEASE_OCI_IMAGE_SELECTION_INVALID"},
		{"wrong-selection", "RELEASE_OCI_IMAGE_SELECTION_INVALID"},
		{"wrong-sibling-config", "RELEASE_OCI_CONFIG_IDENTITY_INVALID"},
		{"undeclared", "RELEASE_OCI_IMAGE_SET_INVALID"},
		{"duplicate-root", "RELEASE_OCI_IMAGE_SET_INVALID"},
		{"missing-manifest", "RELEASE_OCI_DESCRIPTOR_INVALID"},
		{"missing-config", "RELEASE_OCI_CONFIG_IDENTITY_INVALID"},
		{"remote-descriptor", "RELEASE_OCI_IMAGE_SET_INVALID"},
		{"orphan-blob", "RELEASE_OCI_IMAGE_SET_INVALID"},
		{"corrupt-blob", "RELEASE_OCI_BLOB_HASH_INVALID"},
		{"wrong-archive-digest", "RELEASE_IMPORT_IMAGE_SOURCE_HASH_INVALID"},
		{"bad-selected-diff-id", "RELEASE_IMPORT_LAYER_DIFF_ID_INVALID"},
		{"expanded-limit", "RELEASE_IMPORT_LAYER_EXPANSION_OR_READ_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, files, index, approved := selectionFixture(t, test.name == "bad-selected-diff-id")
			selected, sibling := approved[0], approved[1]
			maximum := int64(1 << 20)
			switch test.name {
			case "empty-approval":
				approved = nil
			case "duplicate-approval":
				approved[1] = selected
			case "wrong-selection":
				selected.ConfigDigest = sibling.ConfigDigest
			case "wrong-sibling-config":
				approved[1].ConfigDigest = selected.ConfigDigest
			case "undeclared":
				approved = approved[:1]
			case "duplicate-root":
				index.Manifests[1] = index.Manifests[0]
			case "missing-manifest":
				delete(files, selectionBlobPath(sibling.ManifestDigest))
			case "missing-config":
				delete(files, selectionBlobPath(sibling.ConfigDigest))
			case "remote-descriptor":
				index.Manifests[1].URLs = []string{"https://example.invalid/image"}
			case "orphan-blob":
				data := []byte("unreferenced fixture blob")
				files[selectionBlobPath("sha256:"+wire.Digest(data))] = data
			case "corrupt-blob":
				files[selectionBlobPath(sibling.ConfigDigest)][0] ^= 1
			case "bad-selected-diff-id":
				selected = sibling
			case "expanded-limit":
				maximum = 1
			}
			filename, asset := writeSelectionFixture(t, c, files, index, selected)
			if test.name == "wrong-archive-digest" {
				asset.SHA256 = strings.Repeat("0", 64)
			}
			source, err := OpenImageSelection(t.Context(), c, filename, asset, approved, maximum)
			if ErrorCode(err) != test.code || source != nil {
				t.Fatalf("wanted %s, got %v", test.code, err)
			}
		})
	}
}
