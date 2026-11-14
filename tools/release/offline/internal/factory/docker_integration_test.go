//go:build offlineintegration

package factory

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// TestActualDockerExportAndCLI requires an actual exported small fixture image.
// It is deliberately independent of full product images and native target claims.
func TestActualDockerExportAndCLI(t *testing.T) {
	c, m, _ := setup(t)
	archive := os.Getenv("PROJECT_ONE_TEST_OCI_ARCHIVE")
	imageID := os.Getenv("PROJECT_ONE_TEST_IMAGE_ID")
	configDigest := os.Getenv("PROJECT_ONE_TEST_IMAGE_CONFIG_DIGEST")
	binary := os.Getenv("PROJECT_ONE_TEST_FACTORY_BINARY")
	evidence := os.Getenv("PROJECT_ONE_TEST_FACTORY_EVIDENCE")
	input, e := openRegular(archive, 32<<20, true)
	if e != nil {
		t.Fatal("exact actual export required", e)
	}
	destination, e := os.OpenFile(filepath.Join(c.ArtifactRoot, "actual-image.tar"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	count, e := io.Copy(destination, input)
	destination.Close()
	input.Close()
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(filepath.Join(c.ArtifactRoot, "actual-image.tar"))
	if e != nil {
		t.Fatal(e)
	}
	hash, e := streamHash(context.Background(), f, nil, count)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	m.Assets = append(m.Assets, wire.Asset{ID: "actual-fixture-image", Path: "actual-image.tar", Kind: "image_archive", Size: count, SHA256: hash, ImageID: imageID, ImageConfigDigest: configDigest, Architecture: "amd64", Licenses: []string{"license"}})
	c.MaximumAssetBytes = 32 << 20
	c.MaximumTotalBytes = 48 << 20
	put(t, c.ManifestFile, Encode(m))
	config := filepath.Join(filepath.Dir(c.ManifestFile), "factory.json")
	put(t, config, Encode(c))
	environment := filepath.Join(filepath.Dir(c.ManifestFile), "factory.env")
	put(t, environment, []byte("PROJECT_ONE_OFFLINE_FACTORY_COMPRESSION_LEVEL=2\nPROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=30\n"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	records := []map[string]any{}
	for _, operation := range []string{"validate", "build", "inspect"} {
		command := exec.CommandContext(ctx, binary, operation, "--config", config, "--env-file", environment)
		output, e := command.CombinedOutput()
		if e != nil {
			t.Fatalf("actual CLI %s: %v %s", operation, e, output)
		}
		var result map[string]any
		if json.Unmarshal(output, &result) != nil {
			t.Fatal("CLI result invalid", string(output))
		}
		records = append(records, map[string]any{"operation": operation, "result": result})
		if operation != "validate" && result["qualification"] != "synthetic_test_fixture_only_never_complete_platform_bundle" {
			t.Fatal("fixture mislabeled", result)
		}
	}
	final, e := Inspect(ctx, c, c.OutputFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = privateDirectory(filepath.Dir(evidence)); e != nil {
		t.Fatal(e)
	}
	receipt, e := os.OpenFile(evidence, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer receipt.Close()
	if e = json.NewEncoder(receipt).Encode(map[string]any{"status": "passed_actual_small_Docker_OCI_export_signed_fixture_CLI_roundtrip", "scope": "Explicit synthetic fixture only; Docker Desktop amd64 execution is not native target or full platform installation", "archive_sha256": hash, "image_id": imageID, "bytes": count, "signed_fixture": final, "operations": records}); e != nil || receipt.Sync() != nil {
		t.Fatal("evidence write failed", e)
	}
	// A second process with no fixture admission must fail without changing the bundle.
	put(t, environment, []byte("PROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE=false\n"))
	command := exec.CommandContext(ctx, binary, "inspect", "--config", config, "--env-file", environment)
	output, e := command.CombinedOutput()
	if e == nil || !strings.Contains(string(output), "RELEASE_ARCHIVE_MANIFEST_INVALID") {
		t.Fatal("fixture admission flag ignored", e, string(output))
	}
}
