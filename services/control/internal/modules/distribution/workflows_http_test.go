package distribution_test

import (
	"testing"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// Real TLS/session/CSRF and generated response schemas. This metadata profile
// deliberately does not start an executor and never fabricates a model result.
func TestM13ActualWorkflowHumanTLSContractsCSRFStrictInputAndRevision(t *testing.T) {
	c := setup(t)
	c.s.Identity.Config.Workflows.Enabled = true
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/workflows/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	collection := "/api/v1/workspaces/" + c.p.Me.Workspace.ID + "/workflows"
	key := uuid.NewString()
	portal.Headers = map[string]string{"Idempotency-Key": key}
	input := map[string]any{"name": "HTTP Caf\u00e9 \u5de5\u4f5c\u6d41 <script>", "reason": "Synthetic real TLS workflow metadata"}
	draft := portal.Write("POST", collection, input, 201, "WorkflowResponse")
	id := draft["id"].(string)
	path := collection + "/" + id
	replay := portal.Write("POST", collection, input, 201, "WorkflowResponse")
	if replay["id"] != id || replay["draft_sha256"] != draft["draft_sha256"] {
		t.Fatal("HTTP replay changed immutable input/result")
	}
	portal.Write("POST", collection, map[string]string{"name": "Changed intent", "reason": input["reason"].(string)}, 409, "Problem")
	portal.Call("GET", path, nil, 200, "WorkflowResponse")
	admin.Call("GET", "/api/v1/admin/workflows/"+id, nil, 200, "WorkflowResponse")
	portal.Call("GET", "/api/v1/admin/workflows/"+id, nil, 404, "Problem")
	portal.Call("GET", collection+"?limit=1", nil, 200, "WorkflowPageResponse")
	portal.Call("GET", collection+"?limit=1&unknown=ignored", nil, 400, "Problem")
	portal.Call("GET", path+"?workspace_id="+uuid.NewString(), nil, 400, "Problem")
	portal.Call("GET", collection+"/configuration", nil, 200, "WorkflowConfigurationResponse")
	for _, raw := range [][]byte{
		[]byte(`{"name":"x","reason":"test","workspace_id":"untrusted"}`),
		[]byte(`{"name":"x","name":"y","reason":"test"}`),
		[]byte(`{"name":"x","reason":null}`),
		[]byte(`{"Name":"x","reason":"test"}`),
		[]byte(`{"name":"x","reason":"test"} {}`),
	} {
		portal.Headers["Idempotency-Key"] = uuid.NewString()
		portal.Write("POST", collection, raw, 400, "Problem")
	}
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	csrf := portal.CSRF
	portal.CSRF = ""
	portal.Call("POST", collection, input, 403, "Problem")
	portal.CSRF = csrf
	portal.Headers["Origin"] = "https://foreign.example.invalid"
	portal.Call("POST", collection, input, 403, "Problem")
	delete(portal.Headers, "Origin")
	portal.Headers["Content-Type"] = "text/plain"
	portal.Call("POST", collection, input, 415, "Problem")
	delete(portal.Headers, "Content-Type")
	validation := map[string]any{"expected_revision": draft["revision"], "reason": "Reject missing executable inputs"}
	result := portal.Write("POST", path+"/validations", validation, 200, "WorkflowValidationResponse")
	if result["valid"] != false {
		t.Fatal("incomplete HTTP graph became executable")
	}
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	portal.Write("POST", path+"/publications", validation, 422, "Problem")
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	portal.Write("POST", path+"/debug-runs", map[string]any{"expected_revision": draft["revision"], "question": "Synthetic unavailable executor question", "reason": "No started executor must not return accepted"}, 503, "Problem")
	update := map[string]any{"name": draft["name"], "graph": draft["draft_graph"], "enabled": false, "expected_revision": draft["revision"], "reason": "Actual HTTP revision and disable"}
	changed := portal.Write("PATCH", path, update, 200, "WorkflowResponse")
	if changed["revision"].(float64) != draft["revision"].(float64)+1 || changed["enabled"] != false {
		t.Fatal("HTTP revision/false value lost")
	}
	portal.Write("PATCH", path, update, 409, "Problem")
	var workflowsCount, runsCount, versionsCount int
	if e := c.s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM workflows),(SELECT count(*) FROM workflow_runs),(SELECT count(*) FROM workflow_versions)").Scan(&workflowsCount, &runsCount, &versionsCount); e != nil || workflowsCount != 1 || runsCount != 0 || versionsCount != 0 {
		t.Fatal("rejected HTTP inputs created state or fake model success", e)
	}
}
