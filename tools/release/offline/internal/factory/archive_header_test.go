package factory

import (
	"archive/tar"
	"bytes"
	"strconv"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Exercise the real encoder/decoder on headers without allocating a multi-GiB
// fixture. Complete actual large-image factory/consumer evidence is separate.
func TestReleaseArchiveHeaderCoversAdmittedAssetSizes(t *testing.T) {
	for _, size := range []int64{1, 1<<33 - 1, 1 << 33, 9035303936, 1 << 40} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			var encoded bytes.Buffer
			writer := tar.NewWriter(&encoded)
			if e := writer.WriteHeader(releaseArchiveHeader("assets/inference-image.tar", size)); e != nil {
				t.Fatal("manifest-admitted size cannot be represented in release archive", e)
			}
			if encoded.Len() > 4096 {
				t.Fatal("unexpected unbounded header metadata")
			}
			// No body is materialized. Next must still return the declared header.
			header, e := tar.NewReader(bytes.NewReader(encoded.Bytes())).Next()
			if e != nil || header.Name != "assets/inference-image.tar" || header.Size != size || header.Typeflag != tar.TypeReg || header.Mode != 0644 || header.Linkname != "" {
				t.Fatal("archive reader lost exact declared identity", header, e)
			}
			if !safeAssetHeader(header, wire.Asset{Path: "assets/inference-image.tar", Size: size}) {
				t.Fatal("actual signed-asset consumer refused producer header")
			}
		})
	}
}

func TestReleaseArchiveHeaderPreservesLegacyUSTARBytes(t *testing.T) {
	for _, size := range []int64{1, 1024, 1<<33 - 1} {
		var previous, current bytes.Buffer
		legacy := &tar.Header{Name: "assets/retained-image.tar", Mode: 0644, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if e := tar.NewWriter(&previous).WriteHeader(legacy); e != nil {
			t.Fatal(e)
		}
		if e := tar.NewWriter(&current).WriteHeader(releaseArchiveHeader(legacy.Name, size)); e != nil || !bytes.Equal(previous.Bytes(), current.Bytes()) {
			t.Fatal("unchanged small assets changed archive encoding", e)
		}
	}
}

func TestSignedAssetPAXSizeRefusesEveryOtherExtensionAndIdentity(t *testing.T) {
	const size int64 = 9035303936
	var encoded bytes.Buffer
	if e := tar.NewWriter(&encoded).WriteHeader(releaseArchiveHeader("assets/inference-image.tar", size)); e != nil {
		t.Fatal(e)
	}
	original, e := tar.NewReader(bytes.NewReader(encoded.Bytes())).Next()
	if e != nil || !safeAssetHeader(original, wire.Asset{Path: original.Name, Size: size}) {
		t.Fatal("actual producer/consumer size metadata", e)
	}
	if safeHeader(original) {
		t.Fatal("metadata header admission was broadened")
	}
	for name, change := range map[string]func(*tar.Header, *wire.Asset){
		"unknown":       func(h *tar.Header, a *wire.Asset) { h.PAXRecords["comment"] = "untrusted" },
		"path":          func(h *tar.Header, a *wire.Asset) { h.PAXRecords["path"] = h.Name },
		"linkpath":      func(h *tar.Header, a *wire.Asset) { h.PAXRecords["linkpath"] = "elsewhere" },
		"mtime":         func(h *tar.Header, a *wire.Asset) { h.PAXRecords["mtime"] = "0" },
		"uid-extension": func(h *tar.Header, a *wire.Asset) { h.PAXRecords["uid"] = "0" },
		"sparse":        func(h *tar.Header, a *wire.Asset) { h.PAXRecords["GNU.sparse.size"] = "1" },
		"xattr-record":  func(h *tar.Header, a *wire.Asset) { h.PAXRecords["SCHILY.xattr.test"] = "value" },
		"xattr":         func(h *tar.Header, a *wire.Asset) { h.Xattrs = map[string]string{"test": "value"} },
		"wrong-size":    func(h *tar.Header, a *wire.Asset) { h.PAXRecords["size"] = "9035303935" },
		"leading-zero":  func(h *tar.Header, a *wire.Asset) { h.PAXRecords["size"] = "09035303936" },
		"signed-plus":   func(h *tar.Header, a *wire.Asset) { h.PAXRecords["size"] = "+9035303936" },
		"empty-size":    func(h *tar.Header, a *wire.Asset) { h.PAXRecords["size"] = "" },
		"mode":          func(h *tar.Header, a *wire.Asset) { h.Mode = 0600 },
		"uid":           func(h *tar.Header, a *wire.Asset) { h.Uid = 1 },
		"gid":           func(h *tar.Header, a *wire.Asset) { h.Gid = 1 },
		"hardlink":      func(h *tar.Header, a *wire.Asset) { h.Typeflag = tar.TypeLink },
		"link":          func(h *tar.Header, a *wire.Asset) { h.Linkname = "elsewhere" },
		"format":        func(h *tar.Header, a *wire.Asset) { h.Format = tar.FormatGNU },
		"signed-size":   func(h *tar.Header, a *wire.Asset) { a.Size-- },
		"signed-path":   func(h *tar.Header, a *wire.Asset) { a.Path = "assets/other.tar" },
		"unsafe-path":   func(h *tar.Header, a *wire.Asset) { h.Name = "../outside"; a.Path = h.Name },
		"unnecessary": func(h *tar.Header, a *wire.Asset) {
			h.Size, a.Size = 16, 16
			h.PAXRecords["size"] = "16"
		},
	} {
		t.Run(name, func(t *testing.T) {
			header := *original
			header.PAXRecords = map[string]string{"size": strconv.FormatInt(size, 10)}
			asset := wire.Asset{Path: header.Name, Size: size}
			change(&header, &asset)
			if safeAssetHeader(&header, asset) {
				t.Fatal("noncanonical or foreign PAX/asset identity admitted")
			}
		})
	}
}
