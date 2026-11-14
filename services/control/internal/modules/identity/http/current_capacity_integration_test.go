//go:build repository_integration

package http_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestM01CurrentHTTPCanonicalHashSaturation(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	policy.Rates.LoginEmailCount, policy.Rates.LoginIPCount = 100, 100
	if policy.Identity.HashWorkers != 2 || policy.Identity.HashQueueCapacity != 16 {
		t.Fatal("CANONICAL_CAPACITY_DEFAULT_CHANGED_REQUIRES_CONTRACT_REVIEW")
	}
	s, _, portal, _, password := setup(t, policy)
	var original string
	if s.DB.QueryRow(t.Context(), "SELECT c.password_phc FROM password_credentials c JOIN users u ON u.id=c.user_id WHERE u.email_canonical=$1", "admin@example.invalid").Scan(&original) != nil {
		t.Fatal("CANONICAL_CAPACITY_CREDENTIAL_FIXTURE_UNAVAILABLE")
	}
	parts := strings.Split(original, "$")
	if len(parts) != 6 || parts[3] != "m=19456,t=2,p=1" {
		t.Fatal("CANONICAL_CAPACITY_CREDENTIAL_FORMAT_CHANGED")
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		t.Fatal("CANONICAL_CAPACITY_SYNTHETIC_SALT_INVALID")
	}
	// A real valid bounded slow PHC is solely an isolated synthetic fixture.
	// No worker token, queue entry or production profiling hook is manufactured.
	digest := argon2.IDKey([]byte(norm.NFC.String(password)), salt, 10, 65536, 1, 32)
	parts[3], parts[5] = "m=65536,t=10,p=1", base64.RawStdEncoding.EncodeToString(digest)
	currentHTTPExec(t, s, "UPDATE password_credentials SET password_phc=$1 WHERE user_id=(SELECT id FROM users WHERE email_canonical=$2)", strings.Join(parts, "$"), "admin@example.invalid")
	portal.refresh()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineGoroutines := runtime.NumGoroutine()
	const initialRequests = 24
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	type result struct {
		response *http.Response
		err      error
		elapsed  time.Duration
	}
	ready, results := make(chan struct{}), make(chan result, initialRequests)
	// Establish each real verified TLS connection before starting the admission
	// observation window. Cold TLS scheduling is not the password queue: on the
	// bounded one-CPU race profile it can consume that entire two-second window.
	// Each client owns exactly one idle connection; no hash slot is manufactured.
	clients := make([]*http.Client, initialRequests)
	for i := range initialRequests {
		transport := portal.http.Transport.(*http.Transport).Clone()
		t.Cleanup(transport.CloseIdleConnections)
		clients[i] = &http.Client{Transport: transport}
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, portal.s.URL+"/health/live", nil)
		if e != nil {
			t.Fatal("CANONICAL_CAPACITY_WARM_REQUEST_FAILED")
		}
		request.Host = "portal.example.invalid"
		response, e := clients[i].Do(request)
		if e != nil {
			t.Fatal("CANONICAL_CAPACITY_VERIFIED_TLS_WARMUP_FAILED")
		}
		_, readError := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if readError != nil || response.StatusCode != http.StatusOK || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
			t.Fatal("CANONICAL_CAPACITY_VERIFIED_TLS_WARMUP_FAILED")
		}
	}
	var joined sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		joined.Wait()
		for {
			select {
			case result := <-results:
				if result.response != nil {
					_ = result.response.Body.Close()
				}
			default:
				return
			}
		}
	})
	for i := range initialRequests {
		r := retryRequest(t, portal, "POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": "Synthetic incorrect capacity passphrase"}).WithContext(ctx)
		joined.Add(1)
		go func() {
			defer joined.Done()
			<-ready
			started := time.Now()
			response, e := clients[i].Do(r)
			results <- result{response, e, time.Since(started)}
		}()
	}
	close(ready)
	observationDeadline := time.Now().Add(config.Milliseconds(policy.Identity.HashAdmissionMilliseconds))
	observed := false
	maximumDerivations, maximumWaiters, waitersAtTwoDerivations := 0, 0, 0
	var stack bytes.Buffer
	for time.Now().Before(observationDeadline) {
		stack.Reset()
		if pprof.Lookup("goroutine").WriteTo(&stack, 2) != nil || stack.Len() > 1<<20 {
			t.Fatal("CANONICAL_CAPACITY_STACK_OBSERVATION_FAILED")
		}
		derivations := bytes.Count(stack.Bytes(), []byte("golang.org/x/crypto/argon2.deriveKey("))
		waiters := bytes.Count(stack.Bytes(), []byte("security.(*Passwords).acquire("))
		maximumDerivations = max(maximumDerivations, derivations)
		maximumWaiters = max(maximumWaiters, waiters)
		if derivations == 2 {
			waitersAtTwoDerivations = max(waitersAtTwoDerivations, waiters)
		}
		if derivations == 2 && waiters == 16 {
			observed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	stack.Reset()
	if !observed {
		t.Fatalf("CANONICAL_CAPACITY_REAL_TWO_WORKERS_FULL_QUEUE_NOT_OBSERVED max_derivations=%d max_waiters=%d max_waiters_at_two_derivations=%d", maximumDerivations, maximumWaiters, waitersAtTwoDerivations)
	}
	var full runtime.MemStats
	runtime.ReadMemStats(&full)
	fullGoroutines := runtime.NumGoroutine()
	if full.HeapAlloc > before.HeapAlloc+256<<20 || fullGoroutines > baselineGoroutines+12*initialRequests+64 {
		t.Fatal("CANONICAL_CAPACITY_LOCAL_HEAP_OR_GOROUTINE_GUARD_EXCEEDED")
	}
	started := time.Now()
	r, e := portal.http.Do(retryRequest(t, portal, "POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}).WithContext(ctx))
	if e != nil {
		t.Fatal("CANONICAL_CAPACITY_OVERFLOW_REQUEST_FAILED")
	}
	overflowElapsed := time.Since(started)
	if r.StatusCode != 503 {
		_ = r.Body.Close()
		t.Fatalf("CANONICAL_CAPACITY_EXTRA_REQUEST_NOT_REJECTED status=%d elapsed_ms=%d", r.StatusCode, overflowElapsed.Milliseconds())
	}
	retryProblem(t, portal, r, 503, "AUTH_CAPACITY_EXCEEDED", "2")
	if overflowElapsed > config.Seconds(policy.HTTP.RequestSeconds) {
		t.Fatalf("CANONICAL_CAPACITY_HTTP_REQUEST_NOT_BOUNDED elapsed_ms=%d", overflowElapsed.Milliseconds())
	}
	busy, invalid, delayedBusy := 0, 0, 0
	responses := make([]*http.Response, 0, initialRequests)
	elapsedByResponse := make(map[*http.Response]time.Duration, initialRequests)
	defer func() {
		for _, r := range responses {
			_ = r.Body.Close()
		}
	}()
	failed := false
	for range initialRequests {
		select {
		case result := <-results:
			if result.err != nil {
				failed = true
			} else {
				responses = append(responses, result.response)
				elapsedByResponse[result.response] = result.elapsed
			}
		case <-ctx.Done():
			t.Fatal("CANONICAL_CAPACITY_REQUEST_JOIN_DEADLINE")
		}
	}
	joined.Wait()
	if failed {
		t.Fatal("CANONICAL_CAPACITY_JOINED_REQUEST_FAILED")
	}
	for _, r := range responses {
		switch r.StatusCode {
		case 503:
			busy++
			if elapsedByResponse[r] >= config.Milliseconds(policy.Identity.HashAdmissionMilliseconds) {
				delayedBusy++
			}
			retryProblem(t, portal, r, 503, "AUTH_CAPACITY_EXCEEDED", "2")
		case 401:
			invalid++
			retryProblem(t, portal, r, 401, "AUTH_INVALID_CREDENTIALS", "")
		default:
			r.Body.Close()
			t.Fatal("CANONICAL_CAPACITY_UNEXPECTED_RESPONSE", r.StatusCode)
		}
	}
	if busy == 0 || invalid == 0 || delayedBusy == 0 {
		t.Fatal("CANONICAL_CAPACITY_ADMISSION_TIMEOUT_OR_REAL_HASH_NOT_OBSERVED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions", 0)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL", 1)
	growth := uint64(0)
	if full.HeapAlloc > before.HeapAlloc {
		growth = full.HeapAlloc - before.HeapAlloc
	}
	t.Logf("CANONICAL_HASH_CAPACITY actual_workers=2 actual_waiters=16 requests=25 overflow_status=503 overflow_ms=%d initial_capacity_responses=%d delayed_capacity_responses=%d invalid_credentials=%d heap_growth_bytes=%d goroutine_growth=%d requests_joined=true sessions_before_recovery=0 explicit_correct_recovery=true", overflowElapsed.Milliseconds(), busy, delayedBusy, invalid, growth, fullGoroutines-baselineGoroutines)
}
