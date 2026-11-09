package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

const (
	coldModelPath  = "/api/v1/sites/default/cells/default/admin/models"
	coldImportPath = "/api/v1/sites/default/cells/default/admin/model-imports"
)

func coldRestoreUUID(t *testing.T) string {
	t.Helper()
	id, err := randomBytes(16)
	if err != nil {
		t.Fatal(err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

func publishColdRestoreModel(t *testing.T, browser coldRestoreBrowser, key license.PrivateKey, workspace, path string) string {
	t.Helper()
	const licensePath = "/api/v1/admin/license"
	var installation struct {
		DeploymentID string `json:"deployment_id"`
	}
	browser.get(t, licensePath, &installation)
	now := time.Now().UTC().Truncate(time.Second)
	artifact, err := license.Issue(license.Claims{
		FormatVersion: license.FormatVersion, LicenseID: coldRestoreUUID(t), CustomerReference: "Signed recovery fixture",
		DeploymentID: installation.DeploymentID, Scope: license.WholePlatformScope,
		IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(48 * time.Hour).Format(time.RFC3339),
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	csrf := browser.csrf(t)
	headers := map[string]string{"X-CSRF-Token": csrf}
	browser.request(t, http.MethodPost, licensePath, coldRestoreJSON(t, map[string]string{
		"artifact": string(artifact), "reason": "Signed recovery fixture", "expected_current_license_id": "",
	}), headers, http.StatusOK)

	var catalog struct {
		Digest string `json:"catalog_digest"`
		Chunk  int64  `json:"chunk_bytes"`
	}
	browser.get(t, coldModelPath+"/catalog", &catalog)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || catalog.Chunk < 1 || catalog.Chunk > 64<<20 || catalog.Digest == "" {
		t.Fatalf("invalid model fixture or catalogue: %v", err)
	}
	headers["Idempotency-Key"] = coldRestoreUUID(t)
	raw := browser.request(t, http.MethodPost, coldImportPath, coldRestoreJSON(t, map[string]any{
		"workspace_id": workspace, "name": "Signed recovery model", "catalog_digest": catalog.Digest,
		"expected_bytes": info.Size(), "license_acknowledged": true, "reason": "Signed recovery fixture",
	}), headers, http.StatusCreated)
	var imported struct {
		ID    string `json:"id"`
		State string `json:"state"`
		Error string `json:"error_code"`
	}
	decodeColdRestoreResponse(t, raw, &imported)
	if imported.ID == "" {
		t.Fatal("the model import did not return an ID")
	}
	route := coldImportPath + "/" + imported.ID
	buffer := make([]byte, catalog.Chunk)
	for offset := int64(0); offset < info.Size(); {
		chunk := buffer[:min(catalog.Chunk, info.Size()-offset)]
		if _, err := io.ReadFull(f, chunk); err != nil {
			t.Fatal(err)
		}
		uploadColdRestoreChunk(t, browser, route, csrf, offset, chunk)
		offset += int64(len(chunk))
	}
	delete(headers, "Idempotency-Key")
	browser.request(t, http.MethodPost, route+"/complete", coldRestoreJSON(t, map[string]string{"reason": "Signed recovery fixture"}), headers, http.StatusAccepted)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		browser.get(t, route, &imported)
		switch imported.State {
		case "ready":
			t.Logf("published approved model from %d archive bytes", info.Size())
			return imported.ID
		case "failed":
			t.Fatalf("model verification failed: %s", imported.Error)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("model verification timed out")
	return ""
}

// An ambiguous transport failure is safe to retry only after reading the
// server's committed offset. HTTP refusals still fail the test immediately.
func uploadColdRestoreChunk(t *testing.T, browser coldRestoreBrowser, route, csrf string, offset int64, chunk []byte) {
	t.Helper()
	sum := sha256.Sum256(chunk)
	headers := map[string]string{
		"Content-Type": "application/octet-stream", "X-CSRF-Token": csrf,
		"X-Chunk-Offset": strconv.FormatInt(offset, 10), "X-Chunk-SHA256": hex.EncodeToString(sum[:]),
	}
	for attempt := 0; attempt < 3; attempt++ {
		status, _, err := browser.send(t.Context(), http.MethodPost, route+"/chunks", bytes.NewReader(chunk), headers)
		if err == nil {
			if status != http.StatusOK {
				t.Fatalf("model upload at offset %d returned HTTP %d", offset, status)
			}
			return
		}
		t.Logf("model upload transport failed at offset %d: %v", offset, err)
		var current struct {
			Uploaded int64 `json:"uploaded_bytes"`
		}
		browser.get(t, route, &current)
		switch current.Uploaded {
		case offset + int64(len(chunk)):
			return
		case offset:
			// The server has not committed this chunk.
		default:
			t.Fatalf("server offset %d does not match the attempted chunk at %d", current.Uploaded, offset)
		}
	}
	t.Fatalf("model upload at offset %d exhausted its transport retries", offset)
}

func assertColdRestoreModel(t *testing.T, browser coldRestoreBrowser, site Config, id string) {
	t.Helper()
	var model struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		Manifest struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
			Files      []struct {
				Path   string `json:"path"`
				Size   int64  `json:"size_bytes"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"manifest"`
	}
	browser.get(t, coldModelPath+"/"+id, &model)
	if model.ID != id || model.State != "ready" || model.Manifest.Repository == "" || model.Manifest.Revision == "" || len(model.Manifest.Files) == 0 {
		t.Fatal("the restored approved model is not ready through Admin")
	}
	root := filepath.Join(site.Layout().Models, "objects", id)
	var total int64
	for _, file := range model.Manifest.Files {
		if err := verifyBackupFile(root, backupFile{Bytes: file.Size, SHA256: file.SHA256}, file.Path); err != nil {
			t.Fatal(err)
		}
		total += file.Size
	}
	t.Logf("Admin returned ready model %s revision %s and %d restored files matched their hashes (%d bytes)",
		model.Manifest.Repository, model.Manifest.Revision, len(model.Manifest.Files), total)
}
