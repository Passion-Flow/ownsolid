package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBrowserSessionListUsesGlobalScopeAndPreservesContinuation(t *testing.T) {
	requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != browserSessionsPath ||
			r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("cursor") != "first" || len(r.URL.Query()) != 2 {
			t.Error("session read changed its global route or pagination")
		}
		respond(w, map[string]any{"items": []any{map[string]any{
			"id": workspaceID, "current": true, "authentication_method": "password",
			"created_at": "2026-11-01T00:00:00Z", "last_seen_at": "2026-11-02T00:00:00Z",
			"absolute_expires_at": "2026-11-03T00:00:00Z",
		}}, "next_cursor": "next"})
	})
	out, _, err := run(t, "session", "list", "--limit", "1", "--cursor", "first", "-o", "json")
	var page boundedPage
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Items) != 1 ||
		page.NextCursor == nil || *page.NextCursor != "next" || !strings.Contains(out, "\"current\": true") || requests.Load() != 1 {
		t.Fatalf("session page: %q (%v)", out, err)
	}
}

func TestBrowserSessionRevokeSendsExactCSRFDeleteAndEndsLocalCurrentCredential(t *testing.T) {
	const csrf = "fixture-csrf"
	var deletes atomic.Int64
	requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == authPath+"/csrf":
			http.SetCookie(w, &http.Cookie{Name: "__Host-admin_csrf_context", Value: "context", Path: "/", Secure: true})
			respond(w, map[string]string{"token": csrf})
		case r.Method == http.MethodDelete:
			if r.URL.Path != browserSessionsPath+"/"+nodeID && r.URL.Path != browserSessionsPath+"/"+workspaceID {
				t.Error("wrong target session")
			}
			if r.Header.Get("Origin") != "https://"+r.Host || r.Header.Get("X-CSRF-Token") != csrf ||
				r.URL.RawQuery != "" || r.ContentLength != 0 || r.Header.Get("Idempotency-Key") != "" {
				t.Error("session revocation changed the public DELETE contract")
			}
			deletes.Add(1)
			if strings.HasSuffix(r.URL.Path, "/"+workspaceID) {
				http.SetCookie(w, &http.Cookie{Name: "__Host-admin_session", Value: "", Path: "/", Secure: true, MaxAge: -1})
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected session method or route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	out, _, err := run(t, "session", "revoke", nodeID, "--confirm", "-o", "json")
	if err != nil || !strings.Contains(out, "\"current_session_ended\": false") {
		t.Fatalf("other session revocation: %q (%v)", out, err)
	}
	out, _, err = run(t, "session", "revoke", workspaceID, "--confirm", "-o", "json")
	if err != nil || !strings.Contains(out, "\"current_session_ended\": true") || deletes.Load() != 2 {
		t.Fatalf("current session revocation: %q (%v)", out, err)
	}
	before := requests.Load()
	out, _, err = run(t, "session", "list")
	if out != "" || err == nil || !strings.Contains(err.Error(), "no saved Admin session") || requests.Load() != before {
		t.Fatalf("current credential was retained after acknowledged revocation: %q (%v)", out, err)
	}
}

func TestBrowserSessionRefusalRetainsCredentialAndReportsUncertainty(t *testing.T) {
	for _, tt := range []struct {
		status     int
		code, want string
	}{
		{403, "ACCESS_DENIED", "ACCESS_DENIED"},
		{404, "RESOURCE_NOT_FOUND", "RESOURCE_NOT_FOUND"},
		{503, "STORAGE_UNAVAILABLE", "result uncertain"},
		{409, "STORAGE_COMMIT_OUTCOME_UNKNOWN", "result uncertain"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath+"/csrf" {
					http.SetCookie(w, &http.Cookie{Name: "__Host-admin_csrf_context", Value: "context", Path: "/", Secure: true})
					respond(w, map[string]string{"token": "csrf"})
					return
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "code": tt.code, "status": tt.status})
			})
			cfg, err := loadSettings("", false)
			if err != nil {
				t.Fatal(err)
			}
			store, err := openSessionStore(cfg, false)
			if err != nil {
				t.Fatal(err)
			}
			defer store.root.Close()
			before, err := store.read()
			if err != nil {
				t.Fatal(err)
			}
			out, _, err := run(t, "session", "revoke", nodeID, "--confirm")
			after, readErr := store.read()
			if out != "" || err == nil || !strings.Contains(err.Error(), tt.want) || readErr != nil || after != before {
				t.Fatalf("revocation refusal changed the credential or reported success: %q (%v)", out, err)
			}
		})
	}
}

func TestBrowserSessionArgumentsFailBeforeIO(t *testing.T) {
	requests := authenticatedCLIServer(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid session input reached the server") })
	for _, args := range [][]string{
		{"revoke", nodeID}, {"revoke", "00000000-0000-0000-0000-000000000000", "--confirm"},
		{"revoke", "../sessions", "--confirm"}, {"revoke", strings.ToUpper("abcdef01-0000-4000-8000-000000000001"), "--confirm"},
		{"list", "--limit", "101"}, {"list", "--cursor", ""}, {"list", "--workspace", workspaceID},
	} {
		out, _, err := run(t, append([]string{"session"}, args...)...)
		if out != "" || err == nil {
			t.Fatalf("invalid session arguments produced %q (%v)", out, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid session arguments caused I/O")
	}
}

func TestBrowserSessionRevokeRefusesChangedCredentialAndBusySessionWriter(t *testing.T) {
	requests := authenticatedCLIServer(t, func(http.ResponseWriter, *http.Request) { t.Error("stale session reached the server") })
	cfg, err := loadSettings("", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	old, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	client, err := newClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	client.restoreSession(old)
	fresh := token(t)
	if err := store.save(fresh); err != nil {
		t.Fatal(err)
	}
	opts := options{output: "json"}
	cmd := NewCommand()
	if err := opts.revokeBrowserSession(cmd, cfg, client, nodeID); err == nil || !strings.Contains(err.Error(), "session changed") {
		t.Fatalf("changed credential was not refused: %v", err)
	}
	lock, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	out, _, revokeErr := run(t, "session", "revoke", nodeID, "--confirm")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if out != "" || revokeErr == nil || !strings.Contains(revokeErr.Error(), "another session change") || requests.Load() != 0 {
		t.Fatalf("busy session writer was not refused: %q (%v)", out, revokeErr)
	}
	contents, err := os.ReadFile(filepath.Join(cfg.dir, store.name))
	if err != nil || string(contents) != fresh {
		t.Fatal("refused stale command changed the new credential")
	}
}
