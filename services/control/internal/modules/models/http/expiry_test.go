package http_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestModelImportHTTPSExpiryIsScopedAndDistinctFromCancellation(t *testing.T) {
	const (
		imports = "/api/v1/admin/model-imports"
		expired = "MODEL_IMPORT_EXPIRED"
	)
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s.Config.Models.ArtifactRoot = root
	handler := identityhttp.New(s)
	server := httpfixture.NewTLSServer(t, handler)
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/models/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	me := admin.Call("GET", "/api/v1/me", nil, 200, "")["workspace"].(map[string]any)
	workspace, tenant := me["id"].(string), me["tenant_id"].(string)
	input := models.ImportRequest{
		WorkspaceID: workspace, Name: "Isolated upload expiry", CatalogDigest: wire.ModelCatalogDigest(),
		ExpectedBytes: modelartifact.MinimumArchiveBytes(), LicenseAcknowledged: true, Reason: "Verify the operator expiry result",
	}
	replay := func(key string) map[string]any {
		admin.Headers = map[string]string{"Idempotency-Key": key}
		defer func() { admin.Headers = nil }()
		return admin.Write("POST", imports, input, 201, "ModelImportResponse")
	}
	resolvePath := func(scope, key string) string {
		return imports + "/resolve?" + url.Values{"workspace_id": {scope}, "idempotency_key": {key}}.Encode()
	}
	key := uuid.NewString()
	original := replay(key)
	id := original["id"].(string)
	detail := imports + "/" + id
	chunk := bytes.Repeat([]byte("S"), int(models.ChunkBytes))
	sum := sha256.Sum256(chunk)
	chunkHeaders := map[string]string{
		"Content-Type": "application/octet-stream", "X-Chunk-Offset": "0", "X-Chunk-SHA256": hex.EncodeToString(sum[:]),
	}
	admin.Headers = chunkHeaders
	admin.Call("POST", detail+"/chunks", chunk, 200, "ModelImportResponse")
	admin.Headers = nil

	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(t.Context(), statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE model_imports SET updated_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, id)
	worker := *handler.Models.Service
	worker.ImportExpiry = &models.ImportExpiryPolicy{WorkspaceID: workspace, InactiveFor: time.Hour}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	t.Cleanup(func() { stop(); <-done })

	var detailResponse map[string]any
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		detailResponse = admin.Call("GET", detail, nil, 200, "ModelImportResponse")
		if detailResponse["state"] == "cancelled" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("the expiry worker did not reach a terminal state")
		case <-tick.C:
		}
	}
	stop()
	<-done

	requireExpired := func(value map[string]any) {
		t.Helper()
		if value["id"] != id || value["state"] != "cancelled" || value["error_code"] != expired ||
			value["uploaded_bytes"] != float64(models.ChunkBytes) || value["model_id"] != nil {
			t.Fatalf("the expiry projection lost its terminal identity: %+v", value)
		}
	}
	requireResolved := func(scope, key string, require func(map[string]any)) {
		t.Helper()
		out := admin.Call("GET", resolvePath(scope, key), nil, 200, "ModelImportResolutionResponse")
		value, ok := out["import"].(map[string]any)
		if out["status"] != "found" || !ok {
			t.Fatalf("the operation key did not resolve: %+v", out)
		}
		require(value)
	}
	requireExpired(detailResponse)
	list := admin.Call("GET", imports+"?workspace_id="+workspace, nil, 200, "ModelImportListResponse")
	rows := list["items"].([]any)
	if len(rows) != 1 {
		t.Fatalf("unexpected import history: %d", len(rows))
	}
	requireExpired(rows[0].(map[string]any))
	requireResolved(workspace, key, requireExpired)
	requireExpired(replay(key))
	reason := map[string]any{"reason": "Verify terminal replay"}
	requireExpired(admin.Write("POST", detail+"/cancel", reason, 200, "ModelImportResponse"))
	if _, err := os.Lstat(filepath.Join(root, "imports", id)); !os.IsNotExist(err) {
		t.Fatalf("terminal expiry did not discard staging: %v", err)
	}
	admin.Headers = chunkHeaders
	resume := admin.Call("POST", detail+"/chunks", chunk, 409, "Problem")
	admin.Headers = nil
	complete := admin.Write("POST", detail+"/complete", reason, 409, "Problem")
	for _, failure := range []map[string]any{resume, complete} {
		if failure["code"] != "MODEL_IMPORT_STATE_CONFLICT" || failure["error_code"] != nil {
			t.Fatalf("unexpected expired mutation projection: %+v", failure)
		}
	}

	for _, path := range []string{detail, imports + "?workspace_id=" + workspace, resolvePath(workspace, key)} {
		refused := portal.Call("GET", path, nil, 404, "Problem")
		if refused["import"] != nil || refused["error_code"] != nil {
			t.Fatalf("Portal received import state: %+v", refused)
		}
	}
	other := uuid.NewString()
	exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other expiry scope','active')`, other, tenant)
	absent := admin.Call("GET", resolvePath(other, key), nil, 200, "ModelImportResolutionResponse")
	if absent["status"] != "absent" || absent["import"] != nil {
		t.Fatalf("the operation key escaped its workspace: %+v", absent)
	}
	foreignTenant, foreignWorkspace, foreignImport := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign expiry tenant','active')`, foreignTenant)
	exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign expiry workspace','active')`, foreignWorkspace, foreignTenant)
	exec(`INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,uploaded_bytes,state,error_code,
		requested_by,requested_epoch,idempotency_key,fingerprint,reason)
		SELECT $1,$2,$3,name,catalog_digest,expected_bytes,uploaded_bytes,state,error_code,
		       requested_by,requested_epoch,idempotency_key,fingerprint,reason
		FROM model_imports WHERE id=$4`, foreignImport, foreignTenant, foreignWorkspace, id)
	for _, path := range []string{imports + "/" + foreignImport, resolvePath(foreignWorkspace, key)} {
		refused := admin.Call("GET", path, nil, 404, "Problem")
		if refused["import"] != nil || refused["error_code"] != nil {
			t.Fatalf("foreign installation data escaped scope: %+v", refused)
		}
	}

	freshKey := uuid.NewString()
	fresh := replay(freshKey)
	if fresh["id"] == id || fresh["state"] != "uploading" || fresh["error_code"] != nil {
		t.Fatalf("a new key did not create a fresh upload: %+v", fresh)
	}
	freshID := fresh["id"].(string)
	requireOrdinaryCancel := func(value map[string]any) {
		t.Helper()
		if value["id"] != freshID || value["state"] != "cancelled" || value["error_code"] != nil {
			t.Fatalf("ordinary cancellation acquired an expiry reason: %+v", value)
		}
	}
	for range 2 {
		requireOrdinaryCancel(admin.Write("POST", imports+"/"+freshID+"/cancel", reason, 200, "ModelImportResponse"))
	}
	requireResolved(workspace, freshKey, requireOrdinaryCancel)
	requireOrdinaryCancel(replay(freshKey))
	var intents, completions, cancellations int
	err := s.DB.QueryRow(t.Context(), `
		SELECT count(*) FILTER(WHERE action='model.import_expiry_requested' AND target_id=$1 AND audience='system'),
		       count(*) FILTER(WHERE action='model.import_expired' AND target_id=$1 AND audience='system'),
		       count(*) FILTER(WHERE action='model.import_cancelled' AND target_id=$2 AND audience='admin')
		FROM audit_events WHERE target_id IN ($1,$2)`, id, freshID).Scan(&intents, &completions, &cancellations)
	if err != nil || intents != 1 || completions != 1 || cancellations != 1 {
		t.Fatalf("expiry or cancellation replay duplicated its audit: %d/%d/%d, %v", intents, completions, cancellations, err)
	}
}
