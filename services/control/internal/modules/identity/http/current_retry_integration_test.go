//go:build repository_integration

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func retryPolicyFromFiles(t *testing.T, override string) config.RuntimePolicy {
	t.Helper()
	dir := t.TempDir()
	raw, e := os.ReadFile("../../../../config/examples/control/control.example.yaml")
	if e != nil {
		t.Fatal("RETRY_POLICY_EXAMPLE_UNAVAILABLE")
	}
	base, policyFile := filepath.Join(dir, "control.yaml"), filepath.Join(dir, "http.env")
	if os.WriteFile(base, raw, 0600) != nil || os.WriteFile(policyFile, []byte(override), 0600) != nil {
		t.Fatal("RETRY_POLICY_INPUT_WRITE_FAILED")
	}
	c, e := config.LoadWithEnvironment(base, policyFile)
	if e != nil {
		t.Fatal("RETRY_POLICY_ACTUAL_LOAD_FAILED", e)
	}
	return c.Policy()
}

func retryRequest(t *testing.T, c *client, method, path string, body any) *http.Request {
	t.Helper()
	var raw []byte
	var e error
	if body != nil {
		raw, e = json.Marshal(body)
	}
	if e != nil {
		t.Fatal("RETRY_REQUEST_ENCODE_FAILED")
	}
	r, e := http.NewRequestWithContext(t.Context(), method, c.s.URL+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal("RETRY_REQUEST_BUILD_FAILED")
	}
	r.Host = c.audience + ".example.invalid"
	r.Header.Set("Origin", "https://"+r.Host)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", c.csrf)
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	return r
}

func retryProblem(t *testing.T, c *client, r *http.Response, want int, code, retry string) {
	t.Helper()
	defer r.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
	var body map[string]any
	if e != nil || len(raw) > 65536 || json.Unmarshal(raw, &body) != nil || c.schemas["Problem"].Validate(body) != nil {
		t.Fatal("RETRY_PROBLEM_WIRE_INVALID")
	}
	if r.StatusCode != want || body["code"] != code || r.Header.Get("Retry-After") != retry || body["request_id"] != r.Header.Get("X-Request-ID") || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatal("RETRY_PROBLEM_STATUS_ADVICE_OR_CORRELATION_INVALID", r.StatusCode, body["code"])
	}
	if _, e := uuid.Parse(r.Header.Get("X-Request-ID")); e != nil || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		t.Fatal("RETRY_PROBLEM_VERIFIED_TLS_AND_REQUEST_ID_REQUIRED")
	}
	if len(r.Cookies()) != 0 {
		t.Fatal("FAILED_ADMISSION_CHANGED_AUTHORITY_COOKIE")
	}
}

