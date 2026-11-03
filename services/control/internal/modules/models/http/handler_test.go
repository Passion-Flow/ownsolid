package http_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestStrictModelImportHTTPChunksCSRFAndPortalBoundary(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	root := t.TempDir()
	os.Chmod(root, 0700)
	s.Config.Models.ArtifactRoot = root
	server := httpfixture.NewTLSServer(t, transport.New(s))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/models/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	workspace := admin.Call("GET", "/api/v1/me", nil, 200, "")["workspace"].(map[string]any)["id"].(string)
	availability := admin.Call("GET", "/api/v1/admin/models/catalog", nil, 200, "ModelCatalogAvailabilityResponse")
	if availability["storage_available"] != true {
		t.Fatal("private configured storage not recognized")
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	input := models.ImportRequest{WorkspaceID: workspace, Name: "Synthetic strict HTTP import", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: wire.ModelBytes() + 20000, LicenseAcknowledged: true, Reason: "Synthetic strict HTTP fixture"}
	first := admin.Write("POST", "/api/v1/admin/model-imports", input, 201, "ModelImportResponse")
	id := first["id"].(string)
	again := admin.Write("POST", "/api/v1/admin/model-imports", input, 201, "ModelImportResponse")
	if again["id"] != id {
		t.Fatal("HTTP idempotency created another import")
	}
	base := "/api/v1/admin/model-imports/" + id
	payload := bytes.Repeat([]byte("S"), int(models.ChunkBytes))
	hash := sha256.Sum256(payload)
	admin.Headers = map[string]string{"Content-Type": "application/octet-stream", "X-Chunk-Offset": "0", "X-Chunk-SHA256": hex.EncodeToString(hash[:])}
	csrf := admin.CSRF
	admin.CSRF = ""
	admin.Call("POST", base+"/chunks", payload, 403, "Problem")
	admin.CSRF = csrf
	admin.Call("POST", base+"/chunks", payload, 200, "ModelImportResponse")
	replay := admin.Call("POST", base+"/chunks", payload, 200, "ModelImportResponse")
	if replay["uploaded_bytes"] != float64(models.ChunkBytes) {
		t.Fatal("HTTP chunk replay changed bytes")
	}
	admin.Headers = map[string]string{}
	admin.Write("POST", base+"/complete", map[string]any{"reason": "Reject incomplete package"}, 409, "Problem")
	admin.Call("GET", "/api/v1/admin/models?workspace_id="+workspace, nil, 200, "ModelListResponse")
	admin.Call("GET", "/api/v1/admin/model-imports?workspace_id="+workspace, nil, 200, "ModelImportListResponse")
	portal.Call("GET", "/api/v1/workspaces/"+workspace+"/models", nil, 200, "ModelListResponse")
	portal.Call("GET", base, nil, 404, "Problem")
	portal.Call("GET", "/api/v1/workspaces/"+workspace+"/models/"+id, nil, 404, "Problem")
	admin.Write("POST", base+"/cancel", map[string]any{"reason": "Discard synthetic upload", "path": "/unowned"}, 400, "Problem")
	admin.Write("POST", base+"/cancel", map[string]any{"reason": "Discard synthetic upload"}, 200, "ModelImportResponse")
	admin.Call("GET", base, nil, 200, "ModelImportResponse")
}
