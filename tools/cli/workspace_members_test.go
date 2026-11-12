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

func TestWorkspaceMemberPagePreservesIdentityScopeAndFormats(t *testing.T) {
	const cursor = "opaque+/=&%"
	row := map[string]any{
		"user_id": nodeID, "email": "member@example.invalid", "display_name": "Research \u4e00",
		"user_status": "disabled", "role": "viewer", "version": int64(9007199254740991),
		"created_at": "2026-11-12T00:00:00Z", "updated_at": "2026-11-12T01:00:00Z",
	}
	requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != workspaceReadPath+"/"+workspaceID+"/members" ||
			r.URL.Query().Get("cursor") != cursor || r.URL.Query().Get("limit") != "1" || len(r.URL.Query()) != 2 {
			t.Error("member read changed its method, exact workspace or opaque pagination")
		}
		if r.ContentLength != 0 || r.Header.Get("X-CSRF-Token") != "" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("member read acquired mutation state")
		}
		respond(w, map[string]any{"items": []any{row}, "next_cursor": "continue"})
	})
	for _, format := range []string{"json", "yaml", "table"} {
		out, stderr, err := run(t, "workspace", "member", "list", workspaceID,
			"--cursor", cursor, "--limit", "1", "-o", format)
		if err != nil || stderr != "" {
			t.Fatalf("member %s read failed: %v (%q)", format, err, stderr)
		}
		switch format {
		case "json":
			var page boundedPage
			if json.Unmarshal([]byte(out), &page) != nil || len(page.Items) != 1 || page.NextCursor == nil || *page.NextCursor != "continue" {
				t.Fatalf("member JSON lost its page: %q", out)
			}
			var member struct {
				UserID  string `json:"user_id"`
				Version int64  `json:"version"`
			}
			if json.Unmarshal(page.Items[0], &member) != nil || member.UserID != nodeID || member.Version != 9007199254740991 {
				t.Fatal("member JSON changed identity or version precision")
			}
		case "yaml":
			var page struct {
				Items []struct {
					UserID  string `yaml:"user_id"`
					Version int64  `yaml:"version"`
				} `yaml:"items"`
				NextCursor string `yaml:"next_cursor"`
			}
			if yaml.Unmarshal([]byte(out), &page) != nil || len(page.Items) != 1 || page.Items[0].UserID != nodeID ||
				page.Items[0].Version != 9007199254740991 || page.NextCursor != "continue" {
				t.Fatalf("member YAML changed fields or precision: %q", out)
			}
		case "table":
			for _, value := range []string{nodeID, "member@example.invalid", "disabled", "viewer", "Next cursor: \"continue\""} {
				if !strings.Contains(out, value) {
					t.Errorf("member table omitted %q: %q", value, out)
				}
			}
		}
	}
	if requests.Load() != 3 {
		t.Fatal("member read made extra requests or followed an unrequested page")
	}
}

