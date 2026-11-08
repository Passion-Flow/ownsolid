package factory

import (
	"context"
	"io"
	"path"
	"regexp"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// CurrentProfile is the Linux amd64 profile for the current single-node installer.
const CurrentProfile = "ownsolid-single-node-linux-amd64"

const currentSBOM = "release-sbom"

var currentID = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,95}$`)

// WithCurrentProfile selects the current inventory rules for an explicit current
// release command. The legacy commands retain their existing profile admission.
func WithCurrentProfile(c Config) Config {
	c.current = true
	return c
}

func (c Config) requiredAssets(m wire.Manifest) map[string]string {
	if !c.current {
		return m.RequiredAssets()
	}
	return map[string]string{
		"control-image": "image_archive", "postgres-image": "image_archive",
		"metrics-image": "image_archive", "logs-image": "image_archive",
		"compose": "compose", "installer": "operator", "operator-docs": "runbook",
		currentSBOM: "sbom",
	}
}

func (c Config) validateManifest(m wire.Manifest) error {
	if !c.current {
		if c.RequireRKE2 || c.RequireRKE2Components {
			return fail("RELEASE_RKE2_CURRENT_PROFILE_REQUIRED")
		}
		return m.Validate(c.MaximumAssets, c.MaximumAssetBytes, c.MaximumTotalBytes, c.AllowTestFixture)
	}
	if m.Profile != CurrentProfile {
		return fail("RELEASE_CURRENT_PROFILE_REQUIRED")
	}
	if m.FormatVersion != 1 || !currentID.MatchString(m.ReleaseID) || !wire.ValidSHA256(m.SourceSHA256) ||
		m.SchemaVersion < 1 || m.MinimumPredecessor < 1 || m.MaximumPredecessor < m.MinimumPredecessor ||
		m.MaximumPredecessor > m.SchemaVersion || m.Cumulative != nil || len(m.Capabilities) != 0 {
		return fail("RELEASE_MANIFEST_INVALID")
	}
	if len(m.Prerequisites) == 0 {
		return fail("RELEASE_PREREQUISITES_MISSING")
	}
	for _, prerequisite := range m.Prerequisites {
		if prerequisite == "" || len(prerequisite) > 1024 || strings.ContainsAny(prerequisite, "\x00\r\n") {
			return fail("RELEASE_PREREQUISITE_INVALID")
		}
	}
	if len(m.Assets) == 0 || len(m.Assets) > c.MaximumAssets {
		return fail("RELEASE_ASSET_COUNT_INVALID")
	}
	ids, paths := make(map[string]string, len(m.Assets)), make(map[string]bool, len(m.Assets))
	total := int64(0)
	for _, asset := range m.Assets {
		if !currentID.MatchString(asset.ID) || !wire.SafePath(asset.Path) || ids[asset.ID] != "" || paths[asset.Path] ||
			!wire.ValidSHA256(asset.SHA256) || asset.Size < 1 || asset.Size > c.MaximumAssetBytes || asset.Size > c.MaximumTotalBytes-total {
			return fail("RELEASE_ASSET_INVALID")
		}
		switch asset.Kind {
		case "image_archive":
			if asset.Architecture != "amd64" || !imageDigest(asset.ImageID) || !imageDigest(asset.ImageConfigDigest) {
				return fail("RELEASE_IMAGE_IDENTITY_INVALID")
			}
		case "sbom":
			if (asset.ID != currentSBOM && asset.ID != rke2SPDXID) || asset.Size > c.MaximumMetadataBytes {
				return fail("RELEASE_SBOM_INVALID")
			}
		case "compose", "operator", "runbook", "license", "chart", "model", "plugin", "font", "dependency_lock", rke2BinaryKind, rke2ImageSetKind:
		default:
			return fail("RELEASE_ASSET_KIND_UNSUPPORTED")
		}
		if asset.Kind != "image_archive" && asset.Kind != rke2BinaryKind && asset.Kind != rke2ImageSetKind && (asset.Architecture != "" || asset.ImageID != "" || asset.ImageConfigDigest != "") {
			return fail("RELEASE_ASSET_IMAGE_FIELDS_INVALID")
		}
		total += asset.Size
		ids[asset.ID], paths[asset.Path] = asset.Kind, true
	}
	for _, asset := range m.Assets {
		for parent := path.Dir(asset.Path); parent != "."; parent = path.Dir(parent) {
			if paths[parent] {
				return fail("RELEASE_ASSET_PATH_CONFLICT")
			}
		}
		if asset.Kind == "license" {
			if len(asset.Licenses) != 0 {
				return fail("RELEASE_LICENSE_INVALID")
			}
			continue
		}
		if len(asset.Licenses) == 0 {
			return fail("RELEASE_LICENSE_MISSING")
		}
		seen := make(map[string]bool, len(asset.Licenses))
		for _, notice := range asset.Licenses {
			if ids[notice] != "license" || seen[notice] {
				return fail("RELEASE_LICENSE_INVALID")
			}
			seen[notice] = true
		}
	}
	for id, kind := range c.requiredAssets(m) {
		if ids[id] != kind {
			return fail("RELEASE_REQUIRED_ASSET_MISSING")
		}
	}
	return validateRKE2Inventory(m, c.RequireRKE2, c.RequireRKE2Components)
}

func imageDigest(value string) bool {
	hash, ok := strings.CutPrefix(value, "sha256:")
	return ok && wire.ValidSHA256(hash)
}

// checkManifestAsset retains all byte checks for both profiles and validates the
// current release's SPDX package checksums before publishing or preparing it.
func checkManifestAsset(ctx context.Context, r io.Reader, w io.Writer, a wire.Asset, m wire.Manifest, c Config) (string, error) {
	if !c.current || a.ID != currentSBOM {
		return checkAsset(ctx, r, w, a, c)
	}
	raw, err := io.ReadAll(io.LimitReader(checkedReader{ctx, r}, a.Size+1))
	if err != nil || int64(len(raw)) != a.Size {
		return "", fail("RELEASE_ASSET_SIZE_INVALID")
	}
	if err := checkSBOM(raw, m.Assets); err != nil {
		return "", err
	}
	if w != nil {
		if _, err := w.Write(raw); err != nil {
			return "", fail("RELEASE_ARCHIVE_WRITE_FAILED")
		}
	}
	return wire.Digest(raw), nil
}

func checkSBOM(raw []byte, assets []wire.Asset) error {
	var document struct {
		Version       string                 `json:"spdxVersion"`
		License       string                 `json:"dataLicense"`
		ID            string                 `json:"SPDXID"`
		Name          string                 `json:"name"`
		Namespace     string                 `json:"documentNamespace"`
		References    []spdxExternalDocument `json:"externalDocumentRefs"`
		Relationships []spdxRelationship     `json:"relationships"`
		Packages      []struct {
			ID        string         `json:"SPDXID"`
			Name      string         `json:"name"`
			Checksums []spdxChecksum `json:"checksums"`
		} `json:"packages"`
	}
	if wire.UniqueJSON(raw, &document) != nil || document.Version != "SPDX-2.3" ||
		document.License != "CC0-1.0" || document.ID != "SPDXRef-DOCUMENT" ||
		document.Name == "" || document.Namespace == "" || len(document.Packages) == 0 {
		return fail("RELEASE_SBOM_INVALID")
	}
	ids := make(map[string]bool, len(document.Packages))
	checksums := make(map[string]bool)
	for _, component := range document.Packages {
		if !strings.HasPrefix(component.ID, "SPDXRef-") || component.ID == document.ID ||
			component.Name == "" || ids[component.ID] {
			return fail("RELEASE_SBOM_INVALID")
		}
		ids[component.ID] = true
		for _, checksum := range component.Checksums {
			if checksum.Algorithm == "SHA256" {
				value := strings.ToLower(checksum.Value)
				if !wire.ValidSHA256(value) {
					return fail("RELEASE_SBOM_INVALID")
				}
				checksums[value] = true
			}
		}
	}
	for _, asset := range assets {
		if asset.ID != currentSBOM && !checksums[asset.SHA256] {
			return fail("RELEASE_SBOM_ASSET_CHECKSUM_MISSING")
		}
	}
	return checkRKE2SPDXLink(assets, document.References, document.Relationships)
}
