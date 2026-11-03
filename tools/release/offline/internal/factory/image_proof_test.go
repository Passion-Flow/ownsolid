package factory

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"reflect"
	"testing"
)

// This key signs an explicit synthetic fixture. It is never a runtime signer or
// evidence that any production manufacturer image/release has been approved.
func imageProofFixture(t *testing.T) (Config, wire.Manifest, ImageProof) {
	t.Helper()
	_, asset, filename, _ := importFixture(t, "")
	c, manifest, key := setup(t)
	raw, e := os.ReadFile(filename)
	if e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(c.ArtifactRoot, asset.Path), raw)
	manifest.Assets = append(manifest.Assets, asset)
	// Preserve pretty printed signed bytes through JSON proof transport.
	signed, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	signature := wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(key.Public().(ed25519.PublicKey)), ManifestSHA256: wire.Digest(signed), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(signatureDomain), signed...)))}
	return c, manifest, ImageProof{1, signed, Encode(signature), asset.ID}
}
func TestSignedImageSubsetProofRetainsExactManufacturerBytes(t *testing.T) {
	c, manifest, proof := imageProofFixture(t)
	encoded := Encode(proof)
	var transported ImageProof
	if wire.StrictJSON(encoded, &transported) != nil || !reflect.DeepEqual(proof, transported) {
		t.Fatal("signed bytes changed in JSON transport")
	}
	got, e := VerifyImageProof(t.Context(), c, transported)
	if e != nil || got.ReleaseID != manifest.ReleaseID || got.ManifestSHA256 != wire.Digest(proof.Manifest) || !reflect.DeepEqual(got.Asset, manifest.Assets[len(manifest.Assets)-1]) {
		t.Fatal(got, e)
	}
	// Subset verification needs the actual trusted signature, not all asset files.
	if e = os.Remove(filepath.Join(c.ArtifactRoot, manifest.Assets[0].Path)); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyImageProof(t.Context(), c, proof); e != nil {
		t.Fatal(e)
	}
}
func TestSignedImageSubsetProofRefusalsReturnZero(t *testing.T) {
	for _, fault := range []string{"missing", "wrong-asset", "not-image", "signature", "manifest", "version", "cancelled", "missing-trust", "oversize"} {
		t.Run(fault, func(t *testing.T) {
			c, _, proof := imageProofFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch fault {
			case "missing":
				proof = ImageProof{}
			case "wrong-asset":
				proof.AssetID = "not-in-the-signed-release"
			case "not-image":
				proof.AssetID = "license"
			case "signature":
				proof.Signature[len(proof.Signature)/2] ^= 1
			case "manifest":
				proof.Manifest = append(proof.Manifest, ' ')
			case "version":
				proof.FormatVersion = 2
			case "cancelled":
				cancel()
			case "missing-trust":
				if e := os.Remove(c.TrustFile); e != nil {
					t.Fatal(e)
				}
			case "oversize":
				c.MaximumManifestBytes = int64(len(proof.Manifest)) - 1
			}
			got, e := VerifyImageProof(ctx, c, proof)
			if e == nil || !reflect.DeepEqual(got, ImageProofResult{}) {
				t.Fatal("invalid proof produced a projection", got, e)
			}
		})
	}
}
func TestPreparedImageProofRequiresWholePreparedOrigin(t *testing.T) {
	c, manifest, _ := imageProofFixture(t)
	put(t, c.ManifestFile, Encode(manifest))
	result, e := Build(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(filepath.Dir(c.OutputFile), "prepared-image")
	if _, e = Unpack(t.Context(), c, c.OutputFile, directory, result.ArchiveSHA256, result.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
	asset := manifest.Assets[len(manifest.Assets)-1]
	proof, got, e := PreparedImageProof(t.Context(), c, directory, result.ArchiveSHA256, result.ManifestSHA256, asset.ID)
	if e != nil || got.Asset.SHA256 != asset.SHA256 || proof.AssetID != asset.ID {
		t.Fatal(got, e)
	}
	// The control-plane preparation path still checks the full original inventory.
	if e = os.Remove(filepath.Join(directory, manifest.Assets[0].Path)); e != nil {
		t.Fatal(e)
	}
	if proof, got, e = PreparedImageProof(t.Context(), c, directory, result.ArchiveSHA256, result.ManifestSHA256, asset.ID); e == nil || proof.FormatVersion != 0 || !reflect.DeepEqual(got, ImageProofResult{}) {
		t.Fatal("partial prepared release admitted", got, e)
	}
}
