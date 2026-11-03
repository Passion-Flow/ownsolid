package distribution_test

import (
	"context"
	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"testing"
)

func TestActualAliasCutoverTLSNullableContractCSRFHistoryAndExactReplay(t *testing.T) {
	c := setup(t)
	a := c.access(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	base := "/api/v1/admin/distribution/workspaces/" + a.alias.WorkspaceID + "/aliases/" + a.alias.ID
	body := map[string]any{"action": "pause", "expected_revision": 1, "deployment_id": nil, "deployment_revision": nil, "source_revision": nil, "reason": "Real TLS explicit pause with nullable target union"}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	csrf := admin.CSRF
	admin.CSRF = ""
	admin.Call("POST", base+"/cutovers", body, 403, "Problem")
	admin.CSRF = csrf
	first := admin.Write("POST", base+"/cutovers", body, 200, "AliasCutoverResultResponse")
	if first["applied_revision"] != float64(2) || first["replayed"] != false {
		t.Fatal("typed mutation result")
	}
	replay := admin.Write("POST", base+"/cutovers", body, 200, "AliasCutoverResultResponse")
	if replay["replayed"] != true {
		t.Fatal("exact HTTP replay")
	}
	admin.Call("GET", base+"/targets?limit=1", nil, 200, "AliasTargetPageResponse")
	admin.Call("GET", base+"/targets?alias_id="+uuid.NewString(), nil, 400, "Problem")
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	admin.Write("POST", base+"/cutovers", []byte(`{"action":"resume","expected_revision":2,"deployment_id":null,"deployment_revision":null,"reason":"Missing required source field"}`), 400, "Problem")
	admin.Write("POST", base+"/cutovers", []byte(`{"action":"resume","action":"pause","expected_revision":2,"deployment_id":null,"deployment_revision":null,"source_revision":null,"reason":"Duplicate action"}`), 400, "Problem")
	body["action"] = "resume"
	body["expected_revision"] = 2
	admin.Write("POST", base+"/cutovers", body, 200, "AliasCutoverResultResponse")
	portal.Call("GET", base+"/targets", nil, 404, "Problem")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	portal.Write("POST", "/api/v1/workspaces/"+a.alias.WorkspaceID+"/distribution/aliases/"+a.alias.ID+"/cutovers", body, 404, "Problem")
	if _, e := c.s.AuthorizeModels(context.Background(), a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret); e != nil {
		t.Fatal("original key changed", e)
	}
}
