package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestUserReadsPreserveGlobalFiltersAndOutput(t *testing.T) {
	const prefix, search, cursor = " ALICE+%_", " %_ \u4e00 ", "opaque+/=&%"
	row := map[string]any{"id": nodeID, "email": "member@example.invalid", "display_name": "Research \u4e00",
		"locale": "zh-CN", "status": "disabled", "is_platform_admin": false, "email_verified_at": nil,
		"version": int64(9007199254740991), "created_at": "2026-11-12T00:00:00Z", "updated_at": "2026-11-12T01:00:00Z"}
	requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.ContentLength != 0 || r.Header.Get("X-CSRF-Token") != "" {
			t.Error("account read acquired a mutation")
		}
		switch r.URL.Path {
		case userReadPath:
			q := r.URL.Query()
			if q.Get("email_prefix") != prefix || q.Get("q") != search || q.Get("status") != "disabled" ||
				q.Get("limit") != "1" || q.Get("cursor") != cursor || len(q) != 5 {
				t.Error("account list changed literal filters, cursor or global scope")
			}
			respond(w, map[string]any{"items": []any{row}, "next_cursor": "continue"})
		case userReadPath + "/" + nodeID:
			if r.URL.RawQuery != "" {
				t.Error("account detail acquired configured context or filters")
			}
			respond(w, row)
		default:
			t.Error("account read changed its global route")
			w.WriteHeader(http.StatusNotFound)
		}
	})
	for _, format := range []string{"json", "yaml", "table"} {
		out, stderr, err := run(t, "user", "list", "--email-prefix", prefix, "--query", search,
			"--status", "disabled", "--limit", "1", "--cursor", cursor, "-o", format)
		if err != nil || stderr != "" {
			t.Fatalf("user list %s: %q (%v)", format, stderr, err)
		}
		if format == "table" {
			for _, text := range []string{nodeID, "disabled", "false", "Next cursor: \"continue\""} {
				if !strings.Contains(out, text) {
					t.Errorf("user table omitted %q: %q", text, out)
				}
			}
		} else {
			var page struct {
				Items []struct {
					ID       string `json:"id" yaml:"id"`
					Version  int64  `json:"version" yaml:"version"`
					Admin    *bool  `json:"is_platform_admin" yaml:"is_platform_admin"`
					Verified any    `json:"email_verified_at" yaml:"email_verified_at"`
				} `json:"items" yaml:"items"`
				Cursor string `json:"next_cursor" yaml:"next_cursor"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(out), &page)
			} else {
				err = yaml.Unmarshal([]byte(out), &page)
			}
			if err != nil || len(page.Items) != 1 || page.Items[0].ID != nodeID || page.Items[0].Version != 9007199254740991 ||
				page.Items[0].Admin == nil || *page.Items[0].Admin || page.Items[0].Verified != nil || page.Cursor != "continue" {
				t.Fatalf("account %s lost exact fields: %q (%v)", format, out, err)
			}
		}
		detail, _, err := run(t, "user", "get", nodeID, "-o", format)
		if err != nil || !strings.Contains(detail, nodeID) {
			t.Fatalf("account detail %s: %q (%v)", format, detail, err)
		}
	}
	if requests.Load() != 6 {
		t.Fatal("account reads made extra requests or unrequested context lookup")
	}
}

func TestUserArgumentsRefuseBeforeIO(t *testing.T) {
	requests := authenticatedCLIServer(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid account arguments reached HTTP") })
	for _, args := range [][]string{
		{"get"}, {"get", "alice@example.invalid"}, {"get", "00000000-0000-0000-0000-000000000000"},
		{"get", "ABCDEF01-0000-4000-8000-000000000001"}, {"get", nodeID, "extra"}, {"list", "extra"},
		{"list", "--email-prefix", ""}, {"list", "--email-prefix", "\u4e00"}, {"list", "--email-prefix", "\x7f"},
		{"list", "--email-prefix", strings.Repeat("x", 255)}, {"list", "--query", ""}, {"list", "--query", "\xff"},
		{"list", "--query", "\n"}, {"list", "--query", strings.Repeat("\u4e00", 255)}, {"list", "--status", ""},
		{"list", "--status", "ACTIVE"}, {"list", "--limit", "0"}, {"list", "--limit", "101"},
		{"list", "--cursor", ""}, {"list", "--cursor", "\x1b"}, {"list", "--cursor", strings.Repeat("x", 513)},
		{"list", "--workspace", workspaceID},
	} {
		out, _, err := run(t, append([]string{"user"}, args...)...)
		if err == nil || out != "" {
			t.Errorf("invalid user arguments %v produced %q (%v)", args, out, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid account input caused I/O")
	}
}

func TestUserFilterBoundariesUseServerPagePolicy(t *testing.T) {
	prefix, search := strings.Repeat("x", 254), strings.Repeat("\u4e00", 254)
	requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("email_prefix") != prefix || q.Get("q") != search || q.Get("status") != "active" || len(q) != 3 {
			t.Error("valid boundary filters were changed or server pagination policy overridden")
		}
		respond(w, map[string]any{"items": []any{}, "next_cursor": nil})
	})
	out, _, err := run(t, "user", "list", "--email-prefix", prefix, "--query", search, "--status", "active", "-o", "json")
	var page boundedPage
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || page.Items == nil || len(page.Items) != 0 || page.NextCursor != nil || requests.Load() != 1 {
		t.Fatalf("boundary filter/empty final page: %q (%v)", out, err)
	}
}

func TestUserMalformedResponsesHaveNoPartialOutput(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value any
		args  []string
	}{
		{"missing items", map[string]any{"next_cursor": nil}, []string{"list"}},
		{"empty continuation", map[string]any{"items": []any{}, "next_cursor": "next"}, []string{"list"}},
		{"over bound", map[string]any{"items": []any{map[string]string{"id": nodeID}, map[string]string{"id": secondNodeID}}, "next_cursor": nil}, []string{"list", "--limit", "1"}},
		{"repeat cursor", map[string]any{"items": []any{map[string]string{"id": nodeID}}, "next_cursor": "same"}, []string{"list", "--cursor", "same"}},
		{"invalid later row", map[string]any{"items": []any{map[string]string{"id": nodeID}, map[string]string{"id": "invalid"}}, "next_cursor": nil}, []string{"list"}},
		{"null row", map[string]any{"items": []any{nil}, "next_cursor": nil}, []string{"list"}},
		{"zero ID", map[string]any{"items": []any{map[string]string{"id": "00000000-0000-0000-0000-000000000000"}}, "next_cursor": nil}, []string{"list"}},
		{"wrong detail", map[string]string{"id": secondNodeID}, []string{"get", nodeID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			authenticatedCLIServer(t, func(w http.ResponseWriter, _ *http.Request) { respond(w, tt.value) })
			for _, format := range []string{"table", "json", "yaml"} {
				args := append([]string{"user"}, tt.args...)
				args = append(args, "-o", format)
				out, _, err := run(t, args...)
				if err == nil || out != "" {
					t.Fatalf("malformed account response printed %q (%v)", out, err)
				}
			}
		})
	}
}

func TestUserRefusalAndExplicitRetry(t *testing.T) {
	for _, tt := range []struct {
		status int
		code   string
	}{
		{401, "AUTH_SESSION_EXPIRED"}, {403, "ACCESS_DENIED"}, {404, "RESOURCE_NOT_FOUND"}, {400, "INVALID_CURSOR"}, {503, "STORAGE_UNAVAILABLE"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			var attempts atomic.Int64
			requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != userReadPath || r.URL.RawQuery != "" {
					t.Error("default account read changed scope or query")
				}
				if attempts.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(tt.status)
					_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":"Account read refused","status":%d,"code":%q,"request_id":"12345678-0000-4000-8000-000000000009"}`, tt.status, tt.code)
					return
				}
				respond(w, map[string]any{"items": []any{}, "next_cursor": nil})
			})
			out, _, err := run(t, "user", "list", "-o", "json")
			if err == nil || out != "" || !strings.Contains(err.Error(), tt.code) || requests.Load() != 1 {
				t.Fatalf("user failure hidden or retried: %q (%v)", out, err)
			}
			out, _, err = run(t, "user", "list", "-o", "json")
			if err != nil || !json.Valid([]byte(out)) || requests.Load() != 2 {
				t.Fatalf("explicit user retry failed: %q (%v)", out, err)
			}
		})
	}
}
