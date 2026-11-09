//go:build repository_integration

package applications_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestPortalConversationSearch(t *testing.T) {
	g := portalSetup(t)
	if g.f.PostgreSQL == nil {
		t.Skip("Conversation history search uses PostgreSQL")
	}
	const term = "Ledger_%! \u4e16\u754c"
	named := g.draft(t, "Report "+term)
	namedVersion := g.publication(t, named, 1)
	nameMatch := g.conversation(t, named.ID, namedVersion, g.f.User)
	tombstone := g.conversation(t, named.ID, namedVersion, g.f.User)
	g.exec(t, `UPDATE workflow_conversations SET deleted_at=clock_timestamp() WHERE id=$1`, "", tombstone)
	other := g.other(t)
	otherOwner := g.conversation(t, named.ID, namedVersion, other.Me.User.ID)
	plain := g.draft(t, "Ordinary report")
	plainVersion := g.publication(t, plain, 1)
	questionMatch := g.conversation(t, plain.ID, plainVersion, g.f.User)
	answerMatch := g.conversation(t, plain.ID, plainVersion, g.f.User)
	pending := g.conversation(t, plain.ID, plainVersion, g.f.User)
	g.seedSearchTurn(t, questionMatch, 1, "failed", "Question "+strings.ToUpper(term), nil)
	g.seedSearchTurn(t, questionMatch, 2, "failed", term+" again", nil)
	g.seedSearchTurn(t, answerMatch, 1, "succeeded", "Plain question", "Answer "+term)
	g.seedSearchTurn(t, pending, 1, "pending", term, nil)

	// Exercise the UUID tie break independently of PostgreSQL clock resolution.
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	want := []string{nameMatch, questionMatch, answerMatch}
	for _, id := range want {
		g.exec(t, `UPDATE workflow_conversations SET updated_at=$2 WHERE id=$1`, "", id, stamp)
	}
	g.exec(t, `UPDATE workflow_conversations SET updated_at=$2 WHERE id=$1`, "", pending, stamp.Add(time.Second))
	slices.Sort(want)
	slices.Reverse(want)

	secondWorkspace, inaccessible := uuid.NewString(), uuid.NewString()
	g.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$3,'Other search workspace','active'),($2,$3,'Inaccessible search workspace','active')`, "", secondWorkspace, inaccessible, g.f.Tenant)
	g.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, "", g.f.User, g.f.Tenant, secondWorkspace, uuid.NewString())
	secondary := g
	secondary.f.Workspace = secondWorkspace
	secondaryWorkflow := secondary.draft(t, "Other report "+term)
	otherScope := secondary.conversation(t, secondaryWorkflow.ID, secondary.publication(t, secondaryWorkflow, 1), g.f.User)

	g.owner.Config.Server.PortalOrigin = httpfixture.PortalOrigin
	g.owner.Config.Server.AdminOrigin = httpfixture.AdminOrigin
	server := httpfixture.NewTLSServer(t, identityhttp.New(g.owner))
	cookies, err := httpserver.NewCookiePolicy(g.owner.Config.Server.EffectiveTransportMode())
	if err != nil {
		t.Fatal(err)
	}
	const audience = "portal"
	firstSession := g.issue(t, g.f.User, audience)
	replacement := g.issue(t, g.f.User, audience)
	otherSession := g.issue(t, other.Me.User.ID, audience)
	path := cellroute.Public("/api/v1/application-conversations", wire.LocalScope())
	read := func(t *testing.T, session, workspace string, values url.Values, status int, code string) applications.Page[applications.Conversation] {
		t.Helper()
		query := url.Values{"workspace_id": {workspace}}
		for key, value := range values {
			query[key] = value
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpfixture.PortalOrigin+path+"?"+query.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: cookies.Name(audience, httpserver.SessionCookie), Value: session})
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body struct {
			Data json.RawMessage `json:"data"`
			Code string          `json:"code"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status || body.Code != code || (body.Data != nil) != (status == http.StatusOK) {
			t.Fatalf("got HTTP %d %s, data %t; want HTTP %d %s", response.StatusCode, body.Code, body.Data != nil, status, code)
		}
		if body.Data == nil {
			return applications.Page[applications.Conversation]{}
		}
		var page applications.Page[applications.Conversation]
		decoder := json.NewDecoder(bytes.NewReader(body.Data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&page); err != nil {
			t.Fatalf("expected only conversation metadata: %v", err)
		}
		return page
	}
	assertIDs := func(page applications.Page[applications.Conversation], expected ...string) {
		t.Helper()
		ids := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if page.Items == nil || !slices.Equal(ids, expected) {
			t.Fatalf("conversation IDs %v, want %v", ids, expected)
		}
	}
	workspace := g.f.Workspace
	legacy := read(t, firstSession, workspace, nil, http.StatusOK, "")
	assertIDs(legacy, pending)
	if legacy.NextCursor == nil {
		t.Fatal("default page lost its legacy continuation")
	}
	legacyOptions := url.Values{"cursor": {*legacy.NextCursor}}
	legacyScope := "application-conversations:portal:" + g.f.User + ":" + workspace
	if _, cursor, _, err := pagination.Parse(legacyOptions, legacyScope); err != nil || cursor.ID != pending {
		t.Fatal("unfiltered cursor format changed")
	}
	assertIDs(read(t, replacement, workspace, legacyOptions, http.StatusOK, ""), want[0])

	query := url.Values{"q": {strings.ToLower(term)}}
	var tokens []string
	for i, id := range want {
		page := read(t, firstSession, workspace, query, http.StatusOK, "")
		assertIDs(page, id)
		if i == len(want)-1 {
			if page.NextCursor != nil {
				t.Fatal("last search page issued a continuation")
			}
			break
		}
		if page.NextCursor == nil || len(*page.NextCursor) > pagination.MaximumCursorBytes {
			t.Fatal("search page lost its bounded continuation")
		}
		tokens = append(tokens, *page.NextCursor)
		query.Set("cursor", *page.NextCursor)
	}
	assertIDs(read(t, firstSession, workspace, url.Values{"q": {"%"}, "limit": {"10"}}, http.StatusOK, ""), want...)
	assertIDs(read(t, firstSession, workspace, url.Values{"q": {"%' OR 1=1 --"}}, http.StatusOK, ""))
	assertIDs(read(t, firstSession, workspace, url.Values{"q": {strings.Repeat("\u4e16", 120)}}, http.StatusOK, ""))
	assertIDs(read(t, otherSession, workspace, url.Values{"q": {term}}, http.StatusOK, ""), otherOwner)
	assertIDs(read(t, firstSession, secondWorkspace, url.Values{"q": {term}}, http.StatusOK, ""), otherScope)
	read(t, firstSession, inaccessible, url.Values{"q": {term}}, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	for _, bad := range [][]string{{""}, {"   "}, {"needle", "other"}, {"line\nbreak"}, {"\xff"}, {strings.Repeat("\u4e16", 121)}} {
		read(t, firstSession, workspace, url.Values{"q": bad}, http.StatusBadRequest, "VALIDATION_FAILED")
	}

	query.Set("cursor", tokens[0])
	read(t, replacement, workspace, query, http.StatusBadRequest, "INVALID_CURSOR")
	read(t, otherSession, workspace, query, http.StatusBadRequest, "INVALID_CURSOR")
	read(t, firstSession, secondWorkspace, query, http.StatusBadRequest, "INVALID_CURSOR")
	read(t, firstSession, workspace, url.Values{"cursor": {tokens[0]}}, http.StatusBadRequest, "INVALID_CURSOR")
	read(t, firstSession, workspace, url.Values{"q": {"changed"}, "cursor": {tokens[0]}}, http.StatusBadRequest, "INVALID_CURSOR")
	read(t, firstSession, workspace, url.Values{"q": {query.Get("q")}, "cursor": {*legacy.NextCursor}}, http.StatusBadRequest, "INVALID_CURSOR")
	position, _, _ := strings.Cut(tokens[1], ".")
	_, tag, _ := strings.Cut(tokens[0], ".")
	query.Set("cursor", position+"."+tag)
	read(t, firstSession, workspace, query, http.StatusBadRequest, "INVALID_CURSOR")

	actor, err := g.owner.Authenticate(g.ctx, firstSession, audience)
	if err != nil {
		t.Fatal(err)
	}
	g.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, "", actor.SessionID)
	query.Set("cursor", tokens[0])
	read(t, firstSession, workspace, query, http.StatusUnauthorized, "AUTH_SESSION_EXPIRED")
}

func (g portalStory) seedSearchTurn(t *testing.T, conversation string, turn int, state, question string, answer any) {
	t.Helper()
	tag, err := g.f.PostgreSQL.Exec(g.ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,turn_number,version_id,
		execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,answer,
		evidence,alias_snapshot,usage,deadline_at,finished_at)
	SELECT $2,c.tenant_id,c.workspace_id,c.workflow_id,c.owner_id,c.id,$3,c.version_id,$4,$5,'portal',v.graph,v.graph_sha256,$6::text,$7,$8,
		CASE WHEN $6='succeeded' THEN '{}'::jsonb END,CASE WHEN $6='succeeded' THEN '{}'::jsonb END,
		CASE WHEN $6='succeeded' THEN '{}'::jsonb END,clock_timestamp()+interval '1 hour',
		CASE WHEN $6<>'pending' THEN clock_timestamp() END
	FROM workflow_conversations c JOIN workflow_versions v ON v.id=c.version_id WHERE c.id=$1`,
		conversation, uuid.NewString(), turn, uuid.NewString(), uuid.NewString(), state, question, answer)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("seed one conversation turn: %v", err)
	}
}
