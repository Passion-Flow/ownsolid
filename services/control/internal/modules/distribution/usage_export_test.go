package distribution_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

type usageCSVClient struct {
	server *httptest.Server
	origin string
	path   string
	cookie *http.Cookie
}

func newUsageCSVClient(t *testing.T, c scenario, server *httptest.Server, audience, origin, historyPath string) usageCSVClient {
	t.Helper()
	cookies, err := httpserver.NewCookiePolicy(c.s.Identity.Config.Server.EffectiveTransportMode())
	if err != nil {
		t.Fatal(err)
	}
	_, session, _, err := c.s.Identity.Login(t.Context(), "admin@example.invalid", c.password, audience, "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return usageCSVClient{
		server: server, origin: origin, path: cellroute.Public(historyPath+"/export", agentwire.LocalScope()),
		cookie: &http.Cookie{Name: cookies.Name(audience, httpserver.SessionCookie), Value: session},
	}
}

func (c usageCSVClient) get(t *testing.T, query string, status int, code string) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.origin+c.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.URL.RawQuery = query
	request.Header.Set("Origin", c.origin)
	if c.cookie != nil {
		request.AddCookie(c.cookie)
	}
	response, err := c.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatal("unexpected CSV response", response.StatusCode, status, err)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
		t.Fatal("missing dispatcher security headers")
	}
	if status != http.StatusOK {
		var problem struct {
			Code   string `json:"code"`
			Status int    `json:"status"`
		}
		if response.Header.Get("Content-Type") != "application/problem+json" ||
			response.Header.Get("Content-Disposition") != "" || response.Header.Get("X-Artifact-SHA256") != "" ||
			response.Header.Get("Link") != "" || json.Unmarshal(raw, &problem) != nil ||
			problem.Code != code || problem.Status != status || bytes.Contains(raw, []byte("request_count")) {
			t.Fatal("invalid export problem or attachment data on failure", problem.Code, code)
		}
		return nil
	}
	disposition, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	digest := sha256.Sum256(raw)
	if err != nil || disposition != "attachment" || len(params) != 1 || params["filename"] != "gateway-usage.csv" ||
		response.Header.Get("Content-Type") != "text/csv; charset=utf-8" || response.ContentLength != int64(len(raw)) ||
		response.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(digest[:]) {
		t.Fatal("invalid or inconsistent CSV attachment metadata")
	}
	return raw
}

func assertGatewayUsageCSV(t *testing.T, c, foreign scenario, server, foreignServer *httptest.Server, a, other access, stamp time.Time, ordered, cursors []string, expected map[string][]string) {
	t.Helper()
	workspace := a.alias.WorkspaceID
	portalPath := "/api/v1/workspaces/" + workspace + "/distribution/requests"
	adminPath := "/api/v1/admin/distribution/workspaces/" + workspace + "/requests"
	portal := newUsageCSVClient(t, c, server, "portal", httpfixture.PortalOrigin, portalPath)
	admin := newUsageCSVClient(t, c, server, "admin", httpfixture.AdminOrigin, adminPath)
	foreignAdmin := newUsageCSVClient(t, foreign, foreignServer, "admin", httpfixture.AdminOrigin, adminPath)
	anonymous := portal
	anonymous.cookie = nil

	snapshot := func() ([]byte, int) {
		t.Helper()
		var rows []byte
		var auditEvents int
		if err := c.s.DB.QueryRow(t.Context(), "SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id), (SELECT count(*) FROM audit_events WHERE action LIKE 'gateway.request_%') FROM gateway_requests r").Scan(&rows, &auditEvents); err != nil {
			t.Fatal(err)
		}
		return rows, auditEvents
	}
	before, auditBefore := snapshot()
	read := func(raw []byte) [][]string {
		t.Helper()
		header := []string{"request_id", "admitted_at", "request_state", "outcome_status", "request_count", "elapsed_ms", "prompt_tokens", "completion_tokens", "total_tokens"}
		records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
		if err != nil || len(records) == 0 || !slices.Equal(records[0], header) || !bytes.HasSuffix(raw, []byte("\r\n")) {
			t.Fatal("invalid or unexpected usage export columns", err)
		}
		return records[1:]
	}

	values := url.Values{"api_key_id": {a.key.Key.ID}, "limit": {"1"}}
	for i, id := range ordered {
		raw := portal.get(t, values.Encode(), http.StatusOK, "")
		records := read(raw)
		want := append([]string{id, stamp.Format(time.RFC3339Nano)}, expected[id]...)
		if len(records) != 1 || !slices.Equal(records[0], want) {
			t.Fatal("usage export row mismatch", i)
		}
		if i == 0 && !bytes.Equal(raw, portal.get(t, values.Encode(), http.StatusOK, "")) {
			t.Fatal("unchanged CSV page differed on retry")
		}
		if i < len(cursors) {
			values.Set("cursor", cursors[i])
		}
	}
	raw := portal.get(t, url.Values{"api_key_id": {other.key.Key.ID}}.Encode(), http.StatusOK, "")
	if len(read(raw)) != 0 {
		t.Fatal("portal exported a different actor's key history")
	}
	raw = admin.get(t, "", http.StatusOK, "")
	if records := read(raw); len(records) != len(ordered)+1 {
		t.Fatal("admin export lost workspace records")
	}
	anonymous.get(t, "", http.StatusUnauthorized, "AUTH_REQUIRED")
	portal.get(t, "api_key_id=%ZZ", http.StatusBadRequest, "VALIDATION_FAILED")
	portal.get(t, "limit=101", http.StatusBadRequest, "VALIDATION_FAILED")
	changed := url.Values{"api_key_id": {other.key.Key.ID}, "cursor": {cursors[0]}}
	portal.get(t, changed.Encode(), http.StatusBadRequest, "INVALID_CURSOR")
	changed.Set("api_key_id", a.key.Key.ID)
	admin.get(t, changed.Encode(), http.StatusBadRequest, "INVALID_CURSOR")
	foreignAdmin.get(t, "", http.StatusNotFound, "RESOURCE_NOT_FOUND")
	after, auditAfter := snapshot()
	if !bytes.Equal(before, after) || auditBefore != auditAfter {
		t.Fatal("export or retry changed the request ledger")
	}

	// The stored receipt remains syntactically valid but no longer matches its digest.
	if _, err := c.s.DB.Exec(t.Context(), "UPDATE gateway_requests SET outcome=jsonb_set(outcome,'{elapsed_milliseconds}','99') WHERE id=$1", orderedReported(ordered, expected)); err != nil {
		t.Fatal(err)
	}
	portal.get(t, url.Values{"api_key_id": {a.key.Key.ID}}.Encode(), http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE")
}

func orderedReported(ordered []string, expected map[string][]string) string {
	for _, id := range ordered {
		if expected[id][0] == "reported" {
			return id
		}
	}
	return ""
}
