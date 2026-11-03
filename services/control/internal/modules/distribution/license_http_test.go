package distribution_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// Real TLS/SQL/License/authority/CSRF/revocation/outcome/recovery behavior.
// Model/GPU/operator fence/stopped facts are explicit synthetic protocol fixtures;
// native Rust fence execution and real GPU removal remain independent scopes.
func TestM19ExpiredLicenseDistributionRevocationAndRecoveryHTTP(t *testing.T) {
	c := setup(t)
	a := c.access(t)
	ctx := context.Background()
	owner := c.s.Identity
	server := httpfixture.NewTLSServer(t, identityhttp.New(owner))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	base := "/api/v1/admin/distribution/workspaces/" + a.alias.WorkspaceID
	pbase := "/api/v1/workspaces/" + a.alias.WorkspaceID + "/distribution"
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	minted := portal.Write("POST", pbase+"/keys", map[string]any{"alias_id": a.alias.ID, "label": "Synthetic reduction key", "ttl_seconds": 3600, "reason": "Synthetic previously accepted API work"}, 201, "MintedDistributionKeyResponse")
	var key distribution.MintedKey
	raw, _ := json.Marshal(minted)
	if json.Unmarshal(raw, &key) != nil || key.Secret == nil {
		t.Fatal("actual one-time key")
	}
	machine := func(operation string, body any, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		request, _ := http.NewRequestWithContext(ctx, "POST", httpfixture.AdminOrigin+"/gateway/v1/instances/"+a.gateway.Gateway.ID+"/"+operation, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+*a.gateway.Secret)
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal("actual machine HTTP")
		}
		defer response.Body.Close()
		result, e := io.ReadAll(io.LimitReader(response.Body, 16385))
		if e != nil || response.StatusCode != status {
			t.Fatal("machine status", operation, status, response.StatusCode)
		}
		if bytes.Contains(result, []byte(*a.gateway.Secret)) || bytes.Contains(result, []byte(*key.Secret)) {
			t.Fatal("authority disclosed")
		}
		return result
	}
	known, pending := a.input(), a.input()
	known.APIKey = *key.Secret
	pending.APIKey = *key.Secret
	machine("admissions", known, 200)
	machine("admissions", pending, 200)
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	expired, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(26 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	owner.BusinessAdmission = expired
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	denied := portal.Write("POST", pbase+"/keys", map[string]any{"alias_id": a.alias.ID, "label": "Denied new key", "ttl_seconds": 3600, "reason": "Synthetic expired new work"}, 403, "Problem")
	if denied["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
		t.Fatal("new key license refusal")
	}
	portal.Write("POST", pbase+"/keys/"+a.key.Key.ID+"/revoke", map[string]any{"reason": "Foreign creator remains protected"}, 404, "Problem")
	ownPath := pbase + "/keys/" + key.Key.ID + "/revoke"
	portal.CSRF = ""
	portal.Call("POST", ownPath, map[string]any{"reason": "No CSRF"}, 403, "Problem")
	portal.Write("POST", ownPath, map[string]any{"reason": "Strict key reduction", "workspace_id": uuid.NewString()}, 400, "Problem")
	portal.Write("POST", ownPath, map[string]any{"reason": "Synthetic expired key revocation"}, 200, "DistributionKeyResponse")
	portal.Write("POST", ownPath, map[string]any{"reason": "Explicit terminal revocation replay"}, 200, "DistributionKeyResponse")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	aliasPath := base + "/aliases/" + a.alias.ID + "/disable"
	admin.Write("POST", aliasPath, map[string]any{"expected_revision": a.alias.Revision + 1, "reason": "Current revision remains required"}, 409, "Problem")
	disabled := admin.Write("POST", aliasPath, map[string]any{"expected_revision": a.alias.Revision, "reason": "Synthetic expired alias disable"}, 200, "ModelAliasResponse")
	admin.Write("POST", aliasPath, map[string]any{"expected_revision": disabled["revision"], "reason": "Explicit alias disable replay"}, 200, "ModelAliasResponse")
	gatewayPath := base + "/gateways/" + a.gateway.Gateway.ID + "/revoke"
	admin.Write("POST", gatewayPath, map[string]any{"reason": "Synthetic expired gateway revocation"}, 200, "GatewayInstanceResponse")
	admin.Write("POST", gatewayPath, map[string]any{"reason": "Explicit gateway revocation replay"}, 200, "GatewayInstanceResponse")
	// Existing admitted receipts remain valid after all three authority reductions.
	machine("outcomes", success(known.RequestID), 200)
	machine("outcomes", success(known.RequestID), 200)
	recoveryPath := base + "/requests/" + pending.RequestID + "/recoveries"
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	challenge := admin.Write("POST", recoveryPath, map[string]any{"reason": "Synthetic expired unknown capacity recovery"}, 200, "GatewayRecoveryResponse")
	rid := challenge["id"].(string)
	recoveryPath += "/" + rid
	portal.Write("POST", pbase+"/requests/"+pending.RequestID+"/recoveries", map[string]any{"reason": "Portal cannot become recovery operator"}, 404, "Problem")
	admin.Write("POST", recoveryPath+"/confirm", map[string]any{"deployment_revision": a.alias.DeploymentRevision, "reason": "Fence and stop remain required"}, 409, "Problem")
	admin.Write("POST", recoveryPath+"/fence", map[string]any{"receipt": c.fenceFixture(a, rid), "reason": "Explicit synthetic operator fence fixture"}, 200, "GatewayRecoveryResponse")
	revision := c.stopForRecovery(t, a.alias, a.alias.DeploymentRevision)
	admin.Write("POST", recoveryPath+"/confirm", map[string]any{"deployment_revision": revision, "reason": "Explicit synthetic stopped workload confirmation"}, 200, "GatewayRecoveryResponse")
	var rows, held, reported, reconciled, unknownUsage int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE capacity_held),count(*) FILTER(WHERE state='reported'),count(*) FILTER(WHERE state='reconciled'),count(*) FILTER(WHERE state='reconciled' AND outcome IS NULL) FROM gateway_requests").Scan(&rows, &held, &reported, &reconciled, &unknownUsage); e != nil || rows != 2 || held != 0 || reported != 1 || reconciled != 1 || unknownUsage != 1 {
		t.Fatal("revocation/recovery history or unknown usage integrity", e, rows, held, reported, reconciled, unknownUsage)
	}
	for _, action := range []string{"gateway.api_key_revoked", "gateway.instance_revoked", "model.alias_disabled", "gateway.request_reconciled"} {
		var count int
		if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action=$1", action).Scan(&count); e != nil || count != 1 {
			t.Fatal("reduction/recovery audit count", action, e, count)
		}
	}
	var keys int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys").Scan(&keys); e != nil || keys != 2 {
		t.Fatal("denied key minted authority", e, keys)
	}
}
