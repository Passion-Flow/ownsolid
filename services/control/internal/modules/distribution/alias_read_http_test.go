package distribution_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestPostgresAliasReadCapabilitiesAtRegisteredEntrances(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	policy := c.s.Identity.Config.Policy()
	policy.Distribution.ContextTokens = 1024
	c.s.Identity.Config.Runtime = &policy
	alias := c.published(t)
	legacy := uuid.NewString()
	if _, err := c.s.DB.Exec(ctx, `INSERT INTO model_aliases
        (id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,created_by,idempotency_key,fingerprint)
        SELECT $1,tenant_id,workspace_id,node_id,gateway_id,'legacy-read-without-snapshot',deployment_id,deployment_revision,created_by,$2,fingerprint
        FROM model_aliases WHERE id=$3`, legacy, uuid.NewString(), alias.ID); err != nil {
		t.Fatal(err)
	}
	legacyAlias, err := c.s.Alias(ctx, c.a, alias.WorkspaceID, legacy)
	if err != nil || !legacyAlias.Available {
		t.Fatal("legacy fixture must remain callable without a snapshot", err)
	}

	handler := identityhttp.New(c.s.Identity)
	// Enable the actual registered owner, not the separate fixture authority.
	handler.Distribution.Service.PublishAliasCapabilities = true
	server := httpfixture.NewTLSServer(t, handler)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	invitation, err := c.s.Identity.Invite(ctx, c.a, "", "createInvitation", uuid.NewString(), "read-capability-member@example.invalid", "en", 0, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	password := fixture.Password()
	if err := c.s.Identity.Register(ctx, fixture.InvitationToken(t, c.s.Identity, invitation.ID), "Synthetic read member", password, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	admin.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, http.StatusOK, "")
	portal.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "read-capability-member@example.invalid", "password": password}, http.StatusOK, "")
	adminPath := func(workspace string) string {
		return "/api/v1/admin/distribution/workspaces/" + workspace + "/aliases"
	}
	portalPath := func(workspace string) string { return "/api/v1/workspaces/" + workspace + "/distribution/aliases" }
	type expectation struct {
		alias distribution.Alias
		known bool
	}
	assertView := func(value map[string]any, want expectation) {
		t.Helper()
		// The draft adds exactly one optional closed object to the released read.
		base := maps.Clone(value)
		delete(base, "capabilities")
		if err := schemas["ModelAlias"].Validate(base); err != nil {
			t.Fatal("candidate changed the released alias fields", err)
		}
		if value["id"] != want.alias.ID || value["revision"] != float64(want.alias.Revision) ||
			value["deployment_id"] != want.alias.DeploymentID || value["deployment_revision"] != float64(want.alias.DeploymentRevision) ||
			(value["disabled_at"] != nil) != (want.alias.DisabledAt != nil) {
			t.Fatal("capability view changed the exact alias or deployment")
		}
		if want.alias.Description != nil && (value["description"] != *want.alias.Description ||
			want.alias.DescriptionRevision == nil || value["description_revision"] != float64(*want.alias.DescriptionRevision)) {
			t.Fatal("capability read lost the independent description fields")
		}
		raw, present := value["capabilities"]
		if present != want.known {
			t.Fatalf("capability presence: got %t, want %t", present, want.known)
		}
		if present {
			capabilities, ok := raw.(map[string]any)
			if !ok || len(capabilities) != 2 || capabilities["text_generation_verified"] != true ||
				capabilities["context_token_ceiling"] != float64(policy.Distribution.ContextTokens) {
				t.Fatal("capability object does not match DEV's exact draft contract")
			}
		}
	}
	verifyReads := func(client *httpfixture.Client, path string, wants map[string]expectation) {
		t.Helper()
		seen := map[string]bool{}
		next := path + "?limit=1"
		for page := 0; next != ""; page++ {
			if page >= len(wants)+1 {
				t.Fatal("alias cursor did not terminate")
			}
			result := client.Call(http.MethodGet, next, nil, http.StatusOK, "")
			items := result["items"].([]any)
			base := maps.Clone(result)
			baseItems := make([]any, 0, len(items))
			for _, item := range items {
				value := item.(map[string]any)
				id := value["id"].(string)
				want, ok := wants[id]
				if !ok || seen[id] {
					t.Fatal("mixed alias page returned an unexpected or repeated identity")
				}
				seen[id] = true
				assertView(value, want)
				withoutCapabilities := maps.Clone(value)
				delete(withoutCapabilities, "capabilities")
				baseItems = append(baseItems, withoutCapabilities)
			}
			base["items"] = baseItems
			if err := schemas["ModelAliasPageResponse"].Validate(map[string]any{"data": base}); err != nil {
				t.Fatal("candidate changed the published page envelope", err)
			}
			next = ""
			if result["next_cursor"] != nil {
				next = path + "?limit=1&cursor=" + url.QueryEscape(result["next_cursor"].(string))
			}
		}
		if len(seen) != len(wants) {
			t.Fatal("mixed alias pages lost an authorized alias")
		}
		for id, want := range wants {
			assertView(client.Call(http.MethodGet, path+"/"+id, nil, http.StatusOK, ""), want)
		}
	}
	views := func(want distribution.Alias, known bool, portalHidden bool) {
		t.Helper()
		expected := map[string]expectation{legacy: {legacyAlias, false}, alias.ID: {want, known}}
		verifyReads(admin, adminPath(alias.WorkspaceID), expected)
		if portalHidden {
			delete(expected, alias.ID)
		}
		verifyReads(portal, portalPath(alias.WorkspaceID), expected)
	}
	views(alias, true, false)

	// Description writes keep the route snapshot while both real read entrances expose the new text.
	if alias.Description == nil || alias.DescriptionRevision == nil {
		t.Fatal("normal schema must expose the paired description fields")
	}
	routeRevision := alias.Revision
	firstEdit := distribution.AliasDescriptionEdit{
		Description: "Synthetic verified-target description", ExpectedDescriptionRevision: *alias.DescriptionRevision,
		Reason: "Synthetic capability read description edit",
	}
	firstEditKey := uuid.NewString()
	editPath := adminPath(alias.WorkspaceID) + "/" + alias.ID + "/description"
	editDescription := func(input distribution.AliasDescriptionEdit, key string) {
		t.Helper()
		admin.Headers = map[string]string{"Idempotency-Key": key}
		result := admin.Write(http.MethodPost, editPath, input, http.StatusOK, "AliasDescriptionResultResponse")
		if result["description"] != input.Description || result["description_revision"] != float64(input.ExpectedDescriptionRevision+1) ||
			result["applied_description_revision"] != float64(input.ExpectedDescriptionRevision+1) || result["replayed"] != false {
			t.Fatal("description edit did not return its current and applied revisions")
		}
		current, err := c.s.Alias(ctx, c.a, alias.WorkspaceID, alias.ID)
		if err != nil || current.Revision != routeRevision || current.Description == nil || *current.Description != input.Description ||
			current.DescriptionRevision == nil || *current.DescriptionRevision != input.ExpectedDescriptionRevision+1 {
			t.Fatal("description edit changed routing authority or lost its text", err)
		}
		alias = current
		views(alias, true, false)
	}
	editDescription(firstEdit, firstEditKey)
	editDescription(distribution.AliasDescriptionEdit{
		Description: "", ExpectedDescriptionRevision: *alias.DescriptionRevision,
		Reason: "Clear synthetic capability read description",
	}, uuid.NewString())
	admin.Headers = map[string]string{"Idempotency-Key": firstEditKey}
	replayed := admin.Write(http.MethodPost, editPath, firstEdit, http.StatusOK, "AliasDescriptionResultResponse")
	if replayed["description"] != "" || replayed["description_revision"] != float64(*alias.DescriptionRevision) ||
		replayed["applied_description_revision"] != float64(firstEdit.ExpectedDescriptionRevision+1) || replayed["replayed"] != true {
		t.Fatal("historical description replay replaced the current read state")
	}
	var targetVersions int
	if err := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM model_alias_target_versions WHERE alias_id=$1", alias.ID).Scan(&targetVersions); err != nil || targetVersions != 1 {
		t.Fatal("description edit or replay changed immutable target history", err)
	}
	views(alias, true, false)

	separate, err := c.s.Workspaces.Create(ctx, c.a, "Synthetic separate read workspace", c.a.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic workspace','active')", foreignWorkspace, foreignTenant); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{portalPath(separate.ID), portalPath(foreignWorkspace), adminPath(foreignWorkspace)} {
		client := portal
		if path == adminPath(foreignWorkspace) {
			client = admin
		}
		for _, suffix := range []string{"", "/" + alias.ID} {
			if body := client.Call(http.MethodGet, path+suffix, nil, http.StatusNotFound, "Problem"); body["code"] != "RESOURCE_NOT_FOUND" {
				t.Fatal("candidate leaked a foreign scope")
			}
		}
	}
	verifyReads(admin, adminPath(separate.ID), map[string]expectation{})
	admin.Call(http.MethodGet, adminPath(separate.ID)+"/"+alias.ID, nil, http.StatusNotFound, "Problem")

	// Stale evidence is omitted without hiding the otherwise visible alias.
	if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET deployment_revision=deployment_revision+1 WHERE id=$1", alias.ID); err != nil {
		t.Fatal(err)
	}
	stale := alias
	stale.DeploymentRevision++
	views(stale, false, false)
	if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET deployment_revision=$2 WHERE id=$1", alias.ID, alias.DeploymentRevision); err != nil {
		t.Fatal(err)
	}

	cutover := func(input distribution.CutoverRequest) distribution.Alias {
		t.Helper()
		input.Reason = "Synthetic read capability cutover"
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		// Mutation responses retain the released closed alias component.
		result := admin.Write(http.MethodPost, adminPath(alias.WorkspaceID)+"/"+alias.ID+"/cutovers", input, http.StatusOK, "AliasCutoverResultResponse")
		raw, err := json.Marshal(result["alias"])
		if err != nil {
			t.Fatal(err)
		}
		var next distribution.Alias
		if err := json.Unmarshal(raw, &next); err != nil {
			t.Fatal(err)
		}
		return next
	}
	paused := cutover(distribution.CutoverRequest{Action: "pause", ExpectedRevision: alias.Revision})
	views(paused, false, true)
	if body := portal.Call(http.MethodGet, portalPath(alias.WorkspaceID)+"/"+alias.ID, nil, http.StatusNotFound, "Problem"); body["code"] != "MODEL_ALIAS_NOT_FOUND" {
		t.Fatal("paused alias became visible to Portal")
	}
	replacement := c.replaceBaseDeployment(t, alias.DeploymentID)
	retargeted := cutover(distribution.CutoverRequest{Action: "retarget", ExpectedRevision: paused.Revision, DeploymentID: &replacement.ID, DeploymentRevision: &replacement.Revision})
	resumed := cutover(distribution.CutoverRequest{Action: "resume", ExpectedRevision: retargeted.Revision})
	if resumed.DeploymentID == alias.DeploymentID {
		t.Fatal("fixture did not change the target")
	}
	views(resumed, true, false)

	// Default composition must not depend on the evidence query or change its contract.
	if _, err := c.s.DB.Exec(ctx, "ALTER TABLE model_alias_target_versions RENAME TO synthetic_unavailable_capability_evidence"); err != nil {
		t.Fatal(err)
	}

	handler.Distribution.Service.PublishAliasCapabilities = false
	for _, probe := range []struct {
		client *httpfixture.Client
		path   string
	}{{admin, adminPath(alias.WorkspaceID)}, {portal, portalPath(alias.WorkspaceID)}} {
		probe.client.Call(http.MethodGet, probe.path, nil, http.StatusOK, "ModelAliasPageResponse")
		probe.client.Call(http.MethodGet, probe.path+"/"+alias.ID, nil, http.StatusOK, "ModelAliasResponse")
	}
	handler.Distribution.Service.PublishAliasCapabilities = true
	for _, probe := range []struct {
		client *httpfixture.Client
		path   string
	}{{admin, adminPath(alias.WorkspaceID)}, {portal, portalPath(alias.WorkspaceID)}} {
		for _, suffix := range []string{"", "/" + alias.ID} {
			if problem := probe.client.Call(http.MethodGet, probe.path+suffix, nil, http.StatusServiceUnavailable, "Problem"); problem["code"] != "STORAGE_UNAVAILABLE" {
				t.Fatal("failed evidence lookup was silently treated as absent")
			}
		}
	}
	if _, err := c.s.DB.Exec(ctx, "ALTER TABLE synthetic_unavailable_capability_evidence RENAME TO model_alias_target_versions"); err != nil {
		t.Fatal(err)
	}
}
