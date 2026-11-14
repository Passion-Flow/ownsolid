package factory

import (
	"bytes"
	"encoding/base64"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const containerdSubject = "io.containerd.manifest.subject"

// Docker Engine's containerd exporter annotates local referrers to the selected
// image. Treat them as opaque non-runtime metadata. Their bytes and descriptors
// must be local and intact, and the tagless writer excludes them from import.
func checkCurrentReferrer(d descriptor, document imageDocument, blobs map[string]imageBlob, a wire.Asset) error {
	invalid := fail("RELEASE_OCI_EXPORTED_REFERRER_INVALID")
	if d.MediaType != ociManifestType || d.Data != "" || d.Annotations[containerdSubject] != a.ImageID ||
		len(document.Manifests) != 0 || len(document.Layers) == 0 {
		return invalid
	}
	if d.Platform != nil && (d.Platform.OS != "unknown" || d.Platform.Architecture != "unknown" || d.Platform.Variant != "") {
		return invalid
	}
	readLocal := func(desc descriptor) ([]byte, bool) {
		blob, ok := blobs[desc.Digest]
		return blob.data, ok && blob.size == desc.Size && desc.Size > 0 && len(desc.URLs) == 0 && desc.Platform == nil
	}
	config, ok := readLocal(document.Config)
	if !ok {
		return invalid
	}
	switch document.ArtifactType {
	case attestationManifestType:
		subject := document.Subject
		if subject == nil || subject.MediaType != ociManifestType || subject.Digest != a.ImageID || subject.Data != "" {
			return invalid
		}
		if _, ok := readLocal(*subject); !ok || document.Config.MediaType != ociEmptyType || !bytes.Equal(config, []byte("{}")) {
			return invalid
		}
		if inline := document.Config.Data; inline != "" {
			decoded, err := base64.StdEncoding.Strict().DecodeString(inline)
			if err != nil || !bytes.Equal(decoded, config) {
				return invalid
			}
		}
	case "":
		// Older BuildKit attestations represent the raw in-toto blobs as diff IDs.
		var configIdentity struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			RootFS       struct {
				Type    string   `json:"type"`
				DiffIDs []string `json:"diff_ids"`
			} `json:"rootfs"`
		}
		if document.Subject != nil || document.Config.MediaType != "application/vnd.oci.image.config.v1+json" ||
			document.Config.Data != "" || imageJSON(config, &configIdentity) != nil ||
			configIdentity.OS != "unknown" || configIdentity.Architecture != "unknown" ||
			configIdentity.RootFS.Type != "layers" || len(configIdentity.RootFS.DiffIDs) != len(document.Layers) {
			return invalid
		}
		for i, layer := range document.Layers {
			if configIdentity.RootFS.DiffIDs[i] != layer.Digest {
				return invalid
			}
		}
	default:
		return invalid
	}
	for _, layer := range document.Layers {
		raw, ok := readLocal(layer)
		var statement map[string]any
		if !ok || layer.MediaType != inTotoType || layer.Data != "" || wire.UniqueJSON(raw, &statement) != nil || statement == nil {
			return invalid
		}
	}
	return nil
}
