package distribution_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

// The shared catalogue owner assigns a migration number after ordering the lineage.
//
//go:embed testdata/alias_description_candidate.up.sql
var aliasDescriptionCandidateUp string

//go:embed testdata/alias_description_candidate.down.sql
var aliasDescriptionCandidateDown string

func TestAliasDescriptionPostgreSQLUpgradeAndHTTPS(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	login := func(client *httpfixture.Client, email, password string) {
		t.Helper()
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, 200, "")
	}
	login(admin, "admin@example.invalid", c.password)
	login(portal, "admin@example.invalid", c.password)
	workspace := c.a.Me.Workspace.ID
	adminPath := "/api/v1/admin/distribution/workspaces/" + workspace + "/aliases"
	portalPath := "/api/v1/workspaces/" + workspace + "/distribution/aliases"
	target := c.readyDeployment(t)
	gateway, err := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	publish := map[string]any{
		"gateway_id": gateway.Gateway.ID, "deployment_id": target.ID,
		"deployment_revision": target.Revision, "name": "legacy-alias", "reason": "Synthetic publication",
	}
	legacyKey, describedKey := uuid.NewString(), uuid.NewString()
	admin.Headers = map[string]string{"Idempotency-Key": legacyKey}
	legacy := admin.Write("POST", adminPath, publish, 200, "ModelAliasResponse")
	legacyID := legacy["id"].(string)
	portal.Call("GET", portalPath+"/"+legacyID, nil, 200, "ModelAliasResponse")
	portal.Call("GET", portalPath, nil, 200, "ModelAliasPageResponse")
	if _, exists := legacy["description"]; exists {
		t.Fatal("predecessor response gained a field")
	}
	problem := func(client *httpfixture.Client, path string, body any, status int, code string) {
		t.Helper()
		got := client.Write("POST", path, body, status, "Problem")
		if got["code"] != code || got["title"] != code || got["status"] != float64(status) {
			t.Fatalf("expected %d %s, got %v %v", status, code, got["status"], got["code"])
		}
	}
	edit := distribution.AliasDescriptionEdit{Description: "Initial edit", ExpectedDescriptionRevision: 1, Reason: "Synthetic catalogue edit"}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	predecessorEditPath := adminPath + "/" + legacyID + "/description"
	problem(admin, predecessorEditPath, edit, 503, "SERVICE_NOT_READY")
	anonymous := httpfixture.New(t, server, "admin", schemas)
	if got := anonymous.Call("POST", predecessorEditPath, edit, 403, "Problem"); got["code"] != "CSRF_INVALID" {
		t.Fatal("unauthenticated edit did not return the standard problem")
	}
	problem(anonymous, predecessorEditPath, edit, 401, "AUTH_REQUIRED")
	predecessorRequest := descriptionEditRequest(t, c, server, "admin@example.invalid", c.password, predecessorEditPath, edit.Description)
	predecessorResponse, err := server.Client().Do(predecessorRequest)
	if err != nil {
		t.Fatal(err)
	}
	var predecessorProblem map[string]any
	decodeError := json.NewDecoder(predecessorResponse.Body).Decode(&predecessorProblem)
	predecessorResponse.Body.Close()
	if decodeError != nil || predecessorResponse.StatusCode != 503 ||
		predecessorResponse.Header.Get("Content-Type") != "application/problem+json" ||
		predecessorResponse.Header.Get("Retry-After") != strconv.Itoa(c.s.Identity.Config.Policy().HTTP.ServiceUnavailableRetrySeconds) ||
		predecessorResponse.Header.Get("X-Request-ID") == "" || predecessorProblem["request_id"] != predecessorResponse.Header.Get("X-Request-ID") ||
		predecessorProblem["code"] != "SERVICE_NOT_READY" || schemas["Problem"].Validate(predecessorProblem) != nil {
		t.Fatal("invalid predecessor response or retry metadata", decodeError)
	}
	publish["name"], publish["description"] = "described-alias", "  Use for comparisons.\r\nSecond line.  "
	admin.Headers["Idempotency-Key"] = describedKey
	problem(admin, adminPath, publish, 503, "SERVICE_NOT_READY")

	if _, err := c.s.DB.Exec(ctx, aliasDescriptionCandidateUp); err != nil {
		t.Fatal(err)
	}
	assertDescription := func(value map[string]any, description string, revision int) {
		t.Helper()
		if value["description"] != description || value["description_revision"] != float64(revision) {
			t.Fatal("unexpected description or description revision")
		}
	}
	assertDescription(portal.Call("GET", portalPath+"/"+legacyID, nil, 200, ""), "", 1)
	admin.Headers["Idempotency-Key"] = legacyKey
	publish["name"], publish["description"] = "legacy-alias", " \t "
	if value := admin.Write("POST", adminPath, publish, 200, ""); value["id"] != legacyID {
		t.Fatal("predecessor publication did not replay after migration")
	}
	admin.Headers["Idempotency-Key"] = describedKey
	publish["name"], publish["description"] = "described-alias", "  Use for comparisons.\r\nSecond line.  "
	described := admin.Write("POST", adminPath, publish, 200, "")
	id := described["id"].(string)
	assertDescription(described, "Use for comparisons.\nSecond line.", 1)
	publish["description"] = "Use for comparisons.\nSecond line."
	if replay := admin.Write("POST", adminPath, publish, 200, ""); replay["id"] != id {
		t.Fatal("normalized publication did not replay")
	}
	publish["description"] = "Changed publication"
	problem(admin, adminPath, publish, 409, "IDEMPOTENCY_CONFLICT")
	publish["description"] = "Use for comparisons.\nSecond line."
	editPath := adminPath + "/" + id + "/description"

	const memberEmail = "description-member@example.invalid"
	const editorEmail = "description-editor@example.invalid"
	register := func(email string) (string, string) {
		t.Helper()
		invitation, err := c.s.Identity.Invite(ctx, c.a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		password := fixture.Password()
		if err := c.s.Identity.Register(ctx, fixture.InvitationToken(t, c.s.Identity, invitation.ID), "Synthetic catalogue user", password, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		a := fixture.Login(t, c.s.Identity, email, password, "portal")
		return a.Me.User.ID, password
	}
	_, memberPassword := register(memberEmail)
	member := httpfixture.New(t, server, "portal", schemas)
	login(member, memberEmail, memberPassword)
	assertDescription(member.Call("GET", portalPath+"/"+id, nil, 200, ""), "Use for comparisons.\nSecond line.", 1)
	items := member.Call("GET", portalPath+"?limit=10", nil, 200, "")["items"].([]any)
	seen := false
	for _, item := range items {
		alias := item.(map[string]any)
		if alias["id"] == id {
			assertDescription(alias, "Use for comparisons.\nSecond line.", 1)
			seen = true
		}
	}
	if !seen {
		t.Fatal("member did not see the alias in its workspace list")
	}
	problem(member, portalPath+"/"+id+"/description", edit, 404, "RESOURCE_NOT_FOUND")

	other, err := c.s.Workspaces.Create(ctx, c.a, "Synthetic separate workspace", c.a.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	problem(admin, "/api/v1/admin/distribution/workspaces/"+other.ID+"/aliases/"+id+"/description", edit, 404, "MODEL_ALIAS_NOT_FOUND")
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic workspace','active')", foreignWorkspace, foreignTenant); err != nil {
		t.Fatal(err)
	}
	problem(admin, "/api/v1/admin/distribution/workspaces/"+foreignWorkspace+"/aliases/"+id+"/description", edit, 404, "RESOURCE_NOT_FOUND")
	csrf := admin.CSRF
	admin.CSRF = ""
	if got := admin.Call("POST", editPath, edit, 403, "Problem"); got["code"] != "CSRF_INVALID" {
		t.Fatal("edit skipped CSRF")
	}
	admin.CSRF = csrf
	delete(admin.Headers, "Idempotency-Key")
	problem(admin, editPath, edit, 400, "VALIDATION_FAILED")
	firstEdit := uuid.NewString()
	admin.Headers["Idempotency-Key"] = firstEdit
	admin.Headers["Content-Type"] = "text/plain"
	problem(admin, editPath, edit, 415, "UNSUPPORTED_MEDIA_TYPE")
	delete(admin.Headers, "Content-Type")
	oversized := edit
	oversized.Description = strings.Repeat("a", httpserver.MaxJSONBytes)
	problem(admin, editPath, oversized, 413, "PAYLOAD_TOO_LARGE")
	problem(admin, editPath, []byte(`{"description":"x","expected_description_revision":1,"reason":"Synthetic reason","revision":1}`), 400, "VALIDATION_FAILED")
	for _, value := range []string{strings.Repeat("\u4e2d", 501), "hidden\u0000", "hidden\u0085", "\r"} {
		bad := edit
		bad.Description = value
		problem(admin, editPath, bad, 400, "VALIDATION_FAILED")
	}
	problem(admin, editPath, []byte(`{"description":null,"expected_description_revision":1,"reason":"Synthetic reason"}`), 400, "VALIDATION_FAILED")
	problem(admin, editPath, []byte(`{"description":"x","description":"y","expected_description_revision":1,"reason":"Synthetic reason"}`), 400, "VALIDATION_FAILED")
	problem(admin, editPath, append(append([]byte(`{"description":"`), 0xff), []byte(`","expected_description_revision":1,"reason":"Synthetic reason"}`)...), 400, "VALIDATION_FAILED")

	edit.Description = strings.Repeat("\u4e2d", 500)
	updated := admin.Write("POST", editPath, edit, 200, "")
	assertDescription(updated, edit.Description, 2)
	if len(updated) != 4 || updated["applied_description_revision"] != float64(2) || updated["replayed"] != false {
		t.Fatal("unexpected first edit receipt")
	}
	different := edit
	different.Description = "Different text"
	problem(admin, editPath, different, 409, "IDEMPOTENCY_CONFLICT")

	editorID, editorPassword := register(editorEmail)
	if _, err := c.s.DB.Exec(ctx, "UPDATE users SET is_platform_admin=true WHERE id=$1", editorID); err != nil {
		t.Fatal(err)
	}
	requests := []*http.Request{
		descriptionEditRequest(t, c, server, "admin@example.invalid", c.password, editPath, "First concurrent description"),
		descriptionEditRequest(t, c, server, editorEmail, editorPassword, editPath, "Second concurrent description"),
	}
	type result struct {
		status int
		data   map[string]any
		err    error
	}
	results := make(chan result, len(requests))
	start := make(chan struct{})
	for _, request := range requests {
		go func() {
			<-start
			response, err := server.Client().Do(request)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer response.Body.Close()
			var body map[string]any
			err = json.NewDecoder(response.Body).Decode(&body)
			results <- result{response.StatusCode, body, err}
		}()
	}
	close(start)
	var winner string
	conflicts := 0
	for range requests {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		switch got.status {
		case 200:
			value := got.data["data"].(map[string]any)
			winner = value["description"].(string)
			assertDescription(value, winner, 3)
		case 409:
			if got.data["code"] != "VERSION_CONFLICT" || schemas["Problem"].Validate(got.data) != nil {
				t.Fatal("wrong concurrent refusal", got.data["code"])
			}
			conflicts++
		default:
			t.Fatal("unexpected concurrent response", got.status)
		}
	}
	if winner == "" || conflicts != 1 {
		t.Fatal("expected one edit and one stale revision")
	}
	replayed := admin.Write("POST", editPath, edit, 200, "")
	assertDescription(replayed, winner, 3)
	if replayed["applied_description_revision"] != float64(2) || replayed["replayed"] != true {
		t.Fatal("historical replay lost its applied revision")
	}

	failedEdit := distribution.AliasDescriptionEdit{Description: "Must roll back", ExpectedDescriptionRevision: 3, Reason: "Synthetic audit failure"}
	if _, err := c.s.DB.Exec(ctx, `CREATE FUNCTION refuse_description_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.action='model.alias_description_updated' AND NEW.reason='Synthetic audit failure'
	THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER refuse_description_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION refuse_description_audit();`); err != nil {
		t.Fatal(err)
	}
	rollbackKey := uuid.NewString()
	admin.Headers["Idempotency-Key"] = rollbackKey
	problem(admin, editPath, failedEdit, 503, "STORAGE_UNAVAILABLE")
	assertDescription(member.Call("GET", portalPath+"/"+id, nil, 200, ""), winner, 3)
	assertRoute := func(revision, versions int) {
		t.Helper()
		var actualRevision, actualVersions int
		var deployment string
		var deploymentRevision int64
		err := c.s.DB.QueryRow(ctx, `SELECT revision,deployment_id::text,deployment_revision,
			(SELECT count(*) FROM model_alias_target_versions WHERE alias_id=$1) FROM model_aliases WHERE id=$1`, id).
			Scan(&actualRevision, &deployment, &deploymentRevision, &actualVersions)
		if err != nil || actualRevision != revision || actualVersions != versions ||
			deployment != target.ID || deploymentRevision != target.Revision {
			t.Fatal("description changed routing authority or target history", err)
		}
	}
	assertRoute(1, 1)
	alias := distribution.Alias{ID: id, WorkspaceID: workspace}
	minted, err := c.s.MintKey(ctx, c.p, keyInput(alias), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	authorize := func() {
		t.Helper()
		models, err := c.s.AuthorizeModels(ctx, gateway.Gateway.ID, *gateway.Secret, *minted.Secret)
		if err != nil || len(models.Models) != 1 || models.Models[0].ID != id ||
			models.Models[0].DeploymentID != target.ID || models.Models[0].DeploymentRevision != uint64(target.Revision) {
			t.Fatal("existing key lost its target", err)
		}
	}
	authorize()
	pause := distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Synthetic pause before editing"}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	cutover := admin.Write("POST", adminPath+"/"+id+"/cutovers", pause, 200, "")
	assertDescription(cutover["alias"].(map[string]any), winner, 3)
	member.Call("GET", portalPath+"/"+id, nil, 404, "")
	clear := distribution.AliasDescriptionEdit{Description: " \t\n ", ExpectedDescriptionRevision: 3, Reason: "Clear synthetic description"}
	admin.Headers["Idempotency-Key"] = rollbackKey
	assertDescription(admin.Write("POST", editPath, clear, 200, ""), "", 4)
	assertRoute(2, 2)
	resume := distribution.CutoverRequest{Action: "resume", ExpectedRevision: 2, Reason: "Synthetic resume after editing"}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	admin.Write("POST", adminPath+"/"+id+"/cutovers", resume, 200, "")
	authorize()
	assertDescription(member.Call("GET", portalPath+"/"+id, nil, 200, ""), "", 4)
	var intents, audits int
	if err := c.s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM model_alias_description_intents WHERE alias_id=$1),
		(SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='model.alias_description_updated')`, id).
		Scan(&intents, &audits); err != nil || intents != 3 || audits != 3 {
		t.Fatal("edit intents and audit events disagree", intents, audits, err)
	}

	if _, err := c.s.DB.Exec(ctx, aliasDescriptionCandidateDown); err != nil {
		t.Fatal(err)
	}
	member.Call("GET", portalPath+"/"+id, nil, 200, "ModelAliasResponse")
}

// descriptionEditRequest prepares a real session and CSRF context before starting the HTTP race.
func descriptionEditRequest(t *testing.T, c scenario, server *httptest.Server, email, password, path, description string) *http.Request {
	t.Helper()
	ctx := t.Context()
	_, session, _, err := c.s.Identity.Login(ctx, email, password, "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := httpserver.NewCookiePolicy(c.s.Identity.Config.Server.EffectiveTransportMode())
	if err != nil {
		t.Fatal(err)
	}
	origin := httpfixture.AdminOrigin
	cookie := &http.Cookie{Name: policy.Name("admin", httpserver.SessionCookie), Value: session}
	csrfRequest, err := http.NewRequestWithContext(ctx, "GET", origin+"/api/v1/auth/csrf", nil)
	if err != nil {
		t.Fatal(err)
	}
	csrfRequest.AddCookie(cookie)
	response, err := server.Client().Do(csrfRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var csrf struct{ Data struct{ Token string } }
	if err := json.NewDecoder(response.Body).Decode(&csrf); err != nil || response.StatusCode != 200 || csrf.Data.Token == "" {
		t.Fatal("failed to initialize the concurrent Admin session", response.StatusCode, err)
	}
	body, err := json.Marshal(distribution.AliasDescriptionEdit{
		Description: description, ExpectedDescriptionRevision: 2, Reason: "Synthetic concurrent catalogue edit",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", origin+cellroute.Public(path, agentwire.LocalScope()), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("X-CSRF-Token", csrf.Data.Token)
	request.Header.Set("Idempotency-Key", uuid.NewString())
	request.AddCookie(cookie)
	for _, item := range response.Cookies() {
		request.AddCookie(item)
	}
	return request
}
