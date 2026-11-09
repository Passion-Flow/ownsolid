package distribution_test

import (
	"bytes"
	"encoding/csv"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

type usageCSVClient func(workspace string, values url.Values, status int) ([]byte, string)

func newUsageCSVClient(t *testing.T, c scenario, audience, origin string) usageCSVClient {
	t.Helper()
	cookies, err := httpserver.NewCookiePolicy(c.s.Identity.Config.Server.EffectiveTransportMode())
	if err != nil {
		t.Fatal(err)
	}
	_, session, _, err := c.s.Identity.Login(t.Context(), "admin@example.invalid", c.password, audience, "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	// This fixture mounts the service artifact over TLS without registering a public route.
	server := httpfixture.NewTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential, _ := cookies.Read(r, audience, httpserver.SessionCookie)
		a, err := c.s.Identity.Authenticate(r.Context(), credential, audience)
		if err != nil {
			httpserver.Problem(w, uuid.NewString(), err)
			return
		}
		export, err := c.s.ExportUsagePage(r.Context(), a, strings.TrimPrefix(r.URL.Path, "/fixture/export/"), r.URL.Query())
		if err != nil {
			httpserver.Problem(w, uuid.NewString(), err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		if export.NextCursor != nil {
			w.Header().Set("X-Fixture-Next-Cursor", *export.NextCursor)
		}
		w.Write(export.CSV)
	}))
	return func(workspace string, values url.Values, status int) ([]byte, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin+"/fixture/export/"+workspace+"?"+values.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: cookies.Name(audience, httpserver.SessionCookie), Value: session})
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatal("unexpected CSV fixture response", response.StatusCode, status, err)
		}
		cursor := response.Header.Get("X-Fixture-Next-Cursor")
		csv := response.Header.Get("Content-Type") == "text/csv; charset=utf-8"
		if csv != (status == http.StatusOK) || !csv && (cursor != "" || bytes.Contains(raw, []byte("request_count"))) {
			t.Fatal("CSV content or pagination escaped a failed response")
		}
		return raw, cursor
	}
}

func assertGatewayUsageCSV(t *testing.T, c, foreign scenario, a, other access, stamp time.Time, ordered []string, expected map[string][]string) {
	t.Helper()
	workspace := a.alias.WorkspaceID
	portal := newUsageCSVClient(t, c, "portal", httpfixture.PortalOrigin)
	admin := newUsageCSVClient(t, c, "admin", httpfixture.AdminOrigin)
	foreignAdmin := newUsageCSVClient(t, foreign, "admin", httpfixture.AdminOrigin)

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

	var firstCursor string
	values := url.Values{"api_key_id": {a.key.Key.ID}, "limit": {"1"}}
	for i, id := range ordered {
		raw, cursor := portal(workspace, values, http.StatusOK)
		records := read(raw)
		want := append([]string{id, stamp.Format(time.RFC3339Nano)}, expected[id]...)
		if len(records) != 1 || !slices.Equal(records[0], want) || (cursor == "") != (i == len(ordered)-1) {
			t.Fatal("usage export row or cursor mismatch", i)
		}
		if i == 0 {
			firstCursor = cursor
			replay, replayCursor := portal(workspace, values, http.StatusOK)
			if !bytes.Equal(raw, replay) || replayCursor != cursor {
				t.Fatal("unchanged CSV page differed on retry")
			}
		}
		values.Set("cursor", cursor)
	}
	raw, _ := portal(workspace, url.Values{"api_key_id": {other.key.Key.ID}}, http.StatusOK)
	if len(read(raw)) != 0 {
		t.Fatal("portal exported a different actor's key history")
	}
	raw, _ = admin(workspace, nil, http.StatusOK)
	if records := read(raw); len(records) != len(ordered)+1 {
		t.Fatal("admin export lost workspace records")
	}
	portal(workspace, url.Values{"limit": {"101"}}, http.StatusBadRequest)
	portal(workspace, url.Values{"api_key_id": {other.key.Key.ID}, "cursor": {firstCursor}}, http.StatusBadRequest)
	admin(workspace, url.Values{"api_key_id": {a.key.Key.ID}, "cursor": {firstCursor}}, http.StatusBadRequest)
	foreignAdmin(workspace, nil, http.StatusNotFound)
	after, auditAfter := snapshot()
	if !bytes.Equal(before, after) || auditBefore != auditAfter {
		t.Fatal("export or retry changed the request ledger")
	}

	// The stored receipt remains syntactically valid but no longer matches its digest.
	if _, err := c.s.DB.Exec(t.Context(), "UPDATE gateway_requests SET outcome=jsonb_set(outcome,'{elapsed_milliseconds}','99') WHERE id=$1", orderedReported(ordered, expected)); err != nil {
		t.Fatal(err)
	}
	portal(workspace, url.Values{"api_key_id": {a.key.Key.ID}}, http.StatusServiceUnavailable)
}

func orderedReported(ordered []string, expected map[string][]string) string {
	for _, id := range ordered {
		if expected[id][0] == "reported" {
			return id
		}
	}
	return ""
}
