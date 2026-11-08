package factory

import (
	"slices"
	"testing"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestCurrentRKE2InventoryRequiresTheWholeApprovedRelease(t *testing.T) {
	config, err := LoadConfig([]byte(`{"require_rke2":true}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	config = WithCurrentProfile(config)
	base := currentInventory()
	if err := config.validateManifest(base); ErrorCode(err) != "RELEASE_RKE2_ASSETS_REQUIRED" {
		t.Fatalf("single-node-only release with the RKE2 policy: %v", err)
	}
	release := agent.ApprovedKubernetesRelease()
	complete := rke2Inventory(release)
	for i := range complete {
		complete[i].Licenses = []string{"test-notice"}
	}
	newManifest := func() wire.Manifest {
		m := currentInventory()
		m.Prerequisites = append(m.Prerequisites, rke2Prerequisite+release.Version)
		m.Assets = append(m.Assets, complete...)
		return m
	}
	if err := config.validateManifest(newManifest()); err != nil {
		t.Fatal(err)
	}
	for _, asset := range complete {
		t.Run("missing "+asset.ID, func(t *testing.T) {
			m := newManifest()
			m.Assets = slices.DeleteFunc(m.Assets, func(a wire.Asset) bool { return a.ID == asset.ID })
			if err := config.validateManifest(m); ErrorCode(err) != "RELEASE_RKE2_ASSETS_REQUIRED" {
				t.Fatalf("accepted an incomplete RKE2 group: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name, code string
		change     func(*wire.Manifest)
	}{
		{"signed declaration", "RELEASE_RKE2_DECLARATION_REQUIRED", func(m *wire.Manifest) { m.Prerequisites = base.Prerequisites }},
		{"unsupported version", "RELEASE_RKE2_VERSION_INVALID", func(m *wire.Manifest) { m.Prerequisites[1] = rke2Prerequisite + "different" }},
		{"duplicate declaration", "RELEASE_RKE2_VERSION_INVALID", func(m *wire.Manifest) { m.Prerequisites = append(m.Prerequisites, m.Prerequisites[1]) }},
		{"wrong architecture", "RELEASE_RKE2_ASSET_INVALID", func(m *wire.Manifest) { m.Assets[len(base.Assets)].Architecture = "arm64" }},
		{"substituted checksum file", "RELEASE_RKE2_ASSET_INVALID", func(m *wire.Manifest) { m.Assets[len(m.Assets)-1].SHA256 = wire.Digest([]byte("different")) }},
		{"arbitrary path", "RELEASE_RKE2_ASSET_INVALID", func(m *wire.Manifest) { m.Assets[len(base.Assets)].Path = "other-binary" }},
		{"generic asset in reserved directory", "RELEASE_RKE2_ASSET_INVALID", func(m *wire.Manifest) {
			a := m.Assets[len(m.Assets)-1]
			a.ID, a.Path = "foreign-checksum", "rke2/extra-checksum"
			m.Assets = append(m.Assets, a)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newManifest()
			tc.change(&m)
			if err := config.validateManifest(m); ErrorCode(err) != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
}
