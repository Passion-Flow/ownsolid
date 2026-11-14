//go:build model_lifecycle_derived_storage_integration

package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func TestActualCpuDerivedModelOrdinaryCopyCompleteHashesRestartReplayAndTamperRefusal(t *testing.T) {
	producer := os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT")
	if !filepath.IsAbs(producer) {
		t.Fatal("actual retained native preparation producer required")
	}
	resolved, e := filepath.EvalSymlinks(producer)
	if e != nil || resolved != producer {
		t.Fatal("producer path not exact", e)
	}
	raw, e := os.ReadFile(filepath.Join(producer, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var report wire.PreparedResult
	if wire.DecodeStrict(raw, &report) != nil || report.Status != "prepared" || report.Device != "cpu_verification" || len(report.ModelFiles) != 9 {
		t.Fatal("actual native CPU result required")
	}
	c := derivedStorageCatalog()
	c.Scope = wire.Scope{TenantID: report.TenantID, WorkspaceID: report.WorkspaceID, PreparationID: report.PreparationID}
	c.AttemptID = report.AttemptID
	c.Files = report.ModelFiles
	sum := sha256.Sum256(raw)
	c.ReportSHA256 = hex.EncodeToString(sum[:])
	sources := map[string]*os.File{}
	for _, asset := range c.Files {
		file, e := os.Open(filepath.Join(producer, "model", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		defer file.Close()
		sources[asset.Path] = file
	}
	s := derivedTestStore(t)
	ctx := context.Background()
	prepared, e := s.PrepareDerivedFiles(ctx, c, sources, 3145728000)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.verifyPreparedDerivedToken(prepared); e != nil {
		t.Fatal("bounded final copy identity", e)
	}
	expected, e := c.Digest()
	if e != nil || prepared.Digest() != expected {
		t.Fatal("verified identity", e)
	}
	clone := prepared.Catalog()
	clone.Files[0].SHA256 = "mutated"
	if prepared.Catalog().Files[0].SHA256 == "mutated" {
		t.Fatal("private verified token exposed mutable inventory")
	}
	restarted := &Service{RootDirectory: s.RootDirectory}
	if e = restarted.VerifyDerivedFiles(ctx, c); e != nil {
		t.Fatal("private copy restart", e)
	}
	replay, e := restarted.PrepareDerivedFiles(ctx, c, sources, 3145728000)
	if e != nil || replay.Digest() != prepared.Digest() {
		t.Fatal("exact whole-file replay", e)
	}
	var bytes int64
	for _, asset := range c.Files {
		bytes += asset.SizeBytes
	}
	// Deliberately corrupt only the new isolated test copy, never its retained native producer.
	path := filepath.Join(s.RootDirectory, derivedDirectory(c), "config.json")
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	body[0] ^= 1
	if e = os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	os.Chmod(path, 0444)
	if e = restarted.verifyPreparedDerivedToken(prepared); e == nil {
		t.Fatal("modified bytes retained a verified final token")
	}
	if e = restarted.VerifyDerivedFiles(ctx, c); e == nil {
		t.Fatal("tampered model verified")
	}
	if _, e = restarted.PrepareDerivedFiles(ctx, c, sources, 3145728000); e == nil {
		t.Fatal("tampered canonical directory overwritten/adopted")
	}
	if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
		result, _ := json.MarshalIndent(map[string]any{"scope": "Actual retained native CPU model ordinary copies/full hashes/read-only single-link files/restart/replay/tamper refusal only. Synthetic catalog model/base/artifact IDs; no DB model registration, Admin approval, GPU or serving qualification.", "files": len(c.Files), "bytes": bytes, "catalog_sha256": prepared.Digest(), "precision": "BF16", "database_model_registered": false}, "", "  ")
		if e = os.WriteFile(filepath.Join(output, "actual-derived-storage.json"), result, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("Actual9-file native model copy verified; no database registration, approval or NVIDIA qualification.")
}
