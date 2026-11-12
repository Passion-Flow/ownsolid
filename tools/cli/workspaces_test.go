package cli

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

func workspaceReadServer(t *testing.T, handler http.HandlerFunc) *atomic.Int64 {
	t.Helper()
	session := token(t)
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cookie, err := r.Cookie("__Host-admin_session")
		if err != nil || cookie.Value != session || r.Method != http.MethodGet {
			t.Error("workspace request lost its Admin session or read method")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	dir := privateDir(t)
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", "")
	ca := filepath.Join(dir, "ca.pem")
	writeFile(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	writeConfig(t, dir, fileConfig{
		Server: server.URL, CAFile: ca, SiteID: "other-site", CellID: "other-cell", WorkspaceID: secondNodeID,
	})
	cfg, err := loadSettings("", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.root.Close() })
	if err := store.save(session); err != nil {
		t.Fatal(err)
	}
	return &requests
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
