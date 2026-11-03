package factory

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func syntheticLicenseTrust() []byte {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{37}, ed25519.SeedSize))
	return Encode(licensewire.Trust{FormatVersion: 1, Algorithm: licensewire.Algorithm, KeyID: "synthetic-manufacturer", PublicKey: base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))})
}

func TestM19FactoryValidatesPublicTrustBeforeCopying(t *testing.T) {
	raw := syntheticLicenseTrust()
	var output bytes.Buffer
	a := wire.Asset{Kind: wire.LicenseTrustAssetKind, Size: int64(len(raw))}
	hash, e := checkAsset(context.Background(), bytes.NewReader(raw), &output, a, Config{})
	if e != nil || hash != wire.Digest(raw) || !bytes.Equal(output.Bytes(), raw) {
		t.Fatal("public trust copy", e)
	}
	private := Encode(licensewire.PrivateKey{FormatVersion: 1, Algorithm: licensewire.Algorithm, KeyID: "synthetic-manufacturer", Seed: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{37}, 32))})
	for name, body := range map[string][]byte{"private-seed": private, "release-trust": []byte(`{"keys":{}}`), "duplicate": bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1), "short-key": bytes.Replace(raw, []byte(base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{37}, 32)).Public().(ed25519.PublicKey))), []byte("YQ=="), 1), "oversize": bytes.Repeat([]byte("x"), licensewire.MaximumKeyBytes+1)} {
		t.Run(name, func(t *testing.T) {
			output.Reset()
			a.Size = int64(len(body))
			if _, e := checkAsset(context.Background(), bytes.NewReader(body), &output, a, Config{}); e == nil || output.Len() != 0 {
				t.Fatal("invalid trust copied", e)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output.Reset()
	a.Size = int64(len(raw))
	if _, e := checkAsset(ctx, bytes.NewReader(raw), &output, a, Config{}); e == nil || output.Len() != 0 {
		t.Fatal("cancelled trust copied")
	}
	a.Size++
	if _, e := checkAsset(context.Background(), bytes.NewReader(raw), &output, a, Config{}); e == nil || output.Len() != 0 {
		t.Fatal("size mismatch copied")
	}
}

func TestM19FactoryActualPublicTrustArchiveAndMissingAssetRefusal(t *testing.T) {
	c, m, _ := setup(t)
	raw := syntheticLicenseTrust()
	a := wire.Asset{ID: wire.LicenseTrustAssetID, Path: "public-license-trust.json", Kind: wire.LicenseTrustAssetKind, Size: int64(len(raw)), SHA256: wire.Digest(raw), Licenses: []string{"license"}}
	m.Assets = append(m.Assets, a)
	put(t, filepath.Join(c.ArtifactRoot, a.Path), raw)
	put(t, c.ManifestFile, Encode(m))
	result, e := Build(context.Background(), c)
	if e != nil {
		t.Fatal("actual fixture build", e)
	}
	if _, e = Inspect(context.Background(), c, c.OutputFile); e != nil {
		t.Fatal("actual fixture inspect", e)
	}
	destination := filepath.Join(filepath.Dir(c.OutputFile), "prepared-license")
	if _, e = Unpack(context.Background(), c, c.OutputFile, destination, result.ArchiveSHA256, result.ManifestSHA256); e != nil {
		t.Fatal("actual fixture unpack", e)
	}
	if _, e = VerifyPrepared(context.Background(), c, destination, result.ArchiveSHA256, result.ManifestSHA256); e != nil {
		t.Fatal("actual prepared readback", e)
	}
	actual, e := os.ReadFile(filepath.Join(destination, a.Path))
	if e != nil || !bytes.Equal(actual, raw) {
		t.Fatal("actual public trust bytes", e)
	}
	put(t, filepath.Join(destination, a.Path), bytes.Replace(raw, []byte("synthetic-manufacturer"), []byte("synthetic-manufacturex"), 1))
	if _, e = VerifyPrepared(context.Background(), c, destination, result.ArchiveSHA256, result.ManifestSHA256); e == nil {
		t.Fatal("changed prepared trust admitted")
	}
	if e = os.Remove(filepath.Join(c.ArtifactRoot, a.Path)); e != nil {
		t.Fatal(e)
	}
	validation, e := Validate(context.Background(), c)
	if e == nil || len(validation.Issues) != 1 || validation.Issues[0].AssetID != a.ID {
		t.Fatal("missing actual trust file admitted", e)
	}
	c.OutputFile = filepath.Join(filepath.Dir(c.OutputFile), "missing-trust.tar.gz")
	if _, e = Build(context.Background(), c); e == nil {
		t.Fatal("incomplete fixture package reported success")
	}
	if _, e = os.Lstat(c.OutputFile); !os.IsNotExist(e) {
		t.Fatal("incomplete final archive published")
	}
	if strings.Contains(validation.Issues[0].Code, string(raw)) {
		t.Fatal("public input echoed into diagnostic")
	}
}

func TestM19FactoryReportsMissingCurrentReleaseAssetsBeforePackaging(t *testing.T) {
	c, m, _ := setup(t)
	m.Profile = wire.PlatformProfile
	m.SchemaVersion = wire.LicenseSchemaVersion
	m.MaximumPredecessor = wire.LicenseSchemaVersion
	m.Prerequisites = []string{"Synthetic metadata only; actual complete assets are unavailable"}
	m.Capabilities = nil
	for i := 1; i <= 19; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	m.Assets = nil
	for id, kind := range wire.RequiredAssets(m.Profile) {
		a := wire.Asset{ID: id, Path: id, Kind: kind, Size: 1, SHA256: strings.Repeat("a", 64), Licenses: []string{}}
		if kind != "license" {
			a.Licenses = []string{"platform-license"}
		}
		if kind == "image_archive" {
			a.ImageID = "sha256:" + strings.Repeat("b", 64)
			a.ImageConfigDigest = "sha256:" + strings.Repeat("c", 64)
			a.Architecture = "amd64"
		}
		m.Assets = append(m.Assets, a)
	}
	put(t, c.ManifestFile, Encode(m))
	v, e := Validate(context.Background(), c)
	if e == nil || e.Error() != "RELEASE_REQUIRED_ASSET_MISSING" || len(v.Issues) != 2 {
		t.Fatal("missing declared current assets not reported", e, v.Issues)
	}
	for _, issue := range v.Issues {
		if issue.AssetID != wire.LicenseTrustAssetID && issue.AssetID != wire.LicenseValidationAssetID {
			t.Fatal("wrong missing asset identity")
		}
	}
	if _, e = Build(context.Background(), c); e == nil {
		t.Fatal("incomplete current profile package reported success")
	}
	if _, e = os.Lstat(c.OutputFile); !os.IsNotExist(e) {
		t.Fatal("incomplete current final archive published")
	}
}
