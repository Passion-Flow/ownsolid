package cli

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayRequestPageRefusesUnexpectedScopeOrCursor(t *testing.T) {
	session := token(t)
	requestPath := adminPath + "distribution/workspaces/" + workspaceID + "/requests"
	row := map[string]string{"id": deployID, "workspace_id": workspaceID}
	foreign := map[string]string{"id": modelID, "workspace_id": secondNodeID}
	cases := []struct {
		name, cursor, next, want string
		items                    []any
	}{
		{"valid", "", "next", "", []any{row}},
		{"foreign second row", "", "next", "outside the selected workspace", []any{row, foreign}},
		{"control in cursor", "", "\x1b[1A", "invalid gateway request cursor", []any{row}},
		{"repeated cursor", "same", "same", "invalid gateway request cursor", []any{row}},
		{"empty with cursor", "", "next", "invalid gateway request cursor", []any{}},
		{"missing items", "", "next", "invalid gateway request page", nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie("__Host-admin_session")
				if err != nil || cookie.Value != session {
					t.Error("request lacks its Admin session")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				query := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != requestPath || query.Get("limit") != "2" ||
					query.Get("cursor") != tt.cursor || query.Get("api_key_id") != nodeID {
					t.Error("request changed the scope or page arguments")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				respond(w, map[string]any{"items": tt.items, "next_cursor": tt.next})
			}))
			defer server.Close()
			dir := privateDir(t)
			t.Setenv("OWNSOLID_CONFIG_DIR", dir)
			t.Setenv("OWNSOLID_SERVER", "")
			ca := filepath.Join(dir, "ca.pem")
			writeFile(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
			writeConfig(t, dir, fileConfig{Server: server.URL, CAFile: ca, SiteID: "site-a", CellID: "cell-b"})
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

			args := []string{"gateway", "request", "list", "--workspace", workspaceID, "--limit", "2", "--api-key-id", nodeID, "-o", "json"}
			if tt.cursor != "" {
				args = append(args, "--cursor", tt.cursor)
			}
			out, _, err := run(t, args...)
			if tt.want != "" {
				if out != "" || err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("unexpected output %q or error %v", out, err)
				}
				return
			}
			var page boundedPage
			if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Items) != 1 || page.NextCursor == nil || *page.NextCursor != tt.next {
				t.Fatalf("unexpected output %q or error %v", out, err)
			}
		})
	}
}