func TestM01CurrentHTTPActualRateRetryRecovery(t *testing.T) {
	policy := retryPolicyFromFiles(t, "PROJECT_ONE_HTTP_RATE_LIMIT_FALLBACK_RETRY_SECONDS=17\nPROJECT_ONE_HTTP_SERVICE_UNAVAILABLE_RETRY_SECONDS=7\n")
	// A declared two-second real SQL window bounds the local expiry/recovery test.
	policy.Rates.LoginEmailCount, policy.Rates.LoginEmailWindowSeconds = 1, 2
	s, _, portal, _, password := setup(t, policy)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": "Synthetic wrong password"}, 401, "Problem")
	portal.refresh()
	r, e := portal.http.Do(retryRequest(t, portal, "POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}))
	if e != nil {
		t.Fatal("REAL_RATE_REQUEST_FAILED")
	}
	wait, e := strconv.Atoi(r.Header.Get("Retry-After"))
	if e != nil || wait < 1 || wait > 2 {
		r.Body.Close()
		t.Fatal("ACTUAL_RATE_WAIT_NOT_PRESERVED")
	}
	retryProblem(t, portal, r, 429, "RATE_LIMITED", strconv.Itoa(wait))
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions", 0)
	var count int
	var expires time.Time
	key := security.MAC(s.HMACKey, "m01-rate", "login.email", "admin@example.invalid")
	if s.DB.QueryRow(t.Context(), "SELECT attempt_count,expires_at FROM rate_limit_buckets WHERE bucket_key=$1", key).Scan(&count, &expires) != nil || count != 1 {
		t.Fatal("REAL_THROTTLE_DID_NOT_RETAIN_SQL_ADMISSION")
	}
	// Wait for actual database window expiry; no bucket edits or automatic replay.
	timer := time.NewTimer(time.Duration(wait)*time.Second + 50*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("RATE_RECOVERY_DEADLINE")
	}
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL", 1)
	t.Logf("RETRY_RATE_ACTUAL_SQL window_seconds=2 advice_seconds=%d rejected_without_session=true explicit_expiry_recovery=true", wait)
}

func TestM01CurrentHTTPActualHashCapacityRetry(t *testing.T) {
	policy := retryPolicyFromFiles(t, "PROJECT_ONE_HTTP_SERVICE_UNAVAILABLE_RETRY_SECONDS=9\n")
	policy.Identity.HashWorkers, policy.Identity.HashQueueCapacity = 1, 0
	s, _, portal, _, password := setup(t, policy)
	portal.refresh()
	const requests = 4
	ready, results := make(chan struct{}), make(chan struct {
		response *http.Response
		err      error
	}, requests)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var joined sync.WaitGroup
	for range requests {
		r := retryRequest(t, portal, "POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": "Synthetic wrong password"}).WithContext(ctx)
		joined.Add(1)
		go func() {
			defer joined.Done()
			<-ready
			response, e := portal.http.Do(r)
			results <- struct {
				response *http.Response
				err      error
			}{response, e}
		}()
	}
	close(ready)
	busy, completed := 0, 0
	// Drain and join every request before asserting, including failure outcomes.
	responses := make([]*http.Response, 0, requests)
	failed := false
	for range requests {
		result := <-results
		if result.err != nil {
			failed = true
		} else {
			responses = append(responses, result.response)
		}
	}
	joined.Wait()
	defer func() {
		for _, r := range responses {
			_ = r.Body.Close()
		}
	}()
	if failed {
		for _, r := range responses {
			r.Body.Close()
		}
		t.Fatal("HASH_CAPACITY_REQUEST_FAILED")
	}
	for _, r := range responses {
		switch r.StatusCode {
		case 503:
			busy++
			retryProblem(t, portal, r, 503, "AUTH_CAPACITY_EXCEEDED", "9")
		case 401:
			completed++
			retryProblem(t, portal, r, 401, "AUTH_INVALID_CREDENTIALS", "")
		default:
			r.Body.Close()
			t.Fatal("HASH_CAPACITY_UNEXPECTED_RESPONSE", r.StatusCode)
		}
	}
	if busy == 0 || completed == 0 {
		t.Fatal("ACTUAL_HASH_CAPACITY_OVERLAP_NOT_OBSERVED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions", 0)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL", 1)
	t.Logf("RETRY_HASH_ACTUAL_TLS requests=%d workers=1 queue=0 busy=%d completed=%d joined=true explicit_recovery=true", requests, busy, completed)
}

func TestM01CurrentHTTPConfiguredStorageRetryRecovery(t *testing.T) {
	policy := retryPolicyFromFiles(t, "PROJECT_ONE_HTTP_SERVICE_UNAVAILABLE_RETRY_SECONDS=7\n")
	s, _, portal, _, password := setup(t, policy)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	portal.refresh()
	cookie := portal.cookies["__Host-portal_session"].Value
	// A real separately closed pool is the isolated outage fixture. Preserve the
	// live worker/cleanup pool and restore it before a recovery request.
	closed, e := pgxpool.NewWithConfig(t.Context(), s.DB.Config().Copy())
	if e != nil {
		t.Fatal("RETRY_OUTAGE_POOL_SETUP_FAILED")
	}
	closed.Close()
	original := s.DB
	s.DB = closed
	t.Cleanup(func() { s.DB = original })
	r, e := portal.http.Do(retryRequest(t, portal, "POST", "/api/v1/auth/logout", nil))
	if e != nil {
		t.Fatal("RETRY_OUTAGE_REQUEST_FAILED")
	}
	retryProblem(t, portal, r, 503, "STORAGE_UNAVAILABLE", "7")
	if portal.cookies["__Host-portal_session"].Value != cookie {
		t.Fatal("UNCERTAIN_LOGOUT_CLEARED_COOKIE")
	}
	s.DB = original
	portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	portal.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL", 0)
	t.Log("RETRY_STORAGE_ACTUAL_TLS configured_advice=7 cookie_retained=true pool_restored=true explicit_logout_confirmed=true")
}

func TestM01CurrentHTTPDefaultStorageRetry(t *testing.T) {
	policy := retryPolicyFromFiles(t, "# No overrides; retain the owned HTTP defaults.\n")
	if policy.HTTP != config.DefaultHTTPPolicy() {
		t.Fatal("DEFAULT_HTTP_INPUT_DRIFT")
	}
	s, _, portal, _, _ := setup(t, policy)
	portal.refresh()
	closed, e := pgxpool.NewWithConfig(t.Context(), s.DB.Config().Copy())
	if e != nil {
		t.Fatal("DEFAULT_RETRY_OUTAGE_POOL_SETUP_FAILED")
	}
	closed.Close()
	original := s.DB
	s.DB = closed
	t.Cleanup(func() { s.DB = original })
	r, e := portal.http.Do(retryRequest(t, portal, "POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": "Synthetic wrong password"}))
	if e != nil {
		t.Fatal("DEFAULT_RETRY_OUTAGE_REQUEST_FAILED")
	}
	retryProblem(t, portal, r, 503, "STORAGE_UNAVAILABLE", "2")
	s.DB = original
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions", 0)
	t.Log("RETRY_STORAGE_ACTUAL_TLS default_advice=2 explicit_protected_defaults_loaded=true no_session=true")
}
