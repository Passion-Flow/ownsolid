package factory

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ImageIdentity pairs an approved runtime manifest with its exact image config.
type ImageIdentity struct {
	ManifestDigest string
	ConfigDigest   string
}

// OpenImageSelection checks the complete flat OCI inventory against the caller's
// approved image identities and expands only the selected image's layers.
func OpenImageSelection(ctx context.Context, c Config, filename string, selected wire.Asset, approved []ImageIdentity, maximumExpandedBytes int64) (*ImageSource, error) {
	if !c.current || selected.Architecture != "amd64" || len(approved) == 0 || len(approved) > c.MaximumArchiveEntries {
		return nil, fail("RELEASE_OCI_IMAGE_SELECTION_INVALID")
	}
	selection := make(map[string]string, len(approved))
	for _, image := range approved {
		if !imageDigest(image.ManifestDigest) || !imageDigest(image.ConfigDigest) || selection[image.ManifestDigest] != "" {
			return nil, fail("RELEASE_OCI_IMAGE_SELECTION_INVALID")
		}
		selection[image.ManifestDigest] = image.ConfigDigest
	}
	if selected.ImageConfigDigest == "" || selection[selected.ImageID] != selected.ImageConfigDigest {
		return nil, fail("RELEASE_OCI_IMAGE_SELECTION_INVALID")
	}
	return openImageSource(ctx, c, filename, selected, maximumExpandedBytes, selection)
}

func catalogAMD64(os, arch, variant string, features []string) bool {
	return os == "linux" && arch == "amd64" && (variant == "" || variant == "v1") && len(features) == 0
}

func validateImageSelection(root imageDocument, compat []byte, blobs map[string]imageBlob, selected wire.Asset, c Config, approved map[string]string, inventory *ImageInventory) error {
	const ociIndex = "application/vnd.oci.image.index.v1+json"
	if len(compat) != 0 || len(root.Manifests) != len(approved) ||
		(root.MediaType != "" && root.MediaType != ociIndex) ||
		root.Subject != nil || root.ArtifactType != "" || root.Config.Digest != "" || root.Layers != nil {
		return fail("RELEASE_OCI_IMAGE_SET_INVALID")
	}
	seen := make(map[string]bool, len(approved))
	used := make(map[string]bool, len(blobs))
	for _, manifest := range root.Manifests {
		configDigest := approved[manifest.Digest]
		if configDigest == "" || seen[manifest.Digest] || manifest.MediaType != ociManifestType ||
			len(manifest.URLs) != 0 || manifest.Data != "" || manifest.Annotations[attestationReferenceType] != "" ||
			manifest.Annotations[containerdSubject] != "" {
			return fail("RELEASE_OCI_IMAGE_SET_INVALID")
		}
		if p := manifest.Platform; p != nil && !catalogAMD64(p.OS, p.Architecture, p.Variant, p.OSFeatures) {
			return fail("RELEASE_OCI_DESCRIPTOR_PLATFORM_INVALID")
		}
		blob, ok := blobs[manifest.Digest]
		if !ok || blob.size != manifest.Size || manifest.Size < 1 {
			return fail("RELEASE_OCI_DESCRIPTOR_INVALID")
		}
		var document imageDocument
		if imageJSON(blob.data, &document) != nil || document.SchemaVersion != 2 ||
			(document.MediaType != "" && document.MediaType != ociManifestType) ||
			document.Subject != nil || document.ArtifactType != "" || document.Manifests != nil || document.Layers == nil {
			return fail("RELEASE_OCI_MANIFEST_INVALID")
		}
		var target *ImageInventory
		if manifest.Digest == selected.ImageID {
			target = inventory
		}
		if err := readImageManifest(manifest, document, blobs, configDigest, selected.Architecture, c, true, target); err != nil {
			return err
		}
		seen[manifest.Digest] = true
		used[manifest.Digest], used[configDigest] = true, true
		for _, layer := range document.Layers {
			used[layer.Digest] = true
		}
	}
	if len(used) != len(blobs) {
		return fail("RELEASE_OCI_IMAGE_SET_INVALID")
	}
	return nil
}
