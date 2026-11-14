package factory

import (
	"slices"
	"strings"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	rke2Prerequisite = "rke2-linux-amd64="
	rke2Prefix       = "rke2/"
	rke2BinaryKind   = "rke2_binary"
	rke2ImageSetKind = "rke2_image_set"
	rke2ChecksumID   = "rke2-sha256sum-amd64"
)

func rke2Inventory(release agent.KubernetesRelease) []wire.Asset {
	artifacts := release.Artifacts()
	assets := make([]wire.Asset, 0, len(artifacts)+1)
	for _, artifact := range artifacts {
		kind := rke2ImageSetKind
		if artifact.Name == release.Binary.Name {
			kind = rke2BinaryKind
		}
		assets = append(assets, wire.Asset{ID: artifact.Name, Path: rke2Prefix + artifact.Name, Kind: kind,
			Architecture: "amd64", Size: artifact.SizeBytes, SHA256: artifact.SHA256})
	}
	return append(assets, wire.Asset{ID: rke2ChecksumID, Path: rke2Prefix + "sha256sum-amd64.txt", Kind: "dependency_lock",
		Size: 3558, SHA256: "2a9ae019e47798ab98691f26b2b588a68ca2387cc6c3c3c50efd32b89ac21980"})
}

func validateRKE2Inventory(m wire.Manifest, required, requireComponents bool) error {
	release := agent.ApprovedKubernetesRelease()
	declared := false
	for _, prerequisite := range m.Prerequisites {
		if version, ok := strings.CutPrefix(prerequisite, rke2Prerequisite); ok {
			if declared || version != release.Version {
				return fail("RELEASE_RKE2_VERSION_INVALID")
			}
			declared = true
		}
	}
	expected := make(map[string]wire.Asset)
	for _, asset := range rke2Inventory(release) {
		expected[asset.ID] = asset
	}
	for _, asset := range rke2ComponentAssets {
		expected[asset.ID] = asset
	}
	for _, actual := range m.Assets {
		if actual.Kind != rke2BinaryKind && actual.Kind != rke2ImageSetKind &&
			!strings.HasPrefix(actual.ID, "rke2-") && !strings.HasPrefix(actual.ID, "rke2.") &&
			actual.Path != "rke2" && !strings.HasPrefix(actual.Path, rke2Prefix) {
			continue
		}
		if !declared {
			return fail("RELEASE_RKE2_DECLARATION_REQUIRED")
		}
		pin, ok := expected[actual.ID]
		if !ok || actual.Path != pin.Path || actual.Kind != pin.Kind || actual.Architecture != pin.Architecture ||
			actual.Size != pin.Size || actual.SHA256 != pin.SHA256 || actual.ImageID != "" || actual.ImageConfigDigest != "" {
			return fail("RELEASE_RKE2_ASSET_INVALID")
		}
		delete(expected, actual.ID)
	}
	_, missingSPDX := expected[rke2SPDXID]
	_, missingNotices := expected[rke2NoticeID]
	if (requireComponents || !missingSPDX || !missingNotices) && (missingSPDX || missingNotices) {
		return fail("RELEASE_RKE2_COMPONENTS_REQUIRED")
	}
	for _, asset := range rke2ComponentAssets {
		delete(expected, asset.ID)
	}
	if (declared || required) && len(expected) != 0 {
		return fail("RELEASE_RKE2_ASSETS_REQUIRED")
	}
	if !missingNotices {
		for _, asset := range m.Assets {
			if asset.Kind != "license" && strings.HasPrefix(asset.Path, rke2Prefix) && !slices.Contains(asset.Licenses, rke2NoticeID) {
				return fail("RELEASE_RKE2_NOTICES_REQUIRED")
			}
		}
	}
	return nil
}
