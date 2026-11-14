package install

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"slices"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

// WriteHelmImageValues emits the chart overlay from verified signed media.
func WriteHelmImageValues(ctx context.Context, archive, registry string, policy bundle.Verification, out io.Writer) error {
	address, ok := siteRegistryAddress(registry)
	if !ok {
		return errors.New("INSTALL_HELM_REGISTRY_INVALID: set the private Registry IPv4 address and port")
	}
	result, err := bundle.InspectCurrent(ctx, policy, archive)
	if err != nil {
		return err
	}
	return writeHelmImageOverlay(result, address, out)
}

func writeHelmImageOverlay(result bundle.Result, address netip.AddrPort, out io.Writer) error {
	if !slices.Contains(result.Manifest.Prerequisites, bundle.RegistryPrerequisite) {
		return errors.New("INSTALL_REGISTRY_BUNDLE_REQUIRED: use a signed release containing the site Registry")
	}
	assets, _ := bundleInventory(result)
	control := assets["control-image"]
	digest, valid := strings.CutPrefix(control.ImageID, "sha256:")
	if control.Kind != "image_archive" || control.Architecture != "amd64" || !valid || !wire.ValidSHA256(digest) || !wire.ValidSHA256(result.ManifestSHA256) {
		return errors.New("INSTALL_HELM_CONTROL_IMAGE_REQUIRED: use a release with a signed Linux amd64 control manifest digest")
	}
	return json.NewEncoder(out).Encode(map[string]map[string]string{
		"registry": {"address": address.String()},
		"images":   {"control": control.ImageID},
		"release":  {"manifestSHA256": result.ManifestSHA256},
	})
}
