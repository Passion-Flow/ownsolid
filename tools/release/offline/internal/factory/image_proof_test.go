package factory

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	return c, manifest, signFixtureImageProof(signed, key, asset.ID)
}

func signFixtureImageProof(raw []byte, key ed25519.PrivateKey, asset string) ImageProof {
	signature := wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(key.Public().(ed25519.PublicKey)), ManifestSHA256: wire.Digest(raw), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(signatureDomain), raw...)))}
	return ImageProof{1, raw, Encode(signature), asset}
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
	if e != nil || got.Asset.SHA256 != asset.SHA256 || proof.AssetID != asset.ID || got.Profile != wire.FixtureProfile {
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

func TestPreparedImageProofMatchesCurrentPreparation(t *testing.T) {
	t.Run("current-base", func(t *testing.T) { testCurrentPreparedImageProof(t, "") })
	t.Run("signed-site-group", func(t *testing.T) {
		registry := os.Getenv("OWNSOLID_PROOF_REGISTRY_ARCHIVE")
		if registry == "" {
			t.Skip("needs the pinned Distribution Linux amd64 OCI archive")
		}
		testCurrentPreparedImageProof(t, registry)
	})
}

func testCurrentPreparedImageProof(t *testing.T, registry string) {
	t.Helper()
	signerParent := os.Getenv("OWNSOLID_CURRENT_FACTORY_KEY_DIR")
	if signerParent == "" {
		t.Skip("needs a private fixture signer directory outside the release mount")
	}
	c, _, key := setup(t)
	signer, err := os.MkdirTemp(signerParent, "image-proof-signer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(signer); err != nil {
			t.Error(err)
		}
	})
	c.PrivateKeyFile = filepath.Join(signer, "ephemeral-release-signer")
	put(t, c.PrivateKeyFile, []byte(base64.StdEncoding.EncodeToString(key)))
	c.AllowTestFixture = false
	c.MaximumAssetBytes, c.MaximumTotalBytes = 64<<20, 128<<20
	c.MinimumFreeBytes = 1 << 20
	root := rootTestKey(t)
	put(t, c.TrustFile, rootTestMetadata(1, time.Now().Add(time.Hour), key, root))
	_, fixtureImage, source, expanded := importFixture(t, "")
	image, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	m := currentInventory()
	putAsset := func(a *wire.Asset, raw []byte) {
		t.Helper()
		a.Size, a.SHA256 = int64(len(raw)), wire.Digest(raw)
		put(t, filepath.Join(c.ArtifactRoot, a.Path), raw)
	}
	for i := range m.Assets {
		a := &m.Assets[i]
		switch a.Kind {
		case "image_archive":
			a.ImageID, a.ImageConfigDigest = fixtureImage.ImageID, fixtureImage.ImageConfigDigest
			putAsset(a, image)
		case "sbom":
			continue
		default:
			putAsset(a, []byte("Fixture payload for "+a.ID))
		}
	}
	if registry != "" {
		raw, err := os.ReadFile(registry)
		if err != nil {
			t.Fatal(err)
		}
		a := wire.Asset{ID: RegistryImageAsset, Path: RegistryImageAsset, Kind: "image_archive",
			Architecture: "amd64", ImageID: registryManifest, ImageConfigDigest: registryConfig, Licenses: []string{"test-notice"}}
		putAsset(&a, raw)
		m.Prerequisites = append(m.Prerequisites, RegistryPrerequisite)
		m.Assets = append(m.Assets, a)
	}
	for i := range m.Assets {
		if m.Assets[i].ID == currentSBOM {
			putAsset(&m.Assets[i], testSPDX(m.Assets))
		}
	}
	raw := Encode(m)
	put(t, c.ManifestFile, raw)
	current := WithCurrentProfile(c)
	built, err := Build(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(filepath.Dir(c.OutputFile), "prepared-current")
	prepared, err := Unpack(t.Context(), current, c.OutputFile, directory, "", "")
	if err != nil || prepared.ManifestSHA256 != built.ManifestSHA256 || prepared.ArchiveSHA256 != built.ArchiveSHA256 {
		t.Fatal(prepared, err)
	}
	// Consumers only have verification configuration and a completed preparation.
	c.PrivateKeyFile, c.ManifestFile = "", ""
	const selected = "control-image"
	recheck, err := VerifyPrepared(t.Context(), c, directory, built.ArchiveSHA256, built.ManifestSHA256)
	if err != nil || recheck.Profile != CurrentProfile {
		t.Fatalf("catalog and recovery recheck: %s %v", recheck.Profile, err)
	}
	proof, result, err := PreparedImageProof(t.Context(), c, directory, built.ArchiveSHA256, built.ManifestSHA256, selected)
	if err != nil || !bytes.Equal(proof.Manifest, raw) || result.ManifestSHA256 != built.ManifestSHA256 ||
		result.Profile != CurrentProfile || !reflect.DeepEqual(result.Asset, m.Assets[0]) {
		t.Fatal(result, err)
	}
	checked, err := VerifyImageProof(t.Context(), c, proof)
	if err != nil || !reflect.DeepEqual(checked, result) {
		t.Fatal(checked, err)
	}
	oci, err := OpenImageSource(t.Context(), current, filepath.Join(directory, checked.Asset.Path), checked.Asset, expanded)
	if err != nil {
		t.Fatal(err)
	}
	defer oci.Close()
	if oci.Inventory().ManifestDigest != checked.Asset.ImageID || oci.Inventory().ConfigDigest != checked.Asset.ImageConfigDigest {
		t.Fatal("the selected OCI content changed its signed identity")
	}
	t.Logf("prepared and proved %s, manifest %s, archive %s, selected image %s", m.Profile, result.ManifestSHA256, built.ArchiveSHA256, result.Asset.ImageID)

	for _, test := range []struct{ fault, code string }{
		{"missing-asset", "RELEASE_PREPARED_FILE_MISSING"},
		{"tampered-asset", "RELEASE_PREPARED_FILE_CHANGED"},
		{"manifest", "RELEASE_PREPARED_MANIFEST_INVALID"},
		{"signature", "RELEASE_SIGNATURE_INVALID"},
		{"expired-trust", "RELEASE_TRUST_ROOT_EXPIRED"},
		{"unprotected-root", "RELEASE_PRIVATE_PREPARED_ROOT_REQUIRED"},
		{"wrong-archive", "RELEASE_PREPARED_MARKER_INVALID"},
		{"wrong-manifest", "RELEASE_PREPARED_MARKER_INVALID"},
	} {
		t.Run(test.fault, func(t *testing.T) {
			archiveSHA, manifestSHA := built.ArchiveSHA256, built.ManifestSHA256
			payload := filepath.Join(directory, "compose")
			modify := func(name string, edit func([]byte)) {
				t.Helper()
				before, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { put(t, name, before) })
				edit(bytes.Clone(before))
			}
			switch test.fault {
			case "missing-asset":
				modify(payload, func([]byte) {
					if err := os.Remove(payload); err != nil {
						t.Fatal(err)
					}
				})
			case "tampered-asset":
				modify(payload, func(raw []byte) { raw[0] ^= 1; put(t, payload, raw) })
			case "manifest", "signature":
				file := filepath.Join(directory, test.fault+".json")
				modify(file, func(raw []byte) {
					if test.fault == "signature" {
						raw = bytes.Replace(raw, []byte("Ed25519"), []byte("invalid"), 1)
					} else {
						raw = append(raw, ' ')
					}
					put(t, file, raw)
				})
			case "expired-trust":
				modify(c.TrustFile, func([]byte) { put(t, c.TrustFile, rootTestMetadata(1, time.Now().Add(-time.Hour), key, root)) })
				if result, err := VerifyImageProof(t.Context(), c, proof); ErrorCode(err) != test.code || !reflect.DeepEqual(result, ImageProofResult{}) {
					t.Fatal("cached proof did not enforce the same root expiry", result, err)
				}
			case "unprotected-root":
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(directory, 0700); err != nil {
						t.Error(err)
					}
				})
			case "wrong-archive":
				archiveSHA = strings.Repeat("0", 64)
			case "wrong-manifest":
				manifestSHA = strings.Repeat("0", 64)
			}
			proof, result, err := PreparedImageProof(t.Context(), c, directory, archiveSHA, manifestSHA, selected)
			if ErrorCode(err) != test.code || !reflect.DeepEqual(proof, ImageProof{}) || !reflect.DeepEqual(result, ImageProofResult{}) {
				t.Fatal("unapproved preparation produced a proof", result, err)
			}
		})
	}
	// A legitimately signed manifest missing its required Registry group still refuses.
	if registry == "" {
		m.Prerequisites = append(m.Prerequisites, RegistryPrerequisite)
	} else {
		m.Assets = m.Assets[:len(m.Assets)-1]
	}
	invalid := signFixtureImageProof(Encode(m), key, selected)
	if result, err := VerifyImageProof(t.Context(), c, invalid); ErrorCode(err) != "RELEASE_IMAGE_PROOF_INVENTORY_INVALID" || !reflect.DeepEqual(result, ImageProofResult{}) {
		t.Fatal("an incomplete signed site group was admitted", result, err)
	}
}
