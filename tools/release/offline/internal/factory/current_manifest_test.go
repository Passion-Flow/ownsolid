package factory

import (
	"archive/tar"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func currentInventory() wire.Manifest {
	m := wire.Manifest{
		FormatVersion: 1, ReleaseID: "test-release", Profile: CurrentProfile, SourceSHA256: wire.Digest([]byte("source")),
		SchemaVersion: 52, MinimumPredecessor: 52, MaximumPredecessor: 52,
		Prerequisites: []string{"Linux amd64 and local Docker Engine with Compose"}, Capabilities: []string{},
	}
	for _, item := range []struct{ id, kind string }{
		{"control-image", "image_archive"}, {"postgres-image", "image_archive"},
		{"metrics-image", "image_archive"}, {"logs-image", "image_archive"},
		{"compose", "compose"}, {"installer", "operator"}, {"operator-docs", "runbook"},
		{"test-notice", "license"}, {currentSBOM, "sbom"},
	} {
		a := wire.Asset{ID: item.id, Path: item.id, Kind: item.kind, Size: 64, SHA256: wire.Digest([]byte(item.id)), Licenses: []string{}}
		if item.kind == "image_archive" {
			a.Architecture, a.ImageID, a.ImageConfigDigest = "amd64", "sha256:"+a.SHA256, "sha256:"+a.SHA256
		}
		if item.kind != "license" {
			a.Licenses = []string{"test-notice"}
		}
		m.Assets = append(m.Assets, a)
	}
	return m
}

func TestCurrentManifestDoesNotChangeLegacyAdmission(t *testing.T) {
	legacy, err := LoadConfig([]byte("{}"), nil)
	if err != nil {
		t.Fatal(err)
	}
	current := WithCurrentProfile(legacy)
	m := currentInventory()
	if err := current.validateManifest(m); err != nil {
		t.Fatal(err)
	}
	if err := legacy.validateManifest(m); err == nil {
		t.Fatal("legacy consumer accepted current profile")
	}
	for _, test := range []struct {
		name, code string
		change     func(*wire.Manifest)
	}{
		{"fixture", "RELEASE_CURRENT_PROFILE_REQUIRED", func(m *wire.Manifest) { m.Profile = wire.FixtureProfile }},
		{"image architecture", "RELEASE_IMAGE_IDENTITY_INVALID", func(m *wire.Manifest) { m.Assets[0].Architecture = "arm64" }},
		{"missing image", "RELEASE_REQUIRED_ASSET_MISSING", func(m *wire.Manifest) { m.Assets = m.Assets[1:] }},
		{"path collision", "RELEASE_ASSET_PATH_CONFLICT", func(m *wire.Manifest) { m.Assets[0].Path = m.Assets[1].Path + "/child" }},
		{"notice", "RELEASE_LICENSE_INVALID", func(m *wire.Manifest) { m.Assets[0].Licenses = []string{"missing"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := m
			candidate.Assets = slices.Clone(m.Assets)
			test.change(&candidate)
			if err := current.validateManifest(candidate); ErrorCode(err) != test.code {
				t.Fatalf("want %s, got %v", test.code, err)
			}
		})
	}
}

func testSPDX(assets []wire.Asset) []byte {
	packages := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		if asset.ID == currentSBOM {
			continue
		}
		packages = append(packages, map[string]any{
			"name": asset.ID, "SPDXID": "SPDXRef-" + asset.ID, "downloadLocation": "NOASSERTION",
			"filesAnalyzed": false, "checksums": []map[string]string{{"algorithm": "SHA256", "checksumValue": asset.SHA256}},
		})
	}
	return Encode(map[string]any{
		"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT",
		"name": "ownsolid integration fixture", "documentNamespace": "https://ownsolid.test/spdx/integration-fixture",
		"creationInfo": map[string]any{"created": "2026-11-08T00:00:00Z", "creators": []string{"Tool: ownsolid-integration-test"}},
		"packages":     packages,
	})
}

func TestCurrentSBOMMustBindEveryPayload(t *testing.T) {
	m := currentInventory()
	if err := checkSBOM(testSPDX(m.Assets), m.Assets); err != nil {
		t.Fatal(err)
	}
	if err := checkSBOM(testSPDX(m.Assets[1:]), m.Assets); ErrorCode(err) != "RELEASE_SBOM_ASSET_CHECKSUM_MISSING" {
		t.Fatalf("an SBOM without the first image was accepted: %v", err)
	}
}

// TestManufactureCurrentRelease stages a signed fixture from real Docker exports
// for the installer's isolated acceptance test. Its notices and SBOM are test data.
func TestManufactureCurrentRelease(t *testing.T) {
	stage := os.Getenv("OWNSOLID_CURRENT_FACTORY_STAGE")
	keyDir := os.Getenv("OWNSOLID_CURRENT_FACTORY_KEY_DIR")
	if stage == "" || keyDir == "" {
		t.Skip("needs Docker exports on a real mount and a separate private directory for the test signer")
	}
	c, err := LoadConfig([]byte("{}"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c = WithCurrentProfile(c)
	c.MountRoot = filepath.Dir(stage)
	c.ArtifactRoot = filepath.Join(stage, "assets")
	c.ManifestFile = filepath.Join(stage, "manifest.json")
	c.TrustFile = filepath.Join(stage, "release-trust.json")
	c.PrivateKeyFile = filepath.Join(keyDir, "ephemeral-release-signer")
	c.OutputFile = filepath.Join(stage, "ownsolid.tar.gz")
	c.MinimumFreeBytes = 1 << 20
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.PrivateKeyFile, []byte(base64.StdEncoding.EncodeToString(private)))
	put(t, c.TrustFile, Encode(Trust{Keys: map[string]string{wire.Digest(public): base64.StdEncoding.EncodeToString(public)}}))
	m := currentInventory()
	m.SchemaVersion, err = strconv.Atoi(os.Getenv("OWNSOLID_CURRENT_FACTORY_SCHEMA"))
	if err != nil || m.SchemaVersion <= 0 {
		t.Fatal("expected the exported control image's schema version")
	}
	m.MinimumPredecessor, m.MaximumPredecessor = m.SchemaVersion, m.SchemaVersion
	if source := os.Getenv("OWNSOLID_CURRENT_FACTORY_SOURCE_SHA256"); wire.ValidSHA256(source) {
		m.SourceSHA256 = source
	}
	for n := range m.Assets {
		asset := &m.Assets[n]
		if asset.ID == currentSBOM {
			put(t, filepath.Join(c.ArtifactRoot, asset.Path), testSPDX(m.Assets))
		}
		filename := filepath.Join(c.ArtifactRoot, asset.Path)
		file, err := os.Open(filename)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.New()
		size, err := io.Copy(sum, file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		asset.Size, asset.SHA256 = size, hex.EncodeToString(sum.Sum(nil))
		if asset.Kind == "image_archive" {
			asset.ImageID, asset.ImageConfigDigest = testDockerExportIdentity(t, filename)
		}
	}
	put(t, c.ManifestFile, Encode(m))
	result, err := Build(t.Context(), c)
	if err != nil {
		if validation, _ := Validate(t.Context(), c); len(validation.Issues) > 0 {
			t.Logf("asset issues: %+v", validation.Issues)
		}
		t.Fatal(err)
	}
	t.Logf("signed archive %s, manifest %s, profile %s", result.ArchiveSHA256, result.ManifestSHA256, result.Profile)
	if _, err := Inspect(t.Context(), c, c.OutputFile); err != nil {
		t.Fatal(err)
	}
}

func testDockerExportIdentity(t *testing.T, filename string) (string, string) {
	t.Helper()
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var config struct {
		Config string `json:"Config"`
	}
	manifests := map[string]string{}
	tarball := tar.NewReader(f)
	for {
		header, err := tarball.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch header.Name {
		case "manifest.json":
			var entries []struct {
				Config string `json:"Config"`
			}
			if raw, err := io.ReadAll(tarball); err != nil || wire.UniqueJSON(raw, &entries) != nil || len(entries) != 1 {
				t.Fatal("expected one Docker archive image")
			}
			config = entries[0]
		default:
			digest, blob := strings.CutPrefix(header.Name, "blobs/sha256/")
			if !blob || header.Size > 1<<20 {
				continue
			}
			var document imageDocument
			if raw, err := io.ReadAll(tarball); err == nil && imageJSON(raw, &document) == nil &&
				document.SchemaVersion == 2 && document.Config.Digest != "" && len(document.Layers) > 0 {
				if previous := manifests[document.Config.Digest]; previous != "" && previous != digest {
					t.Fatal("multiple exported manifests share the same config")
				}
				manifests[document.Config.Digest] = digest
			}
		}
	}
	configHash, ok := strings.CutPrefix(config.Config, "blobs/sha256/")
	configDigest := "sha256:" + configHash
	manifest := manifests[configDigest]
	if !ok || !wire.ValidSHA256(configHash) || !wire.ValidSHA256(manifest) {
		t.Fatal("export is missing the Docker configuration or its runnable OCI manifest")
	}
	return "sha256:" + manifest, configDigest
}
