package http

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
)

const cardinalityWorkspace = "11111111-1111-4111-8111-111111111111"
const cardinalityDay = "2026-11-12"

func cardinalityOptions() url.Values {
	return url.Values{"workspace_id": {cardinalityWorkspace}, "date": {cardinalityDay}}
}

func TestSeriesCardinalityCanonicalHTTPQuery(t *testing.T) {
	for _, tc := range []struct {
		name, metric, limit string
		wantLimit           int
	}{
		{"omitted defaults", "", "", 0},
		{"exact Unicode and escapes", "m\u00e9tric\"\\escaped", "2", 2},
		{"literal whitespace", "  cpu  ", "1", 1},
		{"UTF8 byte boundary", strings.Repeat("\u00e9", 512), "100", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := cardinalityOptions()
			if tc.metric != "" {
				values.Set("metric", tc.metric)
			}
			if tc.limit != "" {
				values.Set("limit", tc.limit)
			}
			workspace, selection, err := parseSeriesCardinalityQuery(values.Encode())
			if err != nil || workspace != cardinalityWorkspace || selection.Metric != tc.metric || selection.Limit != tc.wantLimit ||
				selection.Date.Location() != time.UTC || selection.Date.Format(time.DateOnly) != cardinalityDay || selection.Validate() != nil {
				t.Fatalf("invalid canonical query: %q %+v %v", workspace, selection, err)
			}
		})
	}
}

func TestSeriesCardinalityRefusesAmbiguousHTTPQuery(t *testing.T) {
	query := func(key, value string) string {
		values := cardinalityOptions()
		values.Set(key, value)
		return values.Encode()
	}
	duplicate := cardinalityOptions()
	duplicate.Add("date", cardinalityDay)
	for _, tc := range []struct {
		name, raw string
	}{
		{"missing workspace", url.Values{"date": {cardinalityDay}}.Encode()},
		{"missing date", url.Values{"workspace_id": {cardinalityWorkspace}}.Encode()},
		{"empty workspace", query("workspace_id", "")},
		{"all-index day", query("date", "0")},
		{"partial date", query("date", "2026-1-2")},
		{"date-time", query("date", cardinalityDay+"T00:00:00Z")},
		{"pre-epoch day", query("date", "1969-12-31")},
		{"explicit empty metric", query("metric", "")},
		{"invalid UTF8 metric", query("metric", string([]byte{255}))},
		{"excess UTF8 bytes", query("metric", strings.Repeat("\u00e9", 513))},
		{"empty limit", query("limit", "")},
		{"zero limit", query("limit", "0")},
		{"negative limit", query("limit", "-1")},
		{"leading-zero limit", query("limit", "01")},
		{"explicit sign", query("limit", "+1")},
		{"noninteger limit", query("limit", "1.0")},
		{"excess rank limit", query("limit", "101")},
		{"integer overflow", query("limit", strings.Repeat("9", 30))},
		{"repeated option", duplicate.Encode()},
		{"caller tenant", query("tenant_id", "foreign")},
		{"caller focus", query("focusLabel", "private")},
		{"caller selector", query("match[]", "{}")},
		{"unknown option", query("unknown", "value")},
		{"malformed escape", query("limit", "1") + "&metric=%zz"},
		{"raw semicolon", query("limit", "1") + ";metric=cpu"},
		{"raw input cap", strings.Repeat("x", maximumCardinalityQueryBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, selection, err := parseSeriesCardinalityQuery(tc.raw)
			if err == nil || err.Error() != "VALIDATION_FAILED" || workspace != "" || selection != (monitoring.SeriesCardinalityQuery{}) {
				t.Fatalf("invalid query released parsed state: %q %+v %v", workspace, selection, err)
			}
		})
	}
}

func TestSeriesCardinalityRemainsOutsidePublicDispatch(t *testing.T) {
	if Owns(SeriesCardinalityPath) {
		t.Fatal("candidate entered the public route set before contract publication")
	}
	handler := Handler{Service: &monitoring.Service{}}
	request := httptest.NewRequest(http.MethodGet, SeriesCardinalityPath+"?"+cardinalityOptions().Encode(), nil)
	body, status, err := handler.Handle(request, identity.Auth{}, "unselected")
	if err == nil || err.Error() != "RESOURCE_NOT_FOUND" || status != 0 || body != nil {
		t.Fatalf("default dispatcher admitted the private candidate: %d %v", status, err)
	}
	var portal identity.Auth
	portal.Me.Audience = "portal"
	privateBody, privateStatus, privateErr := handler.HandleSeriesCardinality(request, portal, "unselected")
	if privateErr == nil || privateErr.Error() != "ACCESS_DENIED" || privateStatus != 0 || privateBody != nil {
		t.Fatalf("private candidate bypassed the Admin boundary: %d %v", privateStatus, privateErr)
	}
}
