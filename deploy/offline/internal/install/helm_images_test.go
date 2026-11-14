package install

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func TestHelmImageOverlayUsesSignedManifestDigest(t *testing.T) {
	image := wire.Asset{
		ID: "control-image", Kind: "image_archive", Architecture: "amd64",
		ImageID:           "sha256:" + wire.Digest([]byte("runtime manifest")),
		ImageConfigDigest: "sha256:" + wire.Digest([]byte("image config")),
	}
	result := bundle.Result{
		ManifestSHA256: wire.Digest([]byte("release manifest")),
		Manifest: wire.Manifest{
			Prerequisites: []string{bundle.RegistryPrerequisite},
			Assets:        []wire.Asset{image},
		},
	}
	address := netip.MustParseAddrPort("10.77.107.1:5443")
	var output bytes.Buffer
	if err := writeHelmImageOverlay(result, address, &output); err != nil {
		t.Fatal(err)
	}
	var overlay map[string]map[string]string
	if err := json.Unmarshal(output.Bytes(), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay["registry"]["address"] != address.String() ||
		overlay["images"]["control"] != image.ImageID ||
		overlay["release"]["manifestSHA256"] != result.ManifestSHA256 ||
		len(overlay) != 3 || strings.Contains(output.String(), image.ImageConfigDigest) {
		t.Fatalf("wrong image values: %s", output.Bytes())
	}
	for _, tc := range []struct {
		name   string
		change func(*bundle.Result)
		code   string
	}{
		{"missing group", func(r *bundle.Result) { r.Manifest.Prerequisites = nil }, "INSTALL_REGISTRY_BUNDLE_REQUIRED"},
		{"missing image", func(r *bundle.Result) { r.Manifest.Assets = nil }, "INSTALL_HELM_CONTROL_IMAGE_REQUIRED"},
		{"image tag", func(r *bundle.Result) { r.Manifest.Assets[0].ImageID = "control:test" }, "INSTALL_HELM_CONTROL_IMAGE_REQUIRED"},
		{"other architecture", func(r *bundle.Result) { r.Manifest.Assets[0].Architecture = "arm64" }, "INSTALL_HELM_CONTROL_IMAGE_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := result
			candidate.Manifest.Assets = []wire.Asset{image}
			tc.change(&candidate)
			output.Reset()
			if err := writeHelmImageOverlay(candidate, address, &output); err == nil || !strings.HasPrefix(err.Error(), tc.code) || output.Len() != 0 {
				t.Fatalf("wrong refusal: %v, output %s", err, output.Bytes())
			}
		})
	}
}
