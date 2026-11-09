//go:build gateway_key_purpose_candidate

package distribution_test

import (
	_ "embed"
	"net/http"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

//go:embed testdata/api_key_purpose_candidate.up.sql
var keyPurposeCandidateUp string

//go:embed testdata/api_key_purpose_candidate.down.sql
var keyPurposeCandidateDown string

func TestPostgresGatewayKeyPurposeCandidateAtRegisteredEntrances(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	workspace, tenant := c.a.Me.Workspace.ID, c.a.Me.Workspace.TenantID
	deployment := c.readyDeployment(t)
	gateway, err := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	alias, err := c.s.Publish(ctx, c.a, distribution.PublishRequest{
		WorkspaceID: workspace, GatewayID: gateway.Gateway.ID, Name: "legacy-key-model",
		DeploymentID: deployment.ID, DeploymentRevision: deployment.Revision, Reason: "Synthetic legacy key upgrade",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	type legacyKey struct{ id, raw string }
	seed := func(purpose string) legacyKey {
		t.Helper()
		id, token := uuid.NewString(), security.Token()
		hash := security.MAC(c.s.Identity.HMACKey, purpose, "v1", id, token)
		row, err := c.s.DB.Exec(ctx, `INSERT INTO api_keys
			(id,tenant_id,workspace_id,alias_id,label,secret_hash,created_by,requested_epoch,idempotency_key,fingerprint,expires_at,membership_authority)
			SELECT $1,$2,$3,$4,'Legacy fixture',$5,$6,$7,$8,$9,clock_timestamp()+interval '1 hour',authority_id
			FROM workspace_memberships WHERE tenant_id=$2 AND workspace_id=$3 AND user_id=$6`,
			id, tenant, workspace, alias.ID, hash, c.p.Me.User.ID, c.p.Epoch, uuid.NewString(), make([]byte, 32))
		if err != nil || row.RowsAffected() != 1 {
			t.Fatal("seed predecessor key row", err)
		}
		return legacyKey{id: id, raw: "pk1." + id + "." + token}
	}
	ordinary := seed("m07-api-key")
	operation := seed("m07-accepted-operation-key")
	if _, err := c.s.DB.Exec(ctx, keyPurposeCandidateUp); err != nil {
		t.Fatal(err)
	}

	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	machine := keyPurposeMachineProbe(t, server, gateway.Gateway.ID, *gateway.Secret)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, http.StatusOK, "")
	keysPath := "/api/v1/workspaces/" + workspace + "/distribution/keys"
	rotateBody := map[string]any{"overlap_seconds": 0, "reason": "Synthetic scoped rotation"}
	refuseRotation := func(id string) {
		t.Helper()
		portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		p := portal.Write(http.MethodPost, keysPath+"/"+id+"/rotate", rotateBody, http.StatusConflict, "Problem")
		if p["code"] != "API_KEY_NOT_ACTIVE" || p["secret"] != nil || p["data"] != nil {
			t.Fatal("ineligible rotation exposed a successor")
		}
	}
	assertNoSuccessor := func(id string) {
		t.Helper()
		var count int
		if err := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE rotated_from=$1", id).Scan(&count); err != nil || count != 0 {
			t.Fatal("operation key gained a durable successor", err, count)
		}
	}
	purpose := func(id string) *bool {
		t.Helper()
		var stored *bool
		if err := c.s.DB.QueryRow(ctx, "SELECT operation_only FROM api_keys WHERE id=$1", id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		return stored
	}
	request := func(key legacyKey, model string) wire.AdmissionRequest {
		return wire.AdmissionRequest{RequestID: uuid.NewString(), APIKey: key.raw, Model: model, MaximumOutputTokens: 16}
	}
	admit := func(key legacyKey) string {
		t.Helper()
		models := machine("models", wire.ModelsRequest{APIKey: key.raw}, http.StatusOK, "")
		bindings := models["models"].([]any)
		if len(bindings) != 1 || bindings[0].(map[string]any)["id"] != alias.ID {
			t.Fatal("key discovered a different alias")
		}
		in := request(key, alias.Name)
		out := machine("admissions", in, http.StatusOK, "")
		if out["request_id"] != in.RequestID || out["workspace_id"] != workspace {
			t.Fatal("key admitted a different request scope")
		}
		return in.RequestID
	}
	report := func(id string) {
		t.Helper()
		machine("outcomes", success(id), http.StatusOK, "")
	}
	assertRefused := func(key legacyKey) {
		t.Helper()
		machine("models", wire.ModelsRequest{APIKey: key.raw}, http.StatusUnauthorized, "API_KEY_INVALID")
		machine("admissions", request(key, alias.Name), http.StatusUnauthorized, "API_KEY_INVALID")
	}
	revoke := func(key legacyKey) {
		t.Helper()
		value := portal.Write(http.MethodPost, keysPath+"/"+key.id+"/revoke",
			map[string]any{"reason": "Synthetic explicit revocation"}, http.StatusOK, "DistributionKeyResponse")
		if value["id"] != key.id || value["state"] != "revoked" {
			t.Fatal("authorized revoke did not retire the requested key")
		}
		assertRefused(key)
	}
	var acceptedOperation string
	for _, probe := range []struct {
		key       legacyKey
		operation bool
	}{{ordinary, false}, {operation, true}} {
		refuseRotation(probe.key.id)
		machine("admissions", request(probe.key, "wrong-alias"), http.StatusNotFound, "MODEL_ALIAS_NOT_FOUND")
		if purpose(probe.key.id) != nil {
			t.Fatal("refused admission classified a predecessor key")
		}
		accepted := admit(probe.key)
		if probe.operation {
			acceptedOperation = accepted
		} else {
			report(accepted)
		}
		if stored := purpose(probe.key.id); stored == nil || *stored != probe.operation {
			t.Fatal("accepted request did not store its proved key purpose")
		}
	}

	refuseRotation(operation.id)
	assertNoSuccessor(operation.id)
	assertRefused(operation)
	revoke(operation)
	report(acceptedOperation)

	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	rotationPath := keysPath + "/" + ordinary.id + "/rotate"
	rotated := portal.Write(http.MethodPost, rotationPath, rotateBody, http.StatusCreated, "MintedDistributionKeyResponse")
	record := rotated["key"].(map[string]any)
	successor := legacyKey{record["id"].(string), rotated["secret"].(string)}
	storedSuccessor := purpose(successor.id)
	if record["rotated_from"] != ordinary.id || record["workspace_id"] != workspace ||
		record["alias_id"] != alias.ID || record["created_by"] != c.p.Me.User.ID ||
		storedSuccessor == nil || *storedSuccessor {
		t.Fatal("ordinary successor lost the original authority")
	}
	replay := portal.Write(http.MethodPost, rotationPath, rotateBody, http.StatusOK, "MintedDistributionKeyResponse")
	if replay["secret"] != nil || replay["secret_available"] != false || replay["key"].(map[string]any)["id"] != successor.id {
		t.Fatal("rotation replay did not redact the original secret")
	}
	assertRefused(ordinary)
	report(admit(successor))
	report(admit(successor))
	revoke(successor)

	var issued legacyKey
	if err := c.s.WithTransientKey(ctx, c.p, keyInput(alias), 2*time.Second, uuid.NewString(), func(raw string) error {
		issued = legacyKey{strings.Split(raw, ".")[1], raw}
		if stored := purpose(issued.id); stored == nil || !*stored {
			t.Fatal("trusted issuance did not store its operation purpose")
		}
		refuseRotation(issued.id)
		machine("admissions", request(issued, "wrong-alias"), http.StatusNotFound, "MODEL_ALIAS_NOT_FOUND")
		report(admit(issued))
		assertRefused(issued)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertNoSuccessor(issued.id)
	assertRefused(issued)
	if _, err := c.s.DB.Exec(ctx, keyPurposeCandidateDown); err != nil {
		t.Fatal(err)
	}
}
