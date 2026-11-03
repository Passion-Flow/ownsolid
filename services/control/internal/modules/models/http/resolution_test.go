package http_test

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestModelImportHTTPSIntentResolutionStrictScopeAndNoMutation(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	s.Config.Models.ArtifactRoot = root
	server := httpfixture.NewTLSServer(t, identityhttp.New(s))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/models/openapi.json")
	admin, portal := httpfixture.New(t, server, "admin", schemas), httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	me := admin.Call("GET", "/api/v1/me", nil, 200, "")["workspace"].(map[string]any)
	workspace, tenant := me["id"].(string), me["tenant_id"].(string)
	key := uuid.NewString()
	path := func(scope, operation string) string {
		return "/api/v1/admin/model-imports/resolve?" + url.Values{"workspace_id": {scope}, "idempotency_key": {operation}}.Encode()
	}
	query := path(workspace, key)
	absent := admin.Call("GET", query, nil, 200, "ModelImportResolutionResponse")
	if absent["status"] != "absent" || absent["import"] != nil {
		t.Fatal("explicit absent HTTPS resolution")
	}
	admin.Headers = map[string]string{"Idempotency-Key": key}
	input := models.ImportRequest{WorkspaceID: workspace, Name: "HTTPS resolved intent", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: modelartifact.MinimumArchiveBytes(), LicenseAcknowledged: true, Reason: "Isolated HTTPS resolution"}
	created := admin.Write("POST", "/api/v1/admin/model-imports", input, 201, "ModelImportResponse")
	admin.Headers = map[string]string{}
	id := created["id"].(string)
	var before int
	if e := s.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_events`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		found := admin.Call("GET", query, nil, 200, "ModelImportResolutionResponse")
		value, ok := found["import"].(map[string]any)
		if found["status"] != "found" || !ok || value["id"] != id {
			t.Fatal("actual HTTPS durable resolution")
		}
	}
	var after int
	if e := s.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_events`).Scan(&after); e != nil || after != before {
		t.Fatal("read resolution wrote audit/mutation", e)
	}
	for _, bad := range []string{"/api/v1/admin/model-imports/resolve", query + "&unexpected=x", query + "&workspace_id=" + workspace, path(workspace, "not-a-key"), query + "&broken=%zz"} {
		admin.Call("GET", bad, nil, 400, "Problem")
	}
	portal.Call("GET", query, nil, 404, "Problem")
	other := uuid.NewString()
	if _, e := s.DB.Exec(context.Background(), `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other resolution scope','active')`, other, tenant); e != nil {
		t.Fatal(e)
	}
	scoped := admin.Call("GET", path(other, key), nil, 200, "ModelImportResolutionResponse")
	if scoped["status"] != "absent" || scoped["import"] != nil {
		t.Fatal("cross-Workspace HTTPS resolution leak")
	}
	admin.Call("GET", path(uuid.NewString(), key), nil, 404, "Problem")
	admin.Write("POST", "/api/v1/admin/model-imports/"+id+"/cancel", map[string]any{"reason": "Discard resolved synthetic intent"}, 200, "ModelImportResponse")
}
