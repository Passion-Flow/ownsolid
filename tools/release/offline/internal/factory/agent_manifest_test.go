package factory

import (
	"slices"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestAgentProfileInventory(t *testing.T) {
	base, err := LoadConfig([]byte("{}"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := WithAgentProfile(base)
	inventory := currentInventory()
	inventory.Profile = AgentProfile
	inventory.SchemaVersion, inventory.MinimumPredecessor, inventory.MaximumPredecessor = 1, 1, 1
	inventory.Assets = []wire.Asset{
		{ID: AgentBinaryAsset, Path: "agent", Kind: "operator", Architecture: "amd64", Size: 64, SHA256: wire.Digest([]byte("binary")), Licenses: []string{"notice"}},
		{ID: AgentUnitAsset, Path: "agent.service", Kind: "operator", Size: 64, SHA256: wire.Digest([]byte("unit")), Licenses: []string{"notice"}},
		{ID: currentSBOM, Path: "agent.spdx.json", Kind: "sbom", Size: 64, SHA256: wire.Digest([]byte("sbom")), Licenses: []string{"notice"}},
		{ID: "notice", Path: "notice.txt", Kind: "license", Size: 64, SHA256: wire.Digest([]byte("notice"))},
	}
	if err := cfg.validateManifest(inventory); err != nil {
		t.Fatal(err)
	}
	for _, incompatible := range []Config{base, WithCurrentProfile(base)} {
		if err := incompatible.validateManifest(inventory); err == nil {
			t.Fatal("incompatible release path accepted Agent media")
		}
	}
	cfg.RequireRKE2 = true
	if err := cfg.validateManifest(inventory); ErrorCode(err) != "RELEASE_RKE2_CURRENT_PROFILE_REQUIRED" {
		t.Fatalf("Agent media bypassed RKE2 policy: %v", err)
	}
	cfg.RequireRKE2 = false
	for _, test := range []struct {
		name, code string
		change     func(*wire.Manifest)
	}{
		{"architecture", "RELEASE_AGENT_ASSET_INVALID", func(m *wire.Manifest) { m.Assets[0].Architecture = "arm64" }},
		{"oversized executable", "RELEASE_AGENT_ASSET_INVALID", func(m *wire.Manifest) { m.Assets[0].Size = maxAgentBinaryBytes + 1 }},
		{"missing unit", "RELEASE_REQUIRED_ASSET_MISSING", func(m *wire.Manifest) { m.Assets = slices.Delete(m.Assets, 1, 2) }},
		{"missing notice", "RELEASE_LICENSE_INVALID", func(m *wire.Manifest) { m.Assets = m.Assets[:3] }},
		{"image", "RELEASE_AGENT_ASSET_INVALID", func(m *wire.Manifest) { m.Assets[1].Kind = "image_archive" }},
		{"fixture profile", "RELEASE_AGENT_PROFILE_REQUIRED", func(m *wire.Manifest) { m.Profile = wire.FixtureProfile }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := inventory
			bad.Assets = slices.Clone(inventory.Assets)
			test.change(&bad)
			if err := cfg.validateManifest(bad); ErrorCode(err) != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
}
