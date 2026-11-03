package distribution_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestActualHumanDistributionTLSContractsCSRFSecretsHistoryAndRecovery(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	d := c.readyDeployment(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	base := "/api/v1/admin/distribution/workspaces/" + c.a.Me.Workspace.ID
	pbase := "/api/v1/workspaces/" + c.a.Me.Workspace.ID + "/distribution"
	body := map[string]any{"node_id": c.node.NodeID, "name": "Human-created gateway", "client_origin": "https://gateway.example.invalid:22443", "reason": "Synthetic HTTP provision"}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	g := admin.Write("POST", base+"/gateways", body, 201, "ProvisionedGatewayResponse")
	gateway := g["gateway"].(map[string]any)
	gid := gateway["id"].(string)
	if g["secret_available"] != true || g["secret"] == nil {
		t.Fatal("initial secret missing")
	}
	replay := admin.Write("POST", base+"/gateways", body, 200, "ProvisionedGatewayResponse")
	if replay["secret"] != nil || replay["secret_available"] != false {
		t.Fatal("secret replay")
	}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	publish := map[string]any{"gateway_id": gid, "deployment_id": d.ID, "deployment_revision": d.Revision, "name": "human-alias", "reason": "Synthetic publication"}
	alias := admin.Write("POST", base+"/aliases", publish, 200, "ModelAliasResponse")
	aid := alias["id"].(string)
	portal.Call("GET", pbase+"/aliases?limit=1", nil, 200, "ModelAliasPageResponse")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	key := portal.Write("POST", pbase+"/keys", map[string]any{"alias_id": aid, "label": "Human API key", "ttl_seconds": 3600, "reason": "Synthetic access"}, 201, "MintedDistributionKeyResponse")
	k := key["key"].(map[string]any)
	admin.Call("GET", base+"/keys", nil, 200, "DistributionKeyPageResponse")
	portal.Call("GET", pbase+"/keys/"+k["id"].(string), nil, 200, "DistributionKeyResponse")
	portal.Call("GET", pbase+"/gateways", nil, 404, "Problem")
	portal.Call("GET", base+"/gateways", nil, 404, "Problem")
	for _, b := range [][]byte{[]byte(`{"NODE_ID":"synthetic","name":"x","client_origin":"https://example.invalid","reason":"test"}`), []byte(`{"node_id":"synthetic","name":"x","client_origin":"https://example.invalid","reason":"test","workspace_id":"foreign"}`), []byte(`{"node_id":null,"name":"x","client_origin":"https://example.invalid","reason":"test"}`)} {
		admin.Write("POST", base+"/gateways", b, 400, "Problem")
	}
	admin.Call("GET", base+"/keys?created_by=foreign", nil, 400, "Problem")
	csrf := admin.CSRF
	admin.CSRF = ""
	admin.Call("POST", base+"/gateways/"+gid+"/revoke", map[string]any{"reason": "Missing CSRF"}, 403, "Problem")
	admin.CSRF = csrf
	var provision distribution.Provisioned
	raw, _ := json.Marshal(g)
	json.Unmarshal(raw, &provision)
	var model distribution.Alias
	raw, _ = json.Marshal(alias)
	json.Unmarshal(raw, &model)
	var minted distribution.MintedKey
	raw, _ = json.Marshal(key)
	json.Unmarshal(raw, &minted)
	a := access{gateway: provision, alias: model, key: minted, owner: c.p}
	input := a.input()
	if _, e := c.s.Admit(ctx, gid, *a.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	record := portal.Call("GET", pbase+"/requests/"+input.RequestID, nil, 200, "GatewayRequestResponse")
	if record["outcome"] != nil || record["capacity_held"] != true {
		t.Fatal("unknown usage presentation")
	}
	portal.Call("GET", pbase+"/requests", nil, 200, "GatewayRequestPageResponse")
	recoveryPath := base + "/requests/" + input.RequestID + "/recoveries"
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	admin.Write("POST", recoveryPath, map[string]any{"reason": "Synthetic reconcile"}, 409, "Problem")
	admin.Write("POST", base+"/gateways/"+gid+"/revoke", map[string]any{"reason": "Synthetic fence"}, 200, "GatewayInstanceResponse")
	challenge := admin.Write("POST", recoveryPath, map[string]any{"reason": "Synthetic reconcile"}, 200, "GatewayRecoveryResponse")
	rid := challenge["id"].(string)
	recoveryPath += "/" + rid
	portal.Write("POST", pbase+"/requests/"+input.RequestID+"/recoveries", map[string]any{"reason": "Unauthorized"}, 404, "Problem")
	admin.Write("POST", recoveryPath+"/confirm", map[string]any{"deployment_revision": d.Revision, "reason": "Out of order"}, 409, "Problem")
	admin.Write("POST", recoveryPath+"/fence", map[string]any{"receipt": c.fenceFixture(a, rid), "reason": "Synthetic operator fixture"}, 200, "GatewayRecoveryResponse")
	revision := c.stopForRecovery(t, model, d.Revision)
	admin.Write("POST", recoveryPath+"/confirm", map[string]any{"deployment_revision": revision, "reason": "Synthetic confirmed stop"}, 200, "GatewayRecoveryResponse")
	admin.Call("GET", recoveryPath, nil, 200, "GatewayRecoveryResponse")
	admin.Call("GET", base+"/requests?limit=1", nil, 200, "GatewayRequestPageResponse")
	portal.Write("POST", pbase+"/keys/"+minted.Key.ID+"/revoke", map[string]any{"reason": "Synthetic cleanup"}, 200, "DistributionKeyResponse")
	admin.Write("POST", base+"/aliases/"+aid+"/disable", map[string]any{"expected_revision": 1, "reason": "Synthetic cleanup"}, 200, "ModelAliasResponse")
}
