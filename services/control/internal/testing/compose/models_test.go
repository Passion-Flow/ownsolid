package compose_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
)

// This journey uses the actual approved public archive, container worker and new isolated native volumes.
func exerciseComposeModelImport(t *testing.T, client *http.Client, origin, portalOrigin, password string, restart func(), licenses ...[]byte) models.Version {
	t.Helper()
	var csrf string
	call := func(method, path string, body io.Reader, media string, headers map[string]string, expected int, target any) {
		t.Helper()
		request, e := http.NewRequest(method, origin+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if method != "GET" {
			request.Header.Set("Origin", origin)
			request.Header.Set("X-CSRF-Token", csrf)
			request.Header.Set("Content-Type", media)
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, e := client.Do(request)
		if e != nil {
			t.Fatal("COMPOSE_MODEL_TRANSPORT_FAILED", e)
		}
		defer response.Body.Close()
		if response.StatusCode != expected {
			var problem struct {
				Code string `json:"code"`
			}
			json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem)
			t.Fatalf("COMPOSE_MODEL_STATUS: %s %s = %d, expected %d, safe_code=%s", method, path, response.StatusCode, expected, problem.Code)
		}
		if target != nil && json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target) != nil {
			t.Fatal("COMPOSE_MODEL_RESPONSE_INVALID", method, path)
		}
	}
	refresh := func() {
		var value struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		call("GET", "/api/v1/auth/csrf", nil, "", nil, 200, &value)
		csrf = value.Data.Token
		if csrf == "" {
			t.Fatal("COMPOSE_MODEL_CSRF_MISSING")
		}
	}
	encode := func(value any) io.Reader {
		body, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		return bytes.NewReader(body)
	}
	refresh()
	call("POST", "/api/v1/auth/login", encode(map[string]string{"email": "admin@example.invalid", "password": password}), "application/json", nil, 200, nil)
	refresh()
	if len(licenses) > 1 {
		t.Fatal("COMPOSE_LICENSE_SCOPE_INVALID")
	}
	if len(licenses) == 1 {
		call("POST", "/api/v1/admin/license", encode(map[string]any{"artifact": string(licenses[0]), "reason": "Synthetic isolated M06 Compose license", "expected_current_license_id": ""}), "application/json", nil, 200, nil)
	}
	var me struct {
		Data struct {
			Workspace struct {
				ID string `json:"id"`
			} `json:"workspace"`
		} `json:"data"`
	}
	call("GET", "/api/v1/me", nil, "", nil, 200, &me)
	if me.Data.Workspace.ID == "" {
		t.Fatal("COMPOSE_MODEL_WORKSPACE_MISSING")
	}
	archive := filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/models/qwen3-0.6b", wire.ApprovedModel().Revision, "qwen3-0.6b.tar")
	file, e := os.Open(archive)
	if e != nil {
		t.Fatal("ACTUAL_FROZEN_MODEL_REQUIRED", e)
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		t.Fatal(e)
	}
	var imported struct {
		Data models.Import `json:"data"`
	}
	call("POST", "/api/v1/admin/model-imports", encode(models.ImportRequest{WorkspaceID: me.Data.Workspace.ID, Name: "Compose actual offline model", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: info.Size(), LicenseAcknowledged: true, Reason: "Isolated actual Compose persistence verification"}), "application/json", map[string]string{"Idempotency-Key": uuid.NewString()}, 201, &imported)
	base := "/api/v1/admin/model-imports/" + imported.Data.ID
	buffer := make([]byte, models.ChunkBytes)
	for offset := int64(0); offset < info.Size(); {
		n, e := io.ReadFull(file, buffer)
		if e != nil && e != io.ErrUnexpectedEOF {
			t.Fatal(e)
		}
		digest := sha256.Sum256(buffer[:n])
		call("POST", base+"/chunks", bytes.NewReader(buffer[:n]), "application/octet-stream", map[string]string{"X-Chunk-Offset": strconv.FormatInt(offset, 10), "X-Chunk-SHA256": hex.EncodeToString(digest[:])}, 200, &imported)
		offset += int64(n)
		if offset == models.ChunkBytes {
			restart()
			refresh()
			call("GET", base, nil, "", nil, 200, &imported)
			if imported.Data.UploadedBytes != offset {
				t.Fatal("COMPOSE_RESTART_LOST_UPLOAD_STATE")
			}
		}
	}
	call("POST", base+"/complete", encode(map[string]string{"reason": "Verify actual isolated Compose model"}), "application/json", nil, 202, &imported)
	deadline := time.Now().Add(75 * time.Second)
	for imported.Data.State != "ready" && time.Now().Before(deadline) {
		if imported.Data.State == "failed" {
			t.Fatal("COMPOSE_MODEL_WORKER_FAILED", imported.Data.ErrorCode)
		}
		time.Sleep(200 * time.Millisecond)
		call("GET", base, nil, "", nil, 200, &imported)
	}
	if imported.Data.State != "ready" || imported.Data.ModelID == nil {
		t.Fatal("COMPOSE_MODEL_IMPORT_TIMEOUT")
	}
	restart()
	refresh()
	var inventory struct {
		Data struct {
			Items []models.Version `json:"items"`
		} `json:"data"`
	}
	call("GET", "/api/v1/admin/models?workspace_id="+me.Data.Workspace.ID, nil, "", nil, 200, &inventory)
	if len(inventory.Data.Items) != 1 || inventory.Data.Items[0].ID != *imported.Data.ModelID || inventory.Data.Items[0].State != "ready" {
		t.Fatal("COMPOSE_MODEL_RESTART_LOST_IMMUTABLE_INVENTORY")
	}

	if os.Getenv("PROJECT_ONE_COMPOSE_GATEWAY_ONLY") == "1" {
		for _, resource := range []string{"gateways", "aliases", "keys", "requests"} {
			var page struct {
				Data struct {
					Items []json.RawMessage `json:"items"`
				} `json:"data"`
			}
			call("GET", "/api/v1/admin/distribution/workspaces/"+me.Data.Workspace.ID+"/"+resource, nil, "", nil, 200, &page)
			if len(page.Data.Items) != 0 {
				t.Fatal("unexpected fresh gateway history")
			}
		}
		call("GET", "/api/v1/admin/distribution/workspaces/"+me.Data.Workspace.ID+"/aliases/"+uuid.NewString()+"/cutovers", nil, "", nil, 404, nil)
		origin = portalOrigin
		refresh()
		call("POST", "/api/v1/auth/login", encode(map[string]string{"email": "admin@example.invalid", "password": password}), "application/json", nil, 200, nil)
		for _, resource := range []string{"aliases", "keys", "requests"} {
			call("GET", "/api/v1/workspaces/"+me.Data.Workspace.ID+"/distribution/"+resource, nil, "", nil, 200, nil)
		}
		call("GET", "/api/v1/workspaces/"+me.Data.Workspace.ID+"/distribution/gateways", nil, "", nil, 404, nil)
		t.Log("M07 cold35-table/12-entry image/runtime principal serves current scoped Admin/Portal gateway collections and refuses later/audience descendants")
	}
	t.Log("Actual 1.52GB approved public archive imported through TLS/Admin/runtime role into a new initialized model native volume; partial-upload restart and ready-file integrity startup/restart passed. No NVIDIA execution or Linux host bind qualification.")
	return inventory.Data.Items[0]
}
