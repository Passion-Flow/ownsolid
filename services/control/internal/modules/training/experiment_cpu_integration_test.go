//go:build training_artifact_integration && training_experiment_integration

package training_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// The approved replay is the native CPU qualification documented in
// training-dataset-lineage.md. Its protected request and result stay unmodified.
func TestActualCPUExperimentInputs(t *testing.T) {
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	published := os.Getenv("PROJECT_ONE_ACTUAL_DATASET_DIRECTORY")
	if !filepath.IsAbs(output) || !filepath.IsAbs(published) {
		t.Fatal("absolute retained training output and authorized dataset paths required")
	}
	read := func(path string) []byte {
		t.Helper()
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	request, e := wire.DecodeRequest(read(filepath.Join(filepath.Dir(output), "request.json")))
	if e != nil {
		t.Fatal(e)
	}
	c := request.Configuration
	if c.Device != wire.VerificationProfile || c.Seed != 42 || c.MaximumSteps != 24 ||
		c.MaximumTokens != 96 || c.Rank != 8 || c.Threads != 2 || c.LearningRate != "0.0005" ||
		request.ExecutionIdentity.ImageID != nil || request.ModelCatalogSHA256 != agentwire.ModelCatalogDigest() {
		t.Fatal("original CPU configuration or approved base changed")
	}
	raw := read(filepath.Join(output, "result.json"))
	result, e := wire.DecodeResult(raw, request, agentwire.ApprovedModel().Repository)
	if e != nil || result.Status != "trained" || result.GlobalStep != c.MaximumSteps ||
		digest(raw) != "42f0790680294637f8248a37a405cec920ee6d1d6c3b678248946b769781796f" {
		t.Fatal("original protected CPU result changed", e)
	}
	raw = read(filepath.Join(published, "manifest.json"))
	var manifest datawire.Manifest
	if digest(raw) != request.DatasetManifestSHA256 || json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("authorized publication manifest changed")
	}
	for _, asset := range []struct{ name, sha string }{
		{"train.jsonl", request.TrainSHA256},
		{"validation.jsonl", request.ValidationSHA256},
	} {
		body := read(filepath.Join(published, asset.name))
		expected := datawire.Asset{Path: asset.name, SizeBytes: int64(len(body)), SHA256: asset.sha}
		if digest(body) != expected.SHA256 || !slices.Contains(manifest.Files, expected) {
			t.Fatal("published partition changed", asset.name)
		}
	}
	lock := read("../../../../../runtimes/training/requirements/native-macos-arm64.lock")
	if digest(lock) != "03d3fab0f311aa4837190ab2e90e30a9c16c51c098fc6e6c41626630c6aa0175" {
		t.Fatal("native qualification dependency lock changed")
	}
	t.Log("original CPU result, authorized dataset partitions, configuration and native lock verified")
}
