package localization

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestUnavailableRegistryHTTPRetryPolicy(t *testing.T) {
	// A nil registry is an explicit unit failure fixture, never a success adapter.
	var registry *Registry
	policy := config.DefaultHTTPPolicy()
	policy.ServiceUnavailableRetrySeconds = 11
	for _, configured := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/localization/catalog", nil)
		want := "2"
		if configured {
			registry.ServeHTTPWithPolicy(w, r, policy)
			want = "11"
		} else {
			registry.ServeHTTP(w, r)
		}
		var body map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 503 || w.Header().Get("Retry-After") != want || body["code"] != "SERVICE_NOT_READY" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("registry failure did not retain serving policy and public contract")
		}
		if _, e := uuid.Parse(w.Header().Get("X-Request-ID")); e != nil || body["request_id"] != w.Header().Get("X-Request-ID") {
			t.Fatal("registry failure lost request correlation")
		}
	}
}
