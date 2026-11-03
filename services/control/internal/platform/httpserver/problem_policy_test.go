package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// Explicit unit fixture for a producer's typed rate metadata.
type retryPolicyRateFixture int

func (retryPolicyRateFixture) Error() string       { return "RATE_LIMITED" }
func (v retryPolicyRateFixture) RetrySeconds() int { return int(v) }

func TestProblemRetryPolicy(t *testing.T) {
	custom := config.DefaultHTTPPolicy()
	custom.RateLimitFallbackRetrySeconds, custom.ServiceUnavailableRetrySeconds = 17, 7
	invalid := custom
	invalid.ServiceUnavailableRetrySeconds = 0
	for _, v := range []struct {
		name        string
		policy      config.HTTPPolicy
		err         error
		status      int
		retry, code string
	}{
		{"default_rate", config.DefaultHTTPPolicy(), errors.New("RATE_LIMITED"), 429, "900", "RATE_LIMITED"},
		{"default_service", config.DefaultHTTPPolicy(), errors.New("AUTH_CAPACITY_EXCEEDED"), 503, "2", "AUTH_CAPACITY_EXCEEDED"},
		{"custom_rate", custom, errors.New("RATE_LIMITED"), 429, "17", "RATE_LIMITED"},
		{"custom_service", custom, errors.New("AUTH_CAPACITY_EXCEEDED"), 503, "7", "AUTH_CAPACITY_EXCEEDED"},
		{"typed_actual_wait", custom, retryPolicyRateFixture(93), 429, "93", "RATE_LIMITED"},
		{"wrapped_actual_wait", custom, fmt.Errorf("%w", retryPolicyRateFixture(94)), 429, "94", "RATE_LIMITED"},
		{"zero_wait_fallback", custom, retryPolicyRateFixture(0), 429, "17", "RATE_LIMITED"},
		{"negative_wait_fallback", custom, retryPolicyRateFixture(-1), 429, "17", "RATE_LIMITED"},
		{"private_failure", custom, errors.New("private-password-dsn-value"), 503, "7", "STORAGE_UNAVAILABLE"},
		{"invalid_injected_policy", invalid, errors.New("RATE_LIMITED"), 503, "2", "SERVICE_NOT_READY"},
		{"no_retry_on_conflict", custom, errors.New("VERSION_CONFLICT"), 409, "", "VERSION_CONFLICT"},
		{"no_replay_on_unknown_commit", custom, errors.New("STORAGE_COMMIT_OUTCOME_UNKNOWN"), 409, "", "STORAGE_COMMIT_OUTCOME_UNKNOWN"},
	} {
		t.Run(v.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			w.Header().Set("Retry-After", "stale")
			ProblemWithPolicy(w, "synthetic-request", v.err, v.policy)
			var body map[string]any
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != v.status || w.Header().Get("Retry-After") != v.retry || body["code"] != v.code || body["request_id"] != "synthetic-request" || len(body) != 5 || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatal("problem status, retry advice or redacted wire contract changed")
			}
		})
	}
	w := httptest.NewRecorder()
	Problem(w, "compatibility", errors.New("SERVICE_NOT_READY"))
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
		t.Fatal("standalone compatibility did not use owned default")
	}
}
