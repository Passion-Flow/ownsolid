package factory

import (
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestCurrentRegistryRequiresTheExactRuntime(t *testing.T) {
	pin := wire.Asset{
		ID: RegistryImageAsset, Kind: "image_archive", Architecture: "amd64",
		ImageID: registryManifest, ImageConfigDigest: registryConfig,
	}
	for _, tc := range []struct {
		name         string
		prerequisite string
		assets       []wire.Asset
		code         string
	}{
		{name: "legacy"},
		{name: "exact", prerequisite: RegistryPrerequisite, assets: []wire.Asset{pin}},
		{name: "missing", prerequisite: RegistryPrerequisite, code: "RELEASE_REGISTRY_IMAGE_MISSING"},
		{name: "unsigned group selection", assets: []wire.Asset{pin}, code: "RELEASE_REGISTRY_IMAGE_IDENTITY_INVALID"},
		{name: "wrong pin", prerequisite: RegistryPrerequisite, code: "RELEASE_REGISTRY_IMAGE_IDENTITY_INVALID",
			assets: []wire.Asset{{ID: RegistryImageAsset, Kind: "image_archive", Architecture: "amd64", ImageID: pin.ImageID, ImageConfigDigest: pin.ImageID}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := wire.Manifest{Prerequisites: []string{tc.prerequisite}, Assets: tc.assets}
			if err := validateCurrentRegistry(m); ErrorCode(err) != tc.code {
				t.Fatalf("wanted %q, got %v", tc.code, err)
			}
		})
	}
}
