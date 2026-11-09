package apps

import (
	"archive/tar"
	"bytes"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type imageFixture struct {
	files                map[string][]byte
	image, config, layer string
}

func newImageFixture(t *testing.T, architecture string, remoteLayer bool) imageFixture {
	t.Helper()
	return imageVariant(t, architecture, remoteLayer, "base", false)
}

func imageVariant(t *testing.T, architecture string, remoteLayer bool, name string, badDiffID bool) imageFixture {
	t.Helper()
	layer := ociTar(t, map[string][]byte{"app.txt": []byte("offline fixture\n")}, "")
	diffID := digestBytes(layer)
	if badDiffID {
		diffID = digestBytes([]byte("different expanded bytes"))
	}
	config := ociJSON(t, map[string]any{
		"os": "linux", "architecture": architecture,
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{diffID}},
		"config": map[string]any{"Labels": map[string]string{"fixture": name}},
	})
	descriptor := func(mediaType string, data []byte) map[string]any {
		return map[string]any{"mediaType": mediaType, "digest": digestBytes(data), "size": len(data)}
	}
	layerRef := descriptor(ociLayerType, layer)
	if remoteLayer {
		layerRef["urls"] = []string{"https://example.invalid/layer"}
	}
	manifest := ociJSON(t, map[string]any{
		"schemaVersion": 2, "mediaType": ociManifestType,
		"config": descriptor(ociConfigType, config), "layers": []any{layerRef},
	})
	index := ociJSON(t, map[string]any{
		"schemaVersion": 2, "mediaType": ociIndexType, "manifests": []any{descriptor(ociManifestType, manifest)},
	})
	image := digestBytes(manifest)
	layerPath := ociBlobPrefix + strings.TrimPrefix(digestBytes(layer), "sha256:")
	return imageFixture{
		files: map[string][]byte{
			"index.json": index, "oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`),
			ociBlobPrefix + strings.TrimPrefix(image, "sha256:"):               manifest,
			ociBlobPrefix + strings.TrimPrefix(digestBytes(config), "sha256:"): config,
			layerPath: layer,
		},
		image: image, config: digestBytes(config), layer: layerPath,
	}
}

func imageSetFiles(t *testing.T, images ...imageFixture) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	manifests := make([]map[string]any, 0, len(images))
	for _, image := range images {
		for name, data := range image.files {
			files[name] = data
		}
		manifests = append(manifests, map[string]any{
			"mediaType": ociManifestType, "digest": image.image, "size": len(files[ociBlobPath(image.image)]),
			"annotations": map[string]string{"org.opencontainers.image.ref.name": "example.invalid/source:tag"},
		})
	}
	files["index.json"] = ociJSON(t, map[string]any{
		"schemaVersion": 2, "mediaType": ociIndexType, "manifests": manifests,
	})
	return files
}

func ociJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func ociTar(t *testing.T, files map[string][]byte, duplicate string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	slices.Sort(paths)
	if duplicate != "" {
		paths = append(paths, duplicate)
	}
	for _, name := range paths {
		body := files[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func signOCI(t *testing.T, archive []byte, images ...string) signedFixture {
	t.Helper()
	fixture := signedBundle(t, "compose")
	read := func(name string) []byte {
		t.Helper()
		raw, err := fixture.root.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	placeholder := []byte(`["sha256:` + strings.Repeat("b", 64) + `"]`)
	manifest := bytes.Replace(read(manifestName), placeholder, ociJSON(t, images), 1)
	if err := fixture.root.WriteFile(manifestName, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.root.WriteFile(ImageArchiveName, archive, 0600); err != nil {
		t.Fatal(err)
	}
	signFixtureIndex(t, fixture.root, fixture.private, ociJSON(t, bundleIndex{
		SchemaVersion: 1, Manifest: digestBytes(manifest), English: digestBytes(read(englishName)),
		Chinese: digestBytes(read(chineseName)), Images: digestBytes(archive),
	}))
	fixture.limit = int64(len(archive) + len(read("compose.yaml")))
	return fixture
}

func TestProjectVerifiedOCIImageSet(t *testing.T) {
	first := newImageFixture(t, "amd64", false)
	second := imageVariant(t, "amd64", false, "other", false)
	files := imageSetFiles(t, first, second)
	fixture := signOCI(t, ociTar(t, files, ""), second.image, first.image)
	_, source, err := verifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit, true)
	if err != nil {
		t.Fatal(err)
	}
	defer source.file.Close()
	for i, want := range []imageFixture{first, second} {
		var output bytes.Buffer
		if err := source.writeProjection(t.Context(), source.members[i], &output); err != nil {
			t.Fatal(err)
		}
		projected := signOCI(t, output.Bytes(), want.image)
		verified, err := VerifyInstallBundle(t.Context(), projected.root, projected.key, projected.limit)
		if err != nil || !slices.Equal(verified.Archive.Images, []OCIImage{{ManifestDigest: want.image, ConfigDigest: want.config}}) ||
			bytes.Contains(output.Bytes(), []byte("example.invalid/source")) {
			t.Fatalf("projection did not preserve only the selected tagless image: %v", err)
		}
	}

	file, err := os.OpenFile(filepath.Join(fixture.root.Name(), ImageArchiveName), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0}, source.sections[ociBlobPath(second.config)].offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := source.writeProjection(t.Context(), source.members[1], &bytes.Buffer{}); !errors.Is(err, ErrImageImport) {
		t.Fatalf("a changed verified blob was staged: %v", err)
	}
}

func TestImportImageSetRejectsMissingOrTamperedMember(t *testing.T) {
	first := newImageFixture(t, "amd64", false)
	second := imageVariant(t, "amd64", false, "other", false)
	for _, missing := range []bool{true, false} {
		name := "tampered"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			files := imageSetFiles(t, first, second)
			path := ociBlobPath(second.config)
			if missing {
				delete(files, path)
			} else {
				files[path] = append([]byte(" "), files[path]...)
			}
			fixture := signOCI(t, ociTar(t, files, ""), first.image, second.image)
			result, err := ImportInstallImage(t.Context(), fixture.root.Name(), fixture.key, ImageImportOptions{
				MaximumPayloadBytes: fixture.limit, MaximumExpandedBytes: 1 << 20, Timeout: time.Second,
			})
			if !errors.Is(err, ErrBundle) || len(result.Archive.Images) != 0 {
				t.Fatalf("invalid second member was admitted: %v", err)
			}
		})
	}
}

func TestVerifyInstallBundleOCIClosure(t *testing.T) {
	image := newImageFixture(t, "amd64", false)
	archive := ociTar(t, image.files, "")
	fixture := signOCI(t, archive, image.image)
	release, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
	if err != nil || !slices.Equal(release.Manifest.Images, []string{image.image}) {
		t.Fatalf("verified image inventory = %v, %v", release.Manifest.Images, err)
	}
	if release.Archive.Digest != digestBytes(archive) || release.Archive.Bytes != int64(len(archive)) ||
		!slices.Equal(release.Archive.Images, []OCIImage{{ManifestDigest: image.image, ConfigDigest: image.config}}) {
		t.Fatalf("import facts do not match the verified archive: %+v", release.Archive)
	}
	if _, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit-1); !errors.Is(err, ErrBundle) {
		t.Fatalf("oversized archive was admitted: %v", err)
	}
	// Zero padding keeps the OCI layout valid but changes the signed bytes.
	if err := fixture.root.WriteFile(ImageArchiveName, append(archive, 0), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit+1); !errors.Is(err, ErrBundle) {
		t.Fatalf("unsigned archive padding was admitted: %v", err)
	}
}

func TestVerifyInstallBundleRejectsSignedInvalidOCI(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"wrong platform", "Linux amd64"},
		{"remote layer", "remote or unsupported layer"},
		{"missing layer", "missing, remote"},
		{"extra blob", "unreferenced blob"},
		{"wrong digest", "blob that does not match its digest"},
		{"different image", "undeclared"},
		{"docker wrapper", "outside the OCI layout"},
		{"path traversal", "outside the OCI layout"},
		{"duplicate entry", "repeated file"},
		{"missing end marker", "malformed tar end marker"},
		{"trailing archive", "after the tar end marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			architecture := "amd64"
			if tc.name == "wrong platform" {
				architecture = "arm64"
			}
			image := newImageFixture(t, architecture, tc.name == "remote layer")
			var duplicate string
			switch tc.name {
			case "missing layer":
				delete(image.files, image.layer)
			case "extra blob":
				body := []byte("foreign blob")
				image.files[ociBlobPrefix+strings.TrimPrefix(digestBytes(body), "sha256:")] = body
			case "wrong digest":
				image.files[image.layer][0] ^= 1
			case "different image":
				image.image = "sha256:" + strings.Repeat("0", 64)
			case "docker wrapper":
				image.files["manifest.json"] = []byte("[]")
			case "path traversal":
				image.files["../index.json"] = []byte("{}")
			case "duplicate entry":
				duplicate = image.layer
			}
			archive := ociTar(t, image.files, duplicate)
			switch tc.name {
			case "missing end marker":
				archive = archive[:len(archive)-tarBlockBytes]
			case "trailing archive":
				archive = append(archive, archive...)
			}
			fixture := signOCI(t, archive, image.image)
			if _, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit); err != nil {
				t.Fatalf("the fixture must have a valid signature and payload hash: %v", err)
			}
			release, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
			if !errors.Is(err, ErrBundle) || !strings.Contains(err.Error(), tc.want) || release.Manifest.ID != "" {
				t.Fatalf("wanted refusal containing %q, got %v", tc.want, err)
			}
		})
	}
}
