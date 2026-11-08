package apps

import (
	"archive/tar"
	"bytes"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"
)

type imageFixture struct {
	files        map[string][]byte
	image, layer string
}

func newImageFixture(t *testing.T, architecture string, remoteLayer bool) imageFixture {
	t.Helper()
	layer := ociTar(t, map[string][]byte{"app.txt": []byte("offline fixture\n")}, "")
	config := ociJSON(t, map[string]any{
		"os": "linux", "architecture": architecture,
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{digestBytes(layer)}},
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
		image: image, layer: layerPath,
	}
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

func signOCI(t *testing.T, archive []byte, image string) signedFixture {
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
	manifest := bytes.Replace(read(manifestName), []byte("sha256:"+strings.Repeat("b", 64)), []byte(image), 1)
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

func TestVerifyInstallBundleOCIClosure(t *testing.T) {
	image := newImageFixture(t, "amd64", false)
	archive := ociTar(t, image.files, "")
	fixture := signOCI(t, archive, image.image)
	release, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
	if err != nil || !slices.Equal(release.Manifest.Images, []string{image.image}) {
		t.Fatalf("verified image inventory = %v, %v", release.Manifest.Images, err)
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
