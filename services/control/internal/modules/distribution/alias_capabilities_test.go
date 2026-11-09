package distribution_test

import (
	"net/http"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func TestPostgresAliasCapabilitiesFollowVerifiedScopedTarget(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	policy := c.s.Identity.Config.Policy()
	policy.Distribution.ContextTokens = 1024
	c.s.Identity.Config.Runtime = &policy
	access := c.access(t)
	alias := access.alias
	assertKnown := func(actor identity.Auth, expected distribution.Alias) {
		t.Helper()
		got, err := c.s.AliasCapabilities(ctx, actor, expected.WorkspaceID, expected.ID)
		want := distribution.AliasCapabilityMetadata{
			AliasID: expected.ID, AliasRevision: expected.Revision, DeploymentID: expected.DeploymentID,
			DeploymentRevision: expected.DeploymentRevision, VerifiedTextGeneration: true, ContextTokenCeiling: policy.Distribution.ContextTokens,
		}
		if err != nil || got == nil || *got != want {
			t.Fatalf("capability did not describe the verified target: got %+v, err %v, want %+v", got, err, want)
		}
	}
	assertUnknown := func(id string) {
		t.Helper()
		for _, actor := range []identity.Auth{c.a, access.owner} {
			got, err := c.s.AliasCapabilities(ctx, actor, alias.WorkspaceID, id)
			if err != nil || got != nil {
				t.Fatalf("unverified target exposed metadata: %+v, %v", got, err)
			}
		}
	}
	assertRefused := func(actor identity.Auth, workspace, id, code string) {
		t.Helper()
		got, err := c.s.AliasCapabilities(ctx, actor, workspace, id)
		if got != nil || err == nil || err.Error() != code {
			t.Fatalf("alias visibility changed: expected %s, got %+v, %v", code, got, err)
		}
	}
	assertKnown(c.a, alias)
	assertKnown(access.owner, alias)

	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, http.StatusOK, "")
	portal.Call(http.MethodGet, "/api/v1/workspaces/"+alias.WorkspaceID+"/distribution/aliases/"+alias.ID, nil, http.StatusOK, "ModelAliasResponse")

	other, err := c.s.Workspaces.Create(ctx, c.a, "Synthetic separate workspace", c.a.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assertRefused(c.a, other.ID, alias.ID, "MODEL_ALIAS_NOT_FOUND")
	assertRefused(access.owner, other.ID, alias.ID, "RESOURCE_NOT_FOUND")
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic workspace','active')", foreignWorkspace, foreignTenant); err != nil {
		t.Fatal(err)
	}
	assertRefused(c.a, foreignWorkspace, alias.ID, "RESOURCE_NOT_FOUND")
	assertRefused(access.owner, foreignWorkspace, alias.ID, "RESOURCE_NOT_FOUND")

	legacy := uuid.NewString()
	if _, err := c.s.DB.Exec(ctx, `INSERT INTO model_aliases
		(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,created_by,idempotency_key,fingerprint)
		SELECT $1,tenant_id,workspace_id,node_id,gateway_id,'legacy-without-snapshot',deployment_id,deployment_revision,created_by,$2,fingerprint
		FROM model_aliases WHERE id=$3`, legacy, uuid.NewString(), alias.ID); err != nil {
		t.Fatal(err)
	}
	legacyAlias, err := c.s.Alias(ctx, access.owner, alias.WorkspaceID, legacy)
	if err != nil || !legacyAlias.Available {
		t.Fatal("legacy fixture needs an otherwise callable alias", err)
	}
	assertUnknown(legacy)

	if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET deployment_revision=deployment_revision+1 WHERE id=$1", alias.ID); err != nil {
		t.Fatal(err)
	}
	assertUnknown(alias.ID)
	if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET deployment_revision=$2 WHERE id=$1", alias.ID, alias.DeploymentRevision); err != nil {
		t.Fatal(err)
	}

	cutover := func(in distribution.CutoverRequest) distribution.Alias {
		t.Helper()
		in.Reason = "Synthetic capability qualification"
		out, err := c.s.Cutover(ctx, c.a, alias.WorkspaceID, alias.ID, in, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return out.Alias
	}
	paused := cutover(distribution.CutoverRequest{Action: "pause", ExpectedRevision: alias.Revision})
	if got, err := c.s.AliasCapabilities(ctx, c.a, alias.WorkspaceID, alias.ID); err != nil || got != nil {
		t.Fatalf("paused alias exposed capability metadata: %+v, %v", got, err)
	}
	assertRefused(access.owner, alias.WorkspaceID, alias.ID, "MODEL_ALIAS_NOT_FOUND")
	next := c.replaceBaseDeployment(t, alias.DeploymentID)
	assertUnknown(legacy)
	retargeted := cutover(distribution.CutoverRequest{
		Action: "retarget", ExpectedRevision: paused.Revision, DeploymentID: &next.ID, DeploymentRevision: &next.Revision,
	})
	resumed := cutover(distribution.CutoverRequest{Action: "resume", ExpectedRevision: retargeted.Revision})
	if resumed.DeploymentID != next.ID || resumed.DeploymentID == alias.DeploymentID {
		t.Fatal("capability fixture did not change targets")
	}
	assertKnown(c.a, resumed)
	assertKnown(access.owner, resumed)
}
