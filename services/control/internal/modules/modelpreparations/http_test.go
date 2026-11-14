package modelpreparations_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestActualTLSHoldoutDualEntranceSchemasCSRFStrictIntentAndCurrentScope(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := publish(t, f, f.portal.Me.Workspace.ID, rows("http-first"))
	second := publish(t, f, first.WorkspaceID, rows("http-second"))
	handler := transport.New(f.identity)
	if handler.ModelPreparations == nil || handler.Training.Holdouts == nil {
		t.Fatal("production constructor omitted training guard")
	}
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/model-lifecycle/openapi.json")
	portal := httpfixture.New(t, server, "portal", schemas)
	admin := httpfixture.New(t, server, "admin", schemas)
	for _, client := range []*httpfixture.Client{portal, admin} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": f.password}, 200, "")
	}
	base := "/api/v1/workspaces/" + first.WorkspaceID + "/model-holdouts"
	capabilities := portal.Call("GET", base+"/capabilities", nil, 200, "ModelHoldoutCapabilitiesResponse")
	if capabilities["enabled"] != false || capabilities["can_reserve"] != false {
		t.Fatal("uninitialized dataset store advertised ready")
	}
	handler.Datasets.Service.Store = f.datasets.Store
	handler.Datasets.Service.Processor = f.datasets.Processor
	capabilities = portal.Call("GET", base+"/capabilities", nil, 200, "ModelHoldoutCapabilitiesResponse")
	if capabilities["enabled"] != true || capabilities["can_reserve"] != true {
		t.Fatal("initialized registry unavailable without CUDA image")
	}
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	portal.CSRF = ""
	portal.Call("POST", base, input(first), 403, "Problem")
	created := portal.Write("POST", base, input(first), 201, "ModelHoldoutResponse")
	id := created["id"].(string)
	again := portal.Write("POST", base, input(first), 201, "ModelHoldoutResponse")
	if again["id"] != id {
		t.Fatal("HTTP replay changed holdout")
	}
	changed := input(first)
	changed.Name = "Conflicting intent"
	portal.Write("POST", base, changed, 409, "Problem")
	portal.Headers = nil
	portal.Write("POST", base, input(second), 400, "Problem")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	for _, body := range []any{
		[]byte(`{"workspace_id":null}`),
		[]byte(`{"workspace_id":"` + first.WorkspaceID + `","workspace_id":"` + first.WorkspaceID + `"}`),
		[]byte(`{"Workspace_ID":"` + first.WorkspaceID + `"}`),
	} {
		portal.Write("POST", base, body, 400, "Problem")
	}
	raw, _ := json.Marshal(input(second))
	portal.Write("POST", base, append([]byte(`{"unexpected":true,`), raw[1:]...), 400, "Problem")
	wrong := input(second)
	wrong.WorkspaceID = uuid.NewString()
	portal.Write("POST", base, wrong, 400, "Problem")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("POST", "/api/v1/admin/model-holdouts", input(second), 201, "ModelHoldoutResponse")
	page := portal.Call("GET", base+"?limit=1", nil, 200, "ModelHoldoutPageResponse")
	if len(page["items"].([]any)) != 1 || page["next_cursor"] == nil {
		t.Fatal("HTTP keyset pagination")
	}
	cursor := url.QueryEscape(page["next_cursor"].(string))
	portal.Call("GET", base+"?limit=1&cursor="+cursor, nil, 200, "ModelHoldoutPageResponse")
	portal.Call("GET", base+"?limit=1&limit=2", nil, 400, "Problem")
	portal.Call("GET", base+"?unexpected=1", nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/model-holdouts", nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/model-holdouts?workspace_id="+first.WorkspaceID+"&cursor="+cursor, nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/model-holdouts/"+id+"?workspace_id="+first.WorkspaceID, nil, 200, "ModelHoldoutResponse")
	portal.Call("GET", base+"/"+uuid.NewString(), nil, 404, "Problem")
	if _, e := f.identity.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", first.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	capabilities = portal.Call("GET", base+"/capabilities", nil, 200, "ModelHoldoutCapabilitiesResponse")
	if capabilities["can_reserve"] != false {
		t.Fatal("viewer retained write controls")
	}
	portal.Call("GET", base+"/"+id, nil, 200, "ModelHoldoutResponse")
	portal.Write("POST", base, input(first), 403, "Problem")
	if _, e := f.identity.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", first.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	portal.Call("GET", base+"/"+id, nil, 404, "Problem")
	admin.Call("GET", "/api/v1/admin/model-holdouts/"+id+"?workspace_id="+first.WorkspaceID, nil, 200, "ModelHoldoutResponse")
}
