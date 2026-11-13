package historywire

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	models "github.com/Passion-Flow/ownsolid/sdk/go/models"
	"github.com/google/uuid"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("..", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func response(r *http.Request, status int, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: r}
}
func encoded(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out["data"].(map[string]any)
}
func TestHistoryQueryAndExactRows(t *testing.T) {
	family := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	workspace := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	calls := 0
	client, err := models.NewClientWithResponses("https://admin.example.invalid", models.WithHTTPClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/default/cells/default/admin/models/33333333-3333-4333-8333-333333333333/history" {
			t.Fatal("query changed method or exact anchor")
		}
		q := r.URL.Query()
		if q.Get("workspace_id") != workspace.String() {
			t.Fatal("query lost workspace")
		}
		if calls == 1 && len(q) != 1 {
			t.Fatal("nil options acquired policy defaults")
		}
		if calls == 2 && (q.Get("limit") != "7" || q.Get("cursor") != "opaque+/=%\u754c" || q.Get("state") != "retired" || len(q) != 4) {
			t.Fatal("explicit filter/cursor changed on wire")
		}
		return response(r, 200, append(append([]byte(`{"data":`), fixture(t, "page.json")...), '}')), nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	params := &models.ListAdminModelVersionHistoryParams{WorkspaceId: workspace}
	got, err := client.ListAdminModelVersionHistoryWithResponse(context.Background(), "default", "default", uuid.MustParse("33333333-3333-4333-8333-333333333333"), params)
	if err != nil || got.StatusCode() != 200 || got.GetJSON200() == nil {
		t.Fatalf("history decode: %v", err)
	}
	data := encoded(t, got.GetJSON200())
	items := data["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != "33333333-3333-4333-8333-333333333333" || items[1].(map[string]any)["id"] != "44444444-4444-4444-8444-444444444444" || items[1].(map[string]any)["state"] != "retired" {
		t.Fatal("history changed exact version identity or state")
	}
	if data["anchor_model_id"] != "33333333-3333-4333-8333-333333333333" || data["family_id"] != family.String() {
		t.Fatal("history changed the exact anchor or family")
	}
	if value, ok := data["next_cursor"]; !ok || value != nil {
		t.Fatal("history lost explicit null continuation")
	}
	limit, cursor := 7, "opaque+/=%\u754c"
	state := models.ListAdminModelVersionHistoryParamsState("retired")
	params.Limit, params.Cursor, params.State = &limit, &cursor, &state
	if _, err := client.ListAdminModelVersionHistoryWithResponse(context.Background(), "default", "default", uuid.MustParse("33333333-3333-4333-8333-333333333333"), params); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("history made an extra request")
	}
}
func TestReadyCompatibilityAndRetiredDetail(t *testing.T) {
	family := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	workspace := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	ready := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	retired := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	client, err := models.NewClientWithResponses("https://admin.example.invalid", models.WithHTTPClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatal("metadata read emitted a write")
		}
		file := "ready.json"
		if r.URL.Path == "/api/v1/sites/default/cells/default/admin/models/families/"+family.String()+"/versions/"+retired.String() {
			if r.URL.Query().Get("workspace_id") != workspace.String() {
				t.Fatal("retained detail lost scope")
			}
			file = "retired.json"
		} else if r.URL.Path != "/api/v1/sites/default/cells/default/admin/models/"+ready.String() {
			t.Fatal("detail changed exact version path")
		}
		return response(r, 200, append(append([]byte(`{"data":`), fixture(t, file)...), '}')), nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	old, err := client.GetAdminModelWithResponse(context.Background(), "default", "default", ready)
	if err != nil || old.GetJSON200() == nil {
		t.Fatalf("legacy ready detail: %v", err)
	}
	data := encoded(t, old.GetJSON200())
	if _, exists := data["family_id"]; exists || data["state"] != "ready" {
		t.Fatal("legacy response invented a family or changed ready state")
	}
	history, err := client.GetAdminHistoricalModelVersionWithResponse(context.Background(), "default", "default", family, retired, &models.GetAdminHistoricalModelVersionParams{WorkspaceId: workspace})
	if err != nil || history.GetJSON200() == nil {
		t.Fatalf("retired metadata: %v", err)
	}
	data = encoded(t, history.GetJSON200())
	if data["id"] != retired.String() || data["family_id"] != family.String() || data["state"] != "retired" {
		t.Fatal("retired projection changed immutable bindings")
	}
}
func TestExplicitInvalidValuesAndProblemCodec(t *testing.T) {
	workspace := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	calls := 0
	client, err := models.NewClientWithResponses("https://admin.example.invalid", models.WithHTTPClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet {
			t.Fatal("invalid metadata query emitted a mutation")
		}
		q := r.URL.Query()
		if calls == 1 && q.Get("limit") != "0" {
			t.Fatal("explicit zero was omitted or clamped")
		}
		if calls == 2 {
			if value, ok := q["cursor"]; !ok || len(value) != 1 || value[0] != "" {
				t.Fatal("explicit empty cursor was omitted")
			}
		}
		return response(r, 400, []byte(`{"type":"about:blank","title":"VALIDATION_FAILED","status":400,"code":"VALIDATION_FAILED","request_id":"33333333-3333-4333-8333-333333333333"}`)), nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	zero, empty := 0, ""
	for _, params := range []*models.ListAdminModelVersionHistoryParams{{WorkspaceId: workspace, Limit: &zero}, {WorkspaceId: workspace, Cursor: &empty}} {
		got, err := client.ListAdminModelVersionHistoryWithResponse(context.Background(), "default", "default", uuid.MustParse("33333333-3333-4333-8333-333333333333"), params)
		if err != nil || got.StatusCode() != 400 || got.GetApplicationproblemJSONDefault() == nil || got.GetJSON200() != nil {
			t.Fatalf("problem codec: %v", err)
		}
	}
	if calls != 2 {
		t.Fatal("problem response was retried")
	}
}
