package factory

import (
	"context"
	"path/filepath"

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
type ImageProofResult struct {
	ReleaseID      string     `json:"release_id"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	Asset          wire.Asset `json:"asset"`
}

func VerifyImageProof(ctx context.Context, c Config, proof ImageProof) (ImageProofResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INTERRUPTED")
	}
	if proof.FormatVersion != 1 || proof.AssetID == "" || len(proof.Manifest) < 1 || int64(len(proof.Manifest)) > c.MaximumManifestBytes || len(proof.Signature) < 1 || int64(len(proof.Signature)) > c.MaximumMetadataBytes {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INVALID")
	}
	trust, e := loadTrust(c)
	if e != nil {
		return ImageProofResult{}, e
	}
	if e = verifySignature(proof.Manifest, proof.Signature, trust); e != nil {
		return ImageProofResult{}, e
	}
	var manifest wire.Manifest
	if wire.StrictJSON(proof.Manifest, &manifest) != nil || manifest.Validate(c.MaximumAssets, c.MaximumAssetBytes, c.MaximumTotalBytes, c.AllowTestFixture) != nil {
		return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_INVENTORY_INVALID")
	}
	for _, asset := range manifest.Assets {
		if asset.ID == proof.AssetID {
			if asset.Kind != "image_archive" {
				return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_ASSET_INVALID")
			}
			return ImageProofResult{manifest.ReleaseID, wire.Digest(proof.Manifest), asset}, nil
		}
	}
	return ImageProofResult{}, fail("RELEASE_IMAGE_PROOF_ASSET_MISSING")
}
func PreparedImageProof(ctx context.Context, c Config, directory, archiveSHA, manifestSHA, asset string) (ImageProof, ImageProofResult, error) {
	if _, e := VerifyPrepared(ctx, c, directory, archiveSHA, manifestSHA); e != nil {
		return ImageProof{}, ImageProofResult{}, e
	}
	manifest, e := readRegular(filepath.Join(directory, "manifest.json"), c.MaximumManifestBytes, true)
	if e != nil || wire.Digest(manifest) != manifestSHA {
		return ImageProof{}, ImageProofResult{}, fail("RELEASE_PREPARED_MANIFEST_INVALID")
	}
	signature, e := readRegular(filepath.Join(directory, "signature.json"), c.MaximumMetadataBytes, true)
	if e != nil {
		return ImageProof{}, ImageProofResult{}, e
	}
	proof := ImageProof{1, manifest, signature, asset}
	result, e := VerifyImageProof(ctx, c, proof)
	if e != nil {
		return ImageProof{}, ImageProofResult{}, e
	}
	return proof, result, nil
}