func TestWorkspaceMemberArgumentsRefuseBeforeIO(t *testing.T) {
	requests := authenticatedCLIServer(t, func(http.ResponseWriter, *http.Request) {
		t.Error("invalid member arguments reached the server")
	})
	for _, args := range [][]string{
		{}, {workspaceID, nodeID}, {"research"}, {"../members"}, {"00000000-0000-0000-0000-000000000000"},
		{"ABCDEF01-0000-4000-8000-000000000001"}, {workspaceID, "--limit", "0"}, {workspaceID, "--limit", "101"},
		{workspaceID, "--cursor", ""}, {workspaceID, "--cursor", "\x1b"},
		{workspaceID, "--cursor", strings.Repeat("x", 513)}, {workspaceID, "--workspace", secondNodeID},
		{workspaceID, "--query", "member"},
	} {
		out, _, err := run(t, append([]string{"workspace", "member", "list"}, args...)...)
		if err == nil || out != "" {
			t.Errorf("invalid member arguments %v produced %q (%v)", args, out, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid arguments caused network I/O")
	}
}

func TestWorkspaceMemberMalformedPageHasNoPartialOutput(t *testing.T) {
	for _, tt := range []struct {
		name   string
		value  any
		cursor string
	}{
		{"missing items", map[string]any{"next_cursor": nil}, ""},
		{"null items", map[string]any{"items": nil, "next_cursor": nil}, ""},
		{"empty continued page", map[string]any{"items": []any{}, "next_cursor": "next"}, ""},
		{"repeated cursor", map[string]any{"items": []any{map[string]string{"user_id": nodeID}}, "next_cursor": "same"}, "same"},
		{"invalid cursor", map[string]any{"items": []any{map[string]string{"user_id": nodeID}}, "next_cursor": "\x1b"}, ""},
		{"missing user ID", map[string]any{"items": []any{map[string]string{"id": nodeID}}, "next_cursor": nil}, ""},
		{"null member", map[string]any{"items": []any{nil}, "next_cursor": nil}, ""},
		{"invalid later user ID", map[string]any{"items": []any{map[string]string{"user_id": nodeID}, map[string]string{"user_id": "invalid"}}, "next_cursor": nil}, ""},
		{"zero user ID", map[string]any{"items": []any{map[string]string{"user_id": "00000000-0000-0000-0000-000000000000"}}, "next_cursor": nil}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			authenticatedCLIServer(t, func(w http.ResponseWriter, _ *http.Request) { respond(w, tt.value) })
			for _, format := range []string{"table", "json", "yaml"} {
				args := []string{"workspace", "member", "list", workspaceID, "-o", format}
				if tt.cursor != "" {
					args = append(args, "--cursor", tt.cursor)
				}
				out, _, err := run(t, args...)
				if err == nil || out != "" {
					t.Fatalf("malformed member page printed %q (%v)", out, err)
				}
			}
		})
	}
}

func TestWorkspaceMemberPageBound(t *testing.T) {
	authenticatedCLIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(w, map[string]any{"items": []any{map[string]string{"user_id": nodeID}, map[string]string{"user_id": secondNodeID}}, "next_cursor": nil})
	})
	out, _, err := run(t, "workspace", "member", "list", workspaceID, "--limit", "1", "-o", "json")
	if err == nil || out != "" {
		t.Fatalf("over-bound member page printed %q (%v)", out, err)
	}
}

func TestWorkspaceMemberErrorsAndManualRetry(t *testing.T) {
	for _, tt := range []struct {
		status int
		code   string
	}{
		{401, "AUTH_SESSION_EXPIRED"}, {403, "ACCESS_DENIED"}, {404, "RESOURCE_NOT_FOUND"},
		{400, "INVALID_CURSOR"}, {503, "STORAGE_UNAVAILABLE"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			var attempts atomic.Int64
			requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != workspaceReadPath+"/"+workspaceID+"/members" || len(r.URL.Query()) != 0 {
					t.Error("default member page changed method, scope or server policy")
				}
				if attempts.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(tt.status)
					_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":"Member read refused","status":%d,"code":%q,"request_id":"12345678-0000-4000-8000-000000000009"}`, tt.status, tt.code)
					return
				}
				respond(w, map[string]any{"items": []any{}, "next_cursor": nil})
			})
			out, _, err := run(t, "workspace", "member", "list", workspaceID, "-o", "json")
			if err == nil || out != "" || !strings.Contains(err.Error(), tt.code) || requests.Load() != 1 {
				t.Fatalf("member failure was hidden or retried: %q (%v)", out, err)
			}
			out, _, err = run(t, "workspace", "member", "list", workspaceID, "-o", "json")
			var page boundedPage
			if err != nil || json.Unmarshal([]byte(out), &page) != nil || page.Items == nil || len(page.Items) != 0 ||
				page.NextCursor != nil || requests.Load() != 2 {
				t.Fatalf("explicit member retry lost the empty final page: %q (%v)", out, err)
			}
		})
	}
}
