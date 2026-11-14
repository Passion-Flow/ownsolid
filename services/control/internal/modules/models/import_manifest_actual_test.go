//go:build model_integration

package models

import (
	"os"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestActualApprovedImportManifestRejectsAlternateArchive(t *testing.T) {
	path := os.Getenv("OWNSOLID_TEST_ALTERNATE_MODEL_ARCHIVE")
	if path == "" {
		t.Skip("provide the pinned SmolLM2 archive in OWNSOLID_TEST_ALTERNATE_MODEL_ARCHIVE")
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	manifest := resolveImportManifest(wire.ModelCatalogDigest())
	if manifest == nil {
		t.Fatal("legacy Qwen import manifest unavailable")
	}
	stage := t.TempDir()
	if err = manifest.extractArchive(t.Context(), source, stage); err == nil || err.Error() != "MODEL_ARCHIVE_INVALID" {
		t.Fatalf("Qwen archive verification accepted the SmolLM2 file set: %v", err)
	}
	files, err := os.ReadDir(stage)
	if err != nil || len(files) != 0 {
		t.Fatalf("foreign first entry left a staged file: %v, %v", files, err)
	}
}
