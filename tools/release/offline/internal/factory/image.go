package factory

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type imageBlob struct {
	size int64
	data []byte
}
type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	URLs        []string          `json:"urls"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Data        string            `json:"data,omitempty"`
	Platform    *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Variant      string `json:"variant"`
	} `json:"platform,omitempty"`
}
type imageDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	Manifests     []descriptor `json:"manifests"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
	ArtifactType  string       `json:"artifactType,omitempty"`
	Subject       *descriptor  `json:"subject,omitempty"`
}

func imageJSON(raw []byte, v any) error {
	var object map[string]any
	if wire.UniqueJSON(raw, &object) != nil || json.Unmarshal(raw, v) != nil {
		return fail("RELEASE_OCI_JSON_INVALID")
	}
	return nil
}

// validateImage hashes every blob and follows the complete local descriptor closure.
// Layers are opaque signed content; runtime behavior remains a separate acceptance.
func validateImage(ctx context.Context, reader io.Reader, a wire.Asset, c Config) error {
	return validateImageInventory(ctx, reader, a, c, nil)
}
func validateImageInventory(ctx context.Context, reader io.Reader, a wire.Asset, c Config, inventory *ImageInventory) error {
	t := tar.NewReader(checkedReader{ctx, reader})
	blobs := map[string]imageBlob{}
	seen := map[string]bool{}
	var index, layout, compat []byte
	metadata := int64(0)
	count := 0
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return fail("RELEASE_OCI_TAR_INVALID")
		}
		count++
		name := strings.TrimSuffix(h.Name, "/")
		if count > c.MaximumArchiveEntries || (!wire.SafePath(name) && name != "manifest.json") || seen[name] || len(h.PAXRecords) > 0 || len(h.Xattrs) > 0 {
			return fail("RELEASE_OCI_ENTRY_INVALID")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" {
				return fail("RELEASE_OCI_ENTRY_INVALID")
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > a.Size {
			return fail("RELEASE_OCI_ENTRY_INVALID")
		}
		if name == "index.json" || name == "oci-layout" || name == "manifest.json" {
			if h.Size > c.MaximumMetadataBytes-metadata {
				return fail("RELEASE_OCI_METADATA_LIMIT")
			}
			raw, e := io.ReadAll(t)
			if e != nil {
				return fail("RELEASE_OCI_READ_FAILED")
			}
			metadata += int64(len(raw))
			switch name {
			case "index.json":
				index = raw
			case "oci-layout":
				layout = raw
			default:
				compat = raw
			}
			continue
		}
		if !strings.HasPrefix(name, "blobs/sha256/") || len(name) != len("blobs/sha256/")+64 {
			return fail("RELEASE_OCI_ENTRY_INVALID")
		}
		hash := sha256.New()
		var raw []byte
		if h.Size <= c.MaximumMetadataBytes-metadata {
			raw, e = io.ReadAll(io.TeeReader(t, hash))
			if e != nil {
				return fail("RELEASE_OCI_READ_FAILED")
			}
			if json.Valid(raw) {
				metadata += int64(len(raw))
			} else {
				raw = nil
			}
		} else {
			if _, e = io.Copy(hash, t); e != nil {
				return fail("RELEASE_OCI_READ_FAILED")
			}
		}
		if hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(name, "blobs/sha256/") {
			return fail("RELEASE_OCI_BLOB_HASH_INVALID")
		}
		blobs["sha256:"+strings.TrimPrefix(name, "blobs/sha256/")] = imageBlob{h.Size, raw}
	}
	var marker struct {
		Version string `json:"imageLayoutVersion"`
	}
	if imageJSON(layout, &marker) != nil || marker.Version != "1.0.0" {
		return fail("RELEASE_OCI_LAYOUT_REQUIRED")
	}
	var root imageDocument
	if imageJSON(index, &root) != nil || root.SchemaVersion != 2 || len(root.Manifests) == 0 {
		return fail("RELEASE_OCI_INDEX_INVALID")
	}
	configs := 0
	visited := map[string]bool{}
	var selectedLayers []descriptor
	var visit func(descriptor, int) error
	visit = func(d descriptor, depth int) error {
		attestation := d.Annotations[attestationReferenceType] != ""
		referrer := c.current && depth == 0 && d.Annotations[containerdSubject] != ""
		if !attestation && !referrer && d.Platform != nil && (d.Platform.Architecture != a.Architecture || d.Platform.OS != "linux" || d.Platform.Variant != "") {
			return fail("RELEASE_OCI_DESCRIPTOR_PLATFORM_INVALID")
		}
		blob, ok := blobs[d.Digest]
		if !ok || blob.size != d.Size || d.Size < 1 || len(d.URLs) > 0 || depth > 16 {
			return fail("RELEASE_OCI_DESCRIPTOR_INVALID")
		}
		var doc imageDocument
		if imageJSON(blob.data, &doc) != nil || doc.SchemaVersion != 2 {
			return fail("RELEASE_OCI_MANIFEST_INVALID")
		}
		if referrer {
			return checkCurrentReferrer(d, doc, blobs, a)
		}
		if attestation {
			return validateAttestation(d, doc, blobs, a)
		}
		if doc.ArtifactType != "" || doc.Subject != nil {
			return fail("RELEASE_OCI_UNDECLARED_ARTIFACT")
		}
		if visited[d.Digest] {
			return nil
		}
		visited[d.Digest] = true
		switch d.MediaType {
		case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
			if len(doc.Manifests) == 0 {
				return fail("RELEASE_OCI_INDEX_INVALID")
			}
			for _, next := range doc.Manifests {
				if e := visit(next, depth+1); e != nil {
					return e
				}
			}
		case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
			if d.Digest != a.ImageID {
				return fail("RELEASE_OCI_PLATFORM_MANIFEST_IDENTITY_INVALID")
			}
			config, ok := blobs[doc.Config.Digest]
			if !ok || config.size != doc.Config.Size || doc.Config.Digest != a.ImageConfigDigest || len(doc.Config.URLs) > 0 || (doc.Config.MediaType != "application/vnd.oci.image.config.v1+json" && doc.Config.MediaType != "application/vnd.docker.container.image.v1+json") {
				return fail("RELEASE_OCI_CONFIG_IDENTITY_INVALID")
			}
			var identity struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
				RootFS       struct {
					Type    string   `json:"type"`
					DiffIDs []string `json:"diff_ids"`
				} `json:"rootfs"`
			}
			if imageJSON(config.data, &identity) != nil || identity.Architecture != a.Architecture || identity.OS != "linux" || identity.RootFS.Type != "layers" || len(identity.RootFS.DiffIDs) != len(doc.Layers) {
				return fail("RELEASE_OCI_ARCHITECTURE_INVALID")
			}
			for i, layer := range doc.Layers {
				b, ok := blobs[layer.Digest]
				media := layer.MediaType
				allowed := media == "application/vnd.oci.image.layer.v1.tar" || media == "application/vnd.oci.image.layer.v1.tar+gzip" || (!c.current && media == "application/vnd.oci.image.layer.v1.tar+zstd") || media == "application/vnd.docker.image.rootfs.diff.tar.gzip"
				if !ok || b.size != layer.Size || layer.Size < 1 || len(layer.URLs) > 0 || !allowed || !strings.HasPrefix(identity.RootFS.DiffIDs[i], "sha256:") || !wire.ValidSHA256(strings.TrimPrefix(identity.RootFS.DiffIDs[i], "sha256:")) {
					return fail("RELEASE_OCI_LAYER_INVALID")
				}
			}
			configs++
			if inventory != nil {
				inventory.manifestMediaType = d.MediaType
				inventory.manifestBytes = d.Size
				inventory.ManifestDigest = d.Digest
				inventory.ConfigDigest = doc.Config.Digest
				inventory.configMediaType = doc.Config.MediaType
				inventory.configBytes = doc.Config.Size
				inventory.OS = identity.OS
				inventory.Architecture = identity.Architecture
				inventory.DiffIDs = append([]string(nil), identity.RootFS.DiffIDs...)
				inventory.Layers = make([]ImageLayer, len(doc.Layers))
				for i, layer := range doc.Layers {
					inventory.Layers[i] = ImageLayer{Digest: layer.Digest, MediaType: layer.MediaType, Size: layer.Size, DiffID: identity.RootFS.DiffIDs[i]}
				}
			}
			selectedLayers = doc.Layers
		default:
			return fail("RELEASE_OCI_MEDIA_TYPE_UNSUPPORTED")
		}
		return nil
	}
	for _, d := range root.Manifests {
		if e := visit(d, 0); e != nil {
			return e
		}
	}
	if configs != 1 {
		return fail("RELEASE_OCI_ONE_DECLARED_IMAGE_REQUIRED")
	}
	if len(compat) > 0 {
		var raw any
		if wire.UniqueJSON(compat, &raw) != nil {
			return fail("RELEASE_OCI_COMPAT_INVALID")
		}
		var entries []struct {
			Config   string
			Layers   []string
			RepoTags []string
		}
		if json.Unmarshal(compat, &entries) != nil || len(entries) != 1 || entries[0].Config != "blobs/sha256/"+strings.TrimPrefix(a.ImageConfigDigest, "sha256:") || len(entries[0].Layers) != len(selectedLayers) {
			return fail("RELEASE_OCI_COMPAT_INVALID")
		}
		for i, layer := range entries[0].Layers {
			if layer != "blobs/sha256/"+strings.TrimPrefix(selectedLayers[i].Digest, "sha256:") {
				return fail("RELEASE_OCI_COMPAT_INVALID")
			}
		}
	}
	// tar.Reader stops at the end marker; no second archive or nonzero trailer is admitted.
	tail, e := io.ReadAll(io.LimitReader(reader, 10241))
	if e != nil || len(tail) > 10240 || len(bytes.Trim(tail, "\x00")) != 0 {
		return fail("RELEASE_OCI_TRAILING_DATA")
	}
	return nil
}
