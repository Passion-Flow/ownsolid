package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestAdminAuditHTTPAssistantAttribution(t *testing.T) {
	s, server, portal, admin, password := setup(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	const path = "/api/v1/admin/audit-events"
	const header = "X-Ownsolid-Audit-Actor"
	const adminEmail = "admin@example.invalid"
	const subjectEmail = "audit-subject@example.invalid"
	optedIn := map[string]string{header: "true"}

	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": adminEmail, "password": password}, 200, "LoginResponse")
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": adminEmail, "password": password}, 200, "LoginResponse")
	anonymous := newClient(t, server, "admin", admin.schemas)
	anonymous.request("GET", path, nil, 401, "Problem", optedIn)
	portal.request("GET", path, nil, 404, "Problem", optedIn)

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var tenant, adminID string
	if err := s.DB.QueryRow(ctx, "SELECT tenant_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&tenant, &adminID); err != nil {
		t.Fatal(err)
	}
	workspace, foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	subject := uuid.NewString()
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')", foreignTenant)
	exec(`INSERT INTO workspaces(id,tenant_id,name,status)
		VALUES($1,$2,'Non-default audit workspace','active'),($3,$4,'Foreign','active')`,
		workspace, tenant, foreignWorkspace, foreignTenant)
	exec(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status)
		VALUES($1,$2,$2,'Audit Subject','en','active')`, subject, subjectEmail)
	exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role)
		VALUES($1,$2,$3,'member')`, subject, tenant, workspace)
	store, err := s.Repository()
	if err != nil {
		t.Fatal(err)
	}
	appendAssistant := func(request string) error {
		return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return audit.WriteProductAssistantWithin(ctx, tx, audit.ProductAssistantEvent{
				SubjectUserID: subject, TenantID: tenant, WorkspaceID: workspace, Audience: "portal",
				RequestID: request, ToolID: "list_applications", Outcome: "accepted",
			})
		})
	}
	first, second := uuid.NewString(), uuid.NewString()
	for _, request := range []string{first, second} {
		if err := appendAssistant(request); err != nil {
			t.Fatal(err)
		}
	}
	exec("DELETE FROM workspace_memberships WHERE user_id=$1 AND tenant_id=$2 AND workspace_id=$3", subject, tenant, workspace)
	if err := appendAssistant(uuid.NewString()); !errors.Is(err, audit.ErrProductAssistantScopeDenied) {
		t.Fatalf("removed membership still authorizes a new event: %v", err)
	}

	const assistantShape = `{"actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant"}`
	seedEvent := func(tenantID, workspaceID any, actor any, metadata string) string {
		t.Helper()
		request := uuid.NewString()
		exec(`INSERT INTO audit_events (id,occurred_at,tenant_id,workspace_id,actor_user_id,subject_user_id,
			audience,action,outcome,request_id,metadata)
			VALUES($1,clock_timestamp(),$2,$3,$4,$5,'portal','product_assistant.list_applications','success',$6,$7::jsonb)`,
			uuid.NewString(), tenantID, workspaceID, actor, subject, request, metadata)
		return request
	}
	human := seedEvent(tenant, workspace, adminID, assistantShape)
	ordinary := seedEvent(tenant, workspace, nil, `{"private_detail":"synthetic audit metadata","actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant"}`)
	foreign := seedEvent(foreignTenant, foreignWorkspace, nil, assistantShape)
	mismatched := seedEvent(tenant, foreignWorkspace, nil, assistantShape)
	unscoped := seedEvent(tenant, nil, nil, assistantShape)

	read := func(query url.Values, prefer bool) map[string]any {
		t.Helper()
		schema := "AuditListResponse"
		var headers map[string]string
		if prefer {
			// DEV owns the public schema. Assert the proposed extension and the
			// entire legacy schema independently until that schema is merged.
			headers, schema = optedIn, ""
		}
		return admin.request("GET", path+"?"+query.Encode(), nil, 200, schema, headers)
	}
	single := func(page map[string]any) map[string]any {
		t.Helper()
		items := page["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("expected one audit event, got %d", len(items))
		}
		return items[0].(map[string]any)
	}
	withoutAssistant := func(item map[string]any) map[string]any {
		copy := maps.Clone(item)
		delete(copy, "actor_kind")
		delete(copy, "actor_id")
		return copy
	}
	for _, request := range []string{first, second, human, ordinary, foreign, mismatched, unscoped} {
		query := url.Values{"request_id": {request}}
		legacy := single(read(query, false))
		enriched := single(read(query, true))
		if !reflect.DeepEqual(legacy, withoutAssistant(enriched)) || legacy["request_id"] != request ||
			legacy["subject_user_id"] != subject || legacy["subject_email"] != subjectEmail {
			t.Fatal("the preference changed the existing audit fields or disclosed metadata")
		}
		if request == first || request == second {
			if enriched["actor_kind"] != "product_assistant" || enriched["actor_id"] != "ownsolid.product-assistant" ||
				enriched["actor_user_id"] != nil || enriched["actor_email"] != nil || len(enriched) != len(legacy)+2 {
				t.Fatal("verified assistant was not separated from the user subject")
			}
		} else if !reflect.DeepEqual(legacy, enriched) {
			t.Fatal("an ineligible audit event acquired assistant fields")
		}
		if request == human && (legacy["actor_user_id"] != adminID || legacy["actor_email"] != adminEmail) {
			t.Fatal("human attribution was lost")
		}
	}

	query := url.Values{"limit": {"1"}, "outcome": {"accepted"}}
	legacyPage := read(query, false)
	cursor, ok := legacyPage["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("legacy cursor missing")
	}
	query.Set("cursor", cursor)
	enrichedNext, legacyNext := read(query, true), read(query, false)
	next := single(enrichedNext)
	if next["request_id"] == single(legacyPage)["request_id"] || next["actor_kind"] != "product_assistant" ||
		!reflect.DeepEqual(withoutAssistant(next), single(legacyNext)) || enrichedNext["next_cursor"] != nil {
		t.Fatal("a legacy cursor did not resume correctly with assistant attribution")
	}
	query.Del("cursor")
	enrichedPage := read(query, true)
	cursor, ok = enrichedPage["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("enriched cursor missing")
	}
	query.Set("cursor", cursor)
	if afterOptIn := read(query, false); !reflect.DeepEqual(legacyNext, afterOptIn) {
		t.Fatal("an enriched cursor did not resume in the legacy representation")
	}
	query.Set("outcome", "success")
	if problem := admin.request("GET", path+"?"+query.Encode(), nil, 400, "Problem", optedIn); problem["code"] != "INVALID_CURSOR" {
		t.Fatal("the preference changed cursor filter binding")
	}
	for _, selector := range []string{"tenant_id=" + foreignTenant, "workspace_id=" + foreignWorkspace, "actor_kind=product_assistant"} {
		if problem := admin.request("GET", path+"?"+selector, nil, 400, "Problem", optedIn); problem["code"] != "VALIDATION_FAILED" {
			t.Fatal("the preference authorized a new query parameter")
		}
	}

	urlPath := cellroute.Public(path+"?request_id="+first, wire.LocalScope())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "admin.example.invalid"
	for _, cookie := range admin.cookies {
		request.AddCookie(cookie)
	}
	request.Header.Add(header, "true")
	request.Header.Add(header, "true")
	response, err := admin.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var legacyResponse any
	if response.StatusCode != http.StatusOK || response.Header.Get("Vary") != header ||
		response.Header.Get("Cache-Control") != "no-store" ||
		json.NewDecoder(response.Body).Decode(&legacyResponse) != nil ||
		admin.schemas["AuditListResponse"].Validate(legacyResponse) != nil {
		t.Fatal("repeated preference headers did not select the legacy response")
	}
	// The shared client can validate the full old schema for a combined value.
	admin.request("GET", path+"?request_id="+first, nil, 200, "AuditListResponse", map[string]string{header: "true, true"})

	stale := newClient(t, server, "admin", admin.schemas)
	stale.cookies = maps.Clone(admin.cookies)
	admin.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	if problem := stale.request("GET", path, nil, 401, "Problem", optedIn); problem["code"] != "AUTH_SESSION_EXPIRED" {
		t.Fatal("a revoked Admin session could read assistant attribution")
	}
}
