package factory

import (
	"slices"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	// RegistryPrerequisite selects the optional signed Distribution runtime.
	RegistryPrerequisite = "ownsolid-site-registry:distribution-v3.1.2"
	// RegistryImageAsset is the signed Distribution runtime archive.
	RegistryImageAsset = "registry-image"
	// Docker Official Images, Distribution 3.1.2, Linux amd64.
	registryManifest = "sha256:52b489ad8e6f40d1a676702e3ce398ec796ce38f08fbb786e1f324baa12d84b8"
	registryConfig   = "sha256:39a2a88344598be51c03d6829b40dd29fcda04ce1e01ef1f7596b5af744276ce"
)

func validateCurrentRegistry(m wire.Manifest) error {
	required := slices.Contains(m.Prerequisites, RegistryPrerequisite)
	for _, a := range m.Assets {
		if a.ID != RegistryImageAsset {
			continue
		}
		if !required || a.Kind != "image_archive" || a.Architecture != "amd64" ||
			a.ImageID != registryManifest || a.ImageConfigDigest != registryConfig {
			return fail("RELEASE_REGISTRY_IMAGE_IDENTITY_INVALID")
		}
		return nil
	}
	if required {
		return fail("RELEASE_REGISTRY_IMAGE_MISSING")
	}
	return nil
}
