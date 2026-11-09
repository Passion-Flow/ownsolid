package distribution_test

import (
	"encoding/json"
	"net/url"
	"sort"
	"testing"
	"time"

	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func TestGatewayUsageHistoryAuditExport(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	other := c.access(t)
	minted, err := c.s.MintKey(ctx, c.p, keyInput(other.alias), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	a := access{alias: other.alias, gateway: other.gateway, key: minted, owner: c.p}
	admit := func(scope access) string {
		t.Helper()
		input := scope.input()
		if _, err := c.s.Admit(ctx, scope.gateway.Gateway.ID, *scope.gateway.Secret, input, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		return input.RequestID
	}
	report := func(outcome gatewaywire.Outcome) gatewaywire.Acknowledgment {
		t.Helper()
		receipt, err := c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, outcome, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	first := success(admit(a))
	first.ElapsedMilliseconds = 42
	receipt := report(first)
	if replay := report(first); replay != receipt {
		t.Fatal("receipt replay changed the acknowledgment")
	}
	conflict := first
	conflict.ElapsedMilliseconds++
	_, err = c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, conflict, uuid.NewString())
	check(t, err, "GATEWAY_OUTCOME_CONFLICT")
	second := success(admit(a))
	second.ElapsedMilliseconds = 0
	second.Usage = &gatewaywire.Usage{PromptTokens: 11, CompletionTokens: 0, TotalTokens: 11}
	report(second)
	cancelCode := "client_cancelled"
	cancelled := gatewaywire.Outcome{
		RequestID: admit(a), Status: cancelCode, ErrorCode: &cancelCode,
		ElapsedMilliseconds: 23, OutputStarted: true,
	}
	report(cancelled)
	unknown := admit(a)
	report(success(admit(other)))

	var auditEvents int
	if err := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='gateway.request_reported'", first.RequestID).Scan(&auditEvents); err != nil || auditEvents != 1 {
		t.Fatal("receipt replay created another audit event", auditEvents, err)
	}
	// UUID ordering must resolve ties even when several admissions share a timestamp.
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.s.DB.Exec(ctx, "UPDATE gateway_requests SET admitted_at=$1", stamp); err != nil {
		t.Fatal(err)
	}
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	portal := httpfixture.New(t, server, "portal", schemas)
	admin := httpfixture.New(t, server, "admin", schemas)
	for _, client := range []*httpfixture.Client{portal, admin} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	workspace := a.alias.WorkspaceID
	path := "/api/v1/workspaces/" + workspace + "/distribution/requests"
	values := url.Values{"api_key_id": {a.key.Key.ID}, "limit": {"1"}}
	expected := []string{first.RequestID, second.RequestID, cancelled.RequestID, unknown}
	sort.Sort(sort.Reverse(sort.StringSlice(expected)))
	outcomes := map[string]gatewaywire.Outcome{first.RequestID: first, second.RequestID: second, cancelled.RequestID: cancelled}
	var tokens uint32
	var cursors []string
	for i, id := range expected {
		value := portal.Call("GET", path+"?"+values.Encode(), nil, 200, "GatewayRequestPageResponse")
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var page distribution.Page[distribution.RequestRecord]
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].APIKeyID != a.key.Key.ID ||
			!page.Items[0].AdmittedAt.Equal(stamp) || (page.NextCursor == nil) != (i == len(expected)-1) {
			t.Fatal("unexpected bounded history page", i)
		}
		record := page.Items[0]
		if want, known := outcomes[id]; known {
			if record.State != "reported" || record.CapacityHeld || record.Outcome == nil {
				t.Fatal("reported history row missing its receipt")
			}
			gotDigest, gotErr := record.Outcome.Digest()
			wantDigest, wantErr := want.Digest()
			if gotErr != nil || wantErr != nil || gotDigest != wantDigest {
				t.Fatal("history changed usage, latency or terminal status")
			}
			if record.Outcome.Usage != nil {
				tokens += record.Outcome.Usage.TotalTokens
			}
		} else if record.State != "admitted" || !record.CapacityHeld || record.Outcome != nil {
			t.Fatal("unknown usage was manufactured")
		}
		if page.NextCursor != nil {
			cursors = append(cursors, *page.NextCursor)
			values.Set("cursor", *page.NextCursor)
		}
	}
	if tokens != 20 {
		t.Fatal("scoped extraction token total", tokens)
	}
	count := func(client *httpfixture.Client, path string) int {
		return len(client.Call("GET", path, nil, 200, "GatewayRequestPageResponse")["items"].([]any))
	}
	adminPath := "/api/v1/admin/distribution/workspaces/" + workspace + "/requests"
	if count(portal, path) != 4 || count(admin, adminPath) != 5 ||
		count(portal, path+"?api_key_id="+other.key.Key.ID) != 0 {
		t.Fatal("history request totals crossed an actor or key boundary")
	}
	changed := url.Values{"api_key_id": {other.key.Key.ID}, "cursor": {cursors[0]}}
	portal.Call("GET", path+"?"+changed.Encode(), nil, 400, "Problem")
	admin.Call("GET", adminPath+"?cursor="+url.QueryEscape(cursors[0]), nil, 400, "Problem")

	foreign := setup(t)
	if foreign.a.Me.Workspace.TenantID == c.a.Me.Workspace.TenantID {
		t.Fatal("expected a separate tenant")
	}
	foreignServer := httpfixture.NewTLSServer(t, identityhttp.New(foreign.s.Identity))
	foreignAdmin := httpfixture.New(t, foreignServer, "admin", schemas)
	foreignAdmin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": foreign.password}, 200, "")
	foreignAdmin.Call("GET", adminPath, nil, 404, "Problem")
	foreignAdmin.Call("GET", adminPath+"/"+first.RequestID, nil, 404, "Problem")
	assertGatewayUsageCSV(t, c, foreign, server, foreignServer, a, other, stamp, expected, cursors, map[string][]string{
		first.RequestID:     {"reported", "succeeded", "1", "42", "7", "2", "9"},
		second.RequestID:    {"reported", "succeeded", "1", "0", "11", "0", "11"},
		cancelled.RequestID: {"reported", "client_cancelled", "1", "23", "", "", ""},
		unknown:             {"admitted", "", "1", "", "", "", ""},
	})
}
