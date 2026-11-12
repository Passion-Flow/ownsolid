package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

func workspaceReadServer(t *testing.T, handler http.HandlerFunc) *atomic.Int64 {
	t.Helper()
	return authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("workspace read changed its method")
		}
		handler(w, r)
	})
}

func TestWorkspaceReadsPreserveGlobalPathsSearchAndOutput(t *testing.T) {
	row := map[string]any{
		"id": workspaceID, "tenant_id": nodeID, "name": "Research %_ \u4e00", "status": "disabled",
		"is_default": false, "role": nil, "version": 2,
	}
	const query = " %_ \u4e00 "
	requests := workspaceReadServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case workspaceReadPath:
			if r.URL.Query().Get("q") != query || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("cursor") != "first" ||
				len(r.URL.Query()) != 3 {
				t.Error("workspace list changed its literal filter or page arguments")
			}
			respond(w, map[string]any{"items": []any{row}, "next_cursor": "next"})
		case workspaceReadPath + "/" + workspaceID:
			if len(r.URL.Query()) != 0 {
				t.Error("global workspace detail acquired a configured scope")
			}
			respond(w, row)
		default:
			t.Errorf("unexpected workspace route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	for _, format := range []string{"json", "yaml", "table"} {
		out, _, err := run(t, "workspace", "list", "--query", query, "--limit", "1", "--cursor", "first", "-o", format)
		if err != nil {
			t.Fatal(err)
		}
		switch format {
		case "json":
			var page boundedPage
			if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Items) != 1 ||
				page.NextCursor == nil || *page.NextCursor != "next" || !strings.Contains(out, "\"is_default\": false") {
				t.Fatalf("workspace JSON lost the page or false default flag: %q (%v)", out, err)
			}
		case "yaml":
			var page map[string]any
			if yaml.Unmarshal([]byte(out), &page) != nil || page["next_cursor"] != "next" {
				t.Fatalf("workspace YAML lost its continuation: %q", out)
			}
		case "table":
			if !strings.Contains(out, workspaceID) || !strings.Contains(out, "disabled") ||
				!strings.Contains(out, "false") || !strings.Contains(out, "Next cursor: \"next\"") {
				t.Fatalf("workspace table lost identity, state or continuation: %q", out)
			}
		}
		detail, _, err := run(t, "workspace", "get", workspaceID, "-o", format)
		if err != nil || !strings.Contains(detail, workspaceID) {
			t.Fatalf("workspace detail: %q (%v)", detail, err)
		}
	}
	if requests.Load() != 6 {
		t.Fatal("workspace reads made additional requests or changed local context")
	}
}

func TestWorkspaceArgumentsFailBeforeIO(t *testing.T) {
	requests := workspaceReadServer(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached the server") })
	for _, args := range [][]string{
		{"list", "--limit", "0"}, {"list", "--limit", "101"}, {"list", "--cursor", ""},
		{"list", "--cursor", strings.Repeat("x", 513)}, {"list", "--cursor", "\x1b"},
		{"list", "--query", ""}, {"list", "--query", "\n"}, {"list", "--query", "\xff"},
		{"list", "--query", strings.Repeat("\u4e00", 101)},
		{"get", "00000000-0000-0000-0000-000000000000"}, {"get", "ABCDEF01-0000-4000-8000-000000000001"},
		{"get", "research"}, {"get"}, {"list", "--workspace", workspaceID},
	} {
		out, _, err := run(t, append([]string{"workspace"}, args...)...)
		if out != "" || err == nil {
			t.Errorf("workspace invalid arguments %v produced %q (%v)", args, out, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid arguments caused I/O")
	}
}

func TestWorkspaceReadsRejectMalformedPagesAndWrongIdentity(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value any
		args  []string
	}{
		{"missing items", map[string]any{"next_cursor": nil}, []string{"list"}},
		{"empty continuation", map[string]any{"items": []any{}, "next_cursor": "next"}, []string{"list"}},
		{"repeated continuation", map[string]any{"items": []any{map[string]string{"id": workspaceID}}, "next_cursor": "same"}, []string{"list", "--cursor", "same"}},
		{"control continuation", map[string]any{"items": []any{map[string]string{"id": workspaceID}}, "next_cursor": "\x1b"}, []string{"list"}},
		{"invalid second row", map[string]any{"items": []any{map[string]string{"id": workspaceID}, map[string]string{"id": "invalid"}}, "next_cursor": nil}, []string{"list"}},
		{"zero row", map[string]any{"items": []any{map[string]string{"id": "00000000-0000-0000-0000-000000000000"}}, "next_cursor": nil}, []string{"list"}},
		{"wrong detail", map[string]string{"id": nodeID}, []string{"get", workspaceID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			workspaceReadServer(t, func(w http.ResponseWriter, _ *http.Request) { respond(w, tt.value) })
			out, _, err := run(t, append([]string{"workspace"}, tt.args...)...)
			if out != "" || err == nil {
				t.Fatalf("malformed workspace response produced %q (%v)", out, err)
			}
		})
	}
}

func TestWorkspaceDefaultPageEmptyAndRetry(t *testing.T) {
	var attempts atomic.Int64
	requests := workspaceReadServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != workspaceReadPath || len(r.URL.Query()) != 0 {
			t.Error("default page changed its global path or added arguments")
		}
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"type\":\"about:blank\",\"title\":\"Storage unavailable\",\"status\":503,\"code\":\"STORAGE_UNAVAILABLE\",\"request_id\":\"12345678-0000-4000-8000-000000000001\"}"))
			return
		}
		respond(w, map[string]any{"items": []any{}, "next_cursor": nil})
	})
	out, _, err := run(t, "workspace", "list", "-o", "json")
	if out != "" || err == nil || !strings.Contains(err.Error(), "STORAGE_UNAVAILABLE") || requests.Load() != 1 {
		t.Fatalf("failed workspace read was reported as success or automatically retried: %q (%v)", out, err)
	}
	out, _, err = run(t, "workspace", "list", "-o", "json")
	var page boundedPage
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || page.Items == nil || len(page.Items) != 0 ||
		page.NextCursor != nil || requests.Load() != 2 {
		t.Fatalf("retry lost the empty final page: %q (%v)", out, err)
	}
}
