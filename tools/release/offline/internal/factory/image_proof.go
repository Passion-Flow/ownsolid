package factory

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ImageProof is an actual manufacturer-signed manifest and its selected image.
// It proves neither availability of every release asset nor observation of the
// original packaged archive. No online runtime signing authority is introduced.
type ImageProof struct {
	FormatVersion int    `json:"format_version"`
	Manifest      []byte `json:"manifest_bytes"`
	Signature     []byte `json:"signature_bytes"`
	AssetID       string `json:"asset_id"`
}

// ImageProofResult identifies the exact approved release and image.
type ImageProofResult struct {
	ReleaseID      string     `json:"release_id"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	Asset          wire.Asset `json:"asset"`
	// Profile selects the matching OCI reader in Go consumers.
	Profile string `json:"-"`
}

// verificationConfigForManifest selects the same policy as archive preparation.
// Both callers still authenticate the exact manifest bytes before using its data.
func verificationConfigForManifest(c Config, manifest wire.Manifest) Config {
	if manifest.Profile == CurrentProfile {
		return WithCurrentProfile(c)
	}
	return c
}

// VerifyImageProof checks a transported proof against independently configured public trust.
func VerifyImageProof(ctx context.Context, c Config, proof ImageProof) (ImageProofResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INTERRUPTED")
	}
	if proof.FormatVersion != 1 || proof.AssetID == "" || len(proof.Manifest) < 1 || int64(len(proof.Manifest)) > c.MaximumManifestBytes || len(proof.Signature) < 1 || int64(len(proof.Signature)) > c.MaximumMetadataBytes {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INVALID")
	}
	var manifest wire.Manifest
	if wire.StrictJSON(proof.Manifest, &manifest) != nil {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INVENTORY_INVALID")
	}
	c = verificationConfigForManifest(c, manifest)
	trust, e := loadTrust(c)
	if e != nil {
		return ImageProofResult{}, e
	}
	if e = verifySignature(proof.Manifest, proof.Signature, trust); e != nil {
		return ImageProofResult{}, e
	}
	if c.validateManifest(manifest) != nil {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INVENTORY_INVALID")
	}
	return selectProofImage(manifest, wire.Digest(proof.Manifest), proof.AssetID)
}

func selectProofImage(manifest wire.Manifest, manifestSHA, assetID string) (ImageProofResult, error) {
	for _, asset := range manifest.Assets {
		if asset.ID == assetID {
			if asset.Kind != "image_archive" {
				return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_ASSET_INVALID")
			}
			return ImageProofResult{ReleaseID: manifest.ReleaseID, ManifestSHA256: manifestSHA, Asset: asset, Profile: manifest.Profile}, nil
		}
	}
	return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_ASSET_MISSING")
}

// PreparedImageProof exports the selected image only after verifying the complete
// prepared tree. It returns the same manifest and signature bytes that were checked.
func PreparedImageProof(ctx context.Context, c Config, directory, archiveSHA, manifestSHA, asset string) (ImageProof, ImageProofResult, error) {
	if asset == "" {
		return ImageProof{}, ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INVALID")
	}
	var authority preparedAuthority
	prepared, e := verifyPrepared(ctx, c, directory, archiveSHA, manifestSHA, &authority)
	if e != nil {
		return ImageProof{}, ImageProofResult{}, e
	}
	result, e := selectProofImage(prepared.Manifest, prepared.ManifestSHA256, asset)
	if e != nil {
		return ImageProof{}, ImageProofResult{}, e
	}
	return ImageProof{FormatVersion: 1, Manifest: authority.manifest, Signature: authority.signature, AssetID: asset}, result, nil
}
