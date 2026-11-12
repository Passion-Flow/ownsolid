package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"

	api "github.com/Passion-Flow/ownsolid/sdk/go/distribution"
	"github.com/google/uuid"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

func main() {
	current := len(os.Args) == 2 && os.Args[1] == "candidate"
	_, present := reflect.TypeOf(api.ModelAlias{}).FieldByName("Capabilities")
	require(!present, "capabilities entered the mutation model")
	dataField, found := reflect.TypeOf(api.ModelAliasResponse{}).FieldByName("Data")
	require(found && dataField.Type.Name() == "ModelAlias", "mutation response changed its data type")
	workspace := uuid.MustParse("12345678-0000-4000-8000-000000000002")
	aliasID := uuid.MustParse("12345678-0000-4000-8000-000000000001")
	alias := map[string]any{
		"id": aliasID.String(), "workspace_id": workspace.String(),
		"gateway_id":    "12345678-0000-4000-8000-000000000003",
		"node_id":       "12345678-0000-4000-8000-000000000004",
		"deployment_id": "12345678-0000-4000-8000-000000000005",
		"name":          "verified-alias", "client_origin": "https://gateway.example.invalid",
		"deployment_revision": 1, "revision": 1, "held_requests": 0,
		"disabled_at": nil, "created_at": "2026-11-01T00:00:00Z",
		"available": true, "unavailable_code": nil,
		"description": "", "description_revision": 1,
	}
	calls := 0
	for _, hasEvidence := range []bool{false, true} {
		delete(alias, "capabilities")
		if hasEvidence {
			alias["capabilities"] = map[string]any{"text_generation_verified": true, "context_token_ceiling": 1024}
		}
		client, err := api.NewClientWithResponses("https://admin.example.invalid", api.WithHTTPClient(&http.Client{
			Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
				calls++
				require(request.Method == http.MethodGet, "read changed its method")
				admin := "/api/v1/sites/site-a/cells/cell-b/admin/distribution/workspaces/" + workspace.String() + "/aliases"
				portal := "/api/v1/sites/site-a/cells/cell-b/workspaces/" + workspace.String() + "/distribution/aliases"
				list := request.URL.Path == admin || request.URL.Path == portal
				require(list || request.URL.Path == admin+"/"+aliasID.String() || request.URL.Path == portal+"/"+aliasID.String(),
					"read changed its public scope or identity")
				var data any = alias
				if list {
					require(request.URL.Query().Get("cursor") == "original-cursor" && request.URL.Query().Get("limit") == "1" && len(request.URL.Query()) == 2,
						"read changed its cursor or page size")
					data = map[string]any{"items": []any{alias}, "next_cursor": "opaque-next"}
				} else {
					require(request.URL.RawQuery == "", "detail acquired a query")
				}
				body, err := json.Marshal(map[string]any{"data": data})
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
			}),
		}))
		if err != nil {
			panic(err)
		}
		check := func(response any, list bool) {
			raw, err := json.Marshal(response)
			if err != nil {
				panic(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				panic(err)
			}
			data := decoded["data"].(map[string]any)
			if list {
				require(data["next_cursor"] == "opaque-next", "cursor was lost")
				items := data["items"].([]any)
				require(len(items) == 1, "page changed its item count")
				data = items[0].(map[string]any)
			}
			require(data["id"] == aliasID.String() && data["workspace_id"] == workspace.String() &&
				data["deployment_revision"] == float64(1) && data["description"] == "", "alias fields changed")
			capabilities, exists := data["capabilities"]
			require(exists == (current && hasEvidence), "unknown evidence became a capability or known evidence disappeared")
			if exists {
				cap := capabilities.(map[string]any)
				require(cap["text_generation_verified"] == true && cap["context_token_ceiling"] == float64(1024),
					"verified capability changed")
			}
		}
		ctx := context.Background()
		adminDetail, err := client.AdminGetModelAliasWithResponse(ctx, "site-a", "cell-b", workspace, aliasID)
		if err != nil {
			panic(err)
		}
		require(adminDetail.JSON200 != nil, "Admin detail did not decode")
		check(adminDetail.JSON200, false)
		portalDetail, err := client.PortalGetModelAliasWithResponse(ctx, "site-a", "cell-b", workspace, aliasID)
		if err != nil {
			panic(err)
		}
		require(portalDetail.JSON200 != nil, "Portal detail did not decode")
		check(portalDetail.JSON200, false)
		limit, cursor := 1, "original-cursor"
		adminPage, err := client.AdminListModelAliasWithResponse(ctx, "site-a", "cell-b", workspace, &api.AdminListModelAliasParams{Limit: &limit, Cursor: &cursor})
		if err != nil {
			panic(err)
		}
		require(adminPage.JSON200 != nil, "Admin list did not decode")
		check(adminPage.JSON200, true)
		portalPage, err := client.PortalListModelAliasWithResponse(ctx, "site-a", "cell-b", workspace, &api.PortalListModelAliasParams{Limit: &limit, Cursor: &cursor})
		if err != nil {
			panic(err)
		}
		require(portalPage.JSON200 != nil, "Portal list did not decode")
		check(portalPage.JSON200, true)
	}
	require(calls == 8, "not all read operations ran")
	fmt.Printf("Go %s alias read clients preserved presence/omission and cursor\n", strings.TrimSpace(os.Args[1]))
}
