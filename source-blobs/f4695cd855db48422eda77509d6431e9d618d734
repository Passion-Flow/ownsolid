package distribution_test

import (
	"net/http"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestPostgresAliasCapabilityPublicContractBoundary(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	alias := c.published(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)

	const member = "capability-member@example.invalid"
	invitation, err := c.s.Identity.Invite(ctx, c.a, "", "createInvitation", uuid.NewString(), member, "en", 0, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	password := fixture.Password()
	if err := c.s.Identity.Register(ctx, fixture.InvitationToken(t, c.s.Identity, invitation.ID), "Synthetic capability member", password, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	login := func(client *httpfixture.Client, email, password string) {
		t.Helper()
		client.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "password": password}, http.StatusOK, "")
	}
	login(admin, "admin@example.invalid", c.password)
	login(portal, member, password)

	adminPath := func(workspace string) string {
		return "/api/v1/admin/distribution/workspaces/" + workspace + "/aliases"
	}
	portalPath := func(workspace string) string { return "/api/v1/workspaces/" + workspace + "/distribution/aliases" }
	view := func(client *httpfixture.Client, path string, paused bool) {
		t.Helper()
		assertAlias := func(value map[string]any) {
			t.Helper()
			_, added := value["capabilities"]
			if value["id"] != alias.ID || value["deployment_id"] != alias.DeploymentID ||
				value["deployment_revision"] != float64(alias.DeploymentRevision) || (value["disabled_at"] != nil) != paused || added {
				t.Fatal("published alias response changed identity, visibility or capability fields")
			}
		}
		assertAlias(client.Call(http.MethodGet, path+"/"+alias.ID, nil, http.StatusOK, "ModelAliasResponse"))
		items := client.Call(http.MethodGet, path, nil, http.StatusOK, "ModelAliasPageResponse")["items"].([]any)
		if len(items) != 1 {
			t.Fatal("alias list returned an unexpected scope")
		}
		assertAlias(items[0].(map[string]any))
	}
	view(admin, adminPath(alias.WorkspaceID), false)
	view(portal, portalPath(alias.WorkspaceID), false)

	assertNotFound := func(client *httpfixture.Client, path, code string) {
		t.Helper()
		body := client.Call(http.MethodGet, path, nil, http.StatusNotFound, "Problem")
		if body["code"] != code {
			t.Fatalf("expected %s for inaccessible alias path, got %v", code, body["code"])
		}
	}
	separate, err := c.s.Workspaces.Create(ctx, c.a, "Synthetic separate workspace", c.a.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assertNotFound(portal, portalPath(separate.ID), "RESOURCE_NOT_FOUND")
	assertNotFound(portal, portalPath(separate.ID)+"/"+alias.ID, "RESOURCE_NOT_FOUND")
	assertNotFound(admin, adminPath(separate.ID)+"/"+alias.ID, "MODEL_ALIAS_NOT_FOUND")
	if items := admin.Call(http.MethodGet, adminPath(separate.ID), nil, http.StatusOK, "ModelAliasPageResponse")["items"].([]any); len(items) != 0 {
		t.Fatal("Admin alias list returned an alias from a different workspace")
	}

	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic workspace','active')", foreignWorkspace, foreignTenant); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		client *httpfixture.Client
		path   string
	}{{admin, adminPath(foreignWorkspace)}, {portal, portalPath(foreignWorkspace)}} {
		assertNotFound(probe.client, probe.path, "RESOURCE_NOT_FOUND")
		assertNotFound(probe.client, probe.path+"/"+alias.ID, "RESOURCE_NOT_FOUND")
	}

	if _, err := c.s.Cutover(ctx, c.a, alias.WorkspaceID, alias.ID, distribution.CutoverRequest{
		Action: "pause", ExpectedRevision: alias.Revision, Reason: "Synthetic alias visibility qualification",
	}, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	view(admin, adminPath(alias.WorkspaceID), true)
	assertNotFound(portal, portalPath(alias.WorkspaceID)+"/"+alias.ID, "MODEL_ALIAS_NOT_FOUND")
	if items := portal.Call(http.MethodGet, portalPath(alias.WorkspaceID), nil, http.StatusOK, "ModelAliasPageResponse")["items"].([]any); len(items) != 0 {
		t.Fatal("Portal alias list disclosed the paused alias")
	}
}
