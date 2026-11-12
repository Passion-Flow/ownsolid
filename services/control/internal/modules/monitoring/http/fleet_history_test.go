package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func TestFleetHistoryRejectsAmbiguousInput(t *testing.T) {
	h := &Handler{Service: &monitoring.Service{}}
	admin := identity.Auth{}
	admin.Me.Audience, admin.Me.User.IsPlatformAdmin = "admin", true
	const values = "?workspace_id=58b3eedb-387e-48f8-853f-aa835bbf1892&from=2026-11-09T01:00:00Z&to=2026-11-09T02:00:00Z"
	if !Owns(FleetHistoryPath) || !Owns(Path) {
		t.Fatal("monitoring routes are not registered")
	}
	for _, path := range []string{FleetHistoryPath + "/", FleetHistoryPath + "/query", Path + "-history"} {
		if Owns(path) {
			t.Fatalf("route alias %q is registered", path)
		}
	}
	for _, item := range []struct{ method, target, code string }{
		{http.MethodGet, Path + values, "RESOURCE_NOT_FOUND"},
		{http.MethodHead, FleetHistoryPath + values, "RESOURCE_NOT_FOUND"},
		{http.MethodGet, FleetHistoryPath, "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + values + "&from=2026-11-09T01:00:00Z", "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + values + "&tier=raw", "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + strings.Replace(values, "to=", "selector=", 1), "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + strings.Replace(values, "01:00:00Z", "01:00:00.0Z", 1), "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + strings.Replace(values, "01:00:00Z", "01:00:00%2B00:00", 1), "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + strings.Replace(values, "02:00:00Z", "01:00:00Z", 1), "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + strings.Replace(values, "02:00:00Z", "%zz", 1), "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + values + "&" + strings.Repeat("x", 512), "VALIDATION_FAILED"},
		{http.MethodGet, FleetHistoryPath + values, "MONITORING_OWNER_REQUIRED"},
	} {
		r := httptest.NewRequest(item.method, "https://admin.example.invalid"+item.target, nil)
		body, status, err := h.HandleFleetHistory(r, admin, "fixture")
		if body != nil || status != 0 || err == nil || err.Error() != item.code {
			t.Fatalf("candidate request got %s, %d, %v, want %s", body, status, err, item.code)
		}
		if r.URL.Path == FleetHistoryPath {
			dispatched, status, err := h.Handle(r, admin, "fixture")
			if dispatched != nil || status != 0 || err == nil || err.Error() != item.code {
				t.Fatalf("dispatch returned a body or changed refusal: %v, %d, %v", dispatched, status, err)
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, FleetHistoryPath+values, strings.NewReader("unexpected"))
	if body, _, err := h.HandleFleetHistory(r, admin, "fixture"); body != nil || err == nil || err.Error() != "VALIDATION_FAILED" {
		t.Fatalf("history GET body reached the service: %v", err)
	}
}

func TestFleetHistoryPublicProblemCodes(t *testing.T) {
	for _, tc := range []struct {
		input, code string
		status      int
	}{
		{"VALIDATION_FAILED", "VALIDATION_FAILED", 400},
		{"MONITORING_HISTORY_RANGE_INVALID", "MONITORING_HISTORY_RANGE_INVALID", 400},
		{"AUTH_SESSION_EXPIRED", "AUTH_SESSION_EXPIRED", 401},
		{"ACCESS_DENIED", "ACCESS_DENIED", 403},
		{"MONITORING_HISTORY_RESPONSE_LIMIT", "MONITORING_HISTORY_RESPONSE_LIMIT", 422},
		{"MONITORING_BUSY", "MONITORING_BUSY", 429},
		{"MONITORING_HISTORY_NOT_CONFIGURED", "MONITORING_HISTORY_NOT_CONFIGURED", 503},
		{"MONITORING_LONG_TIER_NOT_CONFIGURED", "MONITORING_LONG_TIER_NOT_CONFIGURED", 503},
		{"MONITORING_RAW_TIER_UNAVAILABLE", "MONITORING_RAW_TIER_UNAVAILABLE", 503},
		{"MONITORING_LONG_TIER_UNAVAILABLE", "MONITORING_LONG_TIER_UNAVAILABLE", 503},
		{"untrusted native transport text", "STORAGE_UNAVAILABLE", 503},
	} {
		code, status := httpserver.PublicProblem(errors.New(tc.input))
		if code != tc.code || status != tc.status {
			t.Errorf("%s got %s %d, want %s %d", tc.input, code, status, tc.code, tc.status)
		}
	}
}
