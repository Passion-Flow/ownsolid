package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestArtifactHandlerStrictScopeHeadersBodyAndZeroErrorProjection(t *testing.T) {
	h := &Handler{Service: &artifactstores.Service{}}
	var a identity.Auth
	a.Me.Audience = "portal"
	base := "/api/v1/workspaces/11111111-1111-4111-8111-111111111111/artifact-objects"
	for _, tc := range []struct{ method, path, body, media string }{
		{"GET", base + "?limit=2&limit=3", "", ""},
		{"GET", base + "?limit=2&endpoint=https://foreign.invalid", "", ""},
		{"GET", base + "?limit=2", "{}", "application/json"},
		{"GET", base + "/", "", ""},
		{"POST", base, `{"name":"first","name":"second"}`, "application/json"},
		{"PUT", base + "/22222222-2222-4222-8222-222222222222/parts/01", "x", "application/octet-stream"},
		{"GET", base + "/22222222-2222-4222-8222-222222222222/content?grant_id=bad", "", ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.media != "" {
				r.Header.Set("Content-Type", tc.media)
			}
			r.Header.Set("Idempotency-Key", "33333333-3333-4333-8333-333333333333")
			v, status, e := h.Handle(r, a, "44444444-4444-4444-8444-444444444444")
			if e == nil || v != nil || status != 0 {
				t.Fatal("invalid request projected", e, status)
			}
		})
	}
	r := httptest.NewRequest("GET", "/api/v1/admin/artifact-objects?workspace_id=11111111-1111-4111-8111-111111111111&limit=2", nil)
	if v, status, e := h.Handle(r, a, "44444444-4444-4444-8444-444444444444"); e == nil || v != nil || status != 0 {
		t.Fatal("portal crossed admin boundary")
	}
}

func TestCancelIngestionRejectsGrantAndBodyOverridesBeforeOwnerAdmission(t *testing.T) {
	h := &Handler{Service: &artifactstores.Service{}}
	var actor identity.Auth
	actor.Me.Audience = "portal"
	path := "/api/v1/workspaces/11111111-1111-4111-8111-111111111111/artifact-objects/22222222-2222-4222-8222-222222222222/ingestions/33333333-3333-4333-8333-333333333333/cancel"
	for _, tc := range []struct{ query, body, code string }{
		{"?grant_id=44444444-4444-4444-8444-444444444444", `{"reason":"Cancel unclaimed intent"}`, "VALIDATION_FAILED"},
		{"", `{"reason":"Cancel unclaimed intent","grant_id":"44444444-4444-4444-8444-444444444444"}`, "VALIDATION_FAILED"},
		{"", `{"reason":"First","reason":"Second"}`, "VALIDATION_FAILED"},
		{"", `{"reason":"Cancel unclaimed intent"}`, "OBJECT_NOT_ADMITTED"},
	} {
		r := httptest.NewRequest("POST", path+tc.query, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		value, status, e := h.Handle(r, actor, "55555555-5555-4555-8555-555555555555")
		if e == nil || e.Error() != tc.code || value != nil || status != 0 {
			t.Fatal("cancellation route/strict request/error projection changed", e, status)
		}
	}
}
