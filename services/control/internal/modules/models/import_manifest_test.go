package models

import (
	"encoding/json"
	"testing"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestImportManifestPreservesLegacyQwenIdentity(t *testing.T) {
	manifest := resolveImportManifest(wire.ModelCatalogDigest())
	if manifest == nil {
		t.Fatal("legacy Agent digest no longer resolves")
	}
	catalog := wire.ApprovedModel()
	stored, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil || !manifest.matchesStored(stored) {
		t.Fatalf("legacy stored manifest no longer resolves: %v", err)
	}
	minimum := modelartifact.MinimumArchiveBytes()
	if manifest.admitsArchiveSize(minimum-1) || !manifest.admitsArchiveSize(minimum) ||
		!manifest.admitsArchiveSize(MaximumBundleBytes) || manifest.admitsArchiveSize(MaximumBundleBytes+1) {
		t.Fatal("legacy archive admission bounds changed")
	}
	for _, want := range catalog.Files {
		if got, ok := manifest.file(want.Path); !ok || got != want {
			t.Errorf("legacy file binding changed for %s", want.Path)
		}
	}
	catalog.Revision += "-altered"
	changed, err := json.Marshal(catalog)
	if err != nil || manifest.matchesStored(changed) {
		t.Fatalf("changed revision was accepted as legacy Qwen: %v", err)
	}
}
