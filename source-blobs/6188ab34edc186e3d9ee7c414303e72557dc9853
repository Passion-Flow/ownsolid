//go:build repository_integration

package http_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// Immutable synthetic acceptance profile, selected through --group workload.
// Deployment/resource settings remain in the existing typed native runner.
const (
	workloadRequests                   = 60
	workloadMembers                    = 5
	workloadBodyLimit                  = 64 << 10
	workloadHeapGrowthLimit            = 256 << 20
	workloadGoroutineGrowthLimit       = 128
	workloadStageDeadline              = 90 * time.Second
	workloadSampleInterval             = 200 * time.Millisecond
	workloadTimingQuantumUS      int64 = 1
)

type workloadResult struct {
	Index         int    `json:"index"`
	Operation     string `json:"operation"`
	Status        int    `json:"status"`
	ElapsedUS     int64  `json:"elapsed_us"`
	ResponseBytes int    `json:"response_bytes"`
	Class         string `json:"class"`
}
type workloadResource struct {
	CPUUS                int64  `json:"cpu_us"`
	RSSMaxBytes          int64  `json:"rss_max_bytes"`
	HeapBytes            uint64 `json:"heap_bytes"`
	Goroutines           int    `json:"goroutines"`
	PoolWaitUS           int64  `json:"pool_wait_us"`
	PoolEmptyAcquires    int64  `json:"pool_empty_acquires"`
	PoolCanceledAcquires int64  `json:"pool_canceled_acquires"`
	PoolAcquired         int32  `json:"pool_acquired"`
	PoolTotal            int32  `json:"pool_total"`
	PoolMax              int32  `json:"pool_max"`
}
type workloadQuantiles struct {
	Count int   `json:"count"`
	P50US int64 `json:"p50_us"`
	P95US int64 `json:"p95_us"`
	P99US int64 `json:"p99_us"`
}
type workloadVolume struct {
	Users       int   `json:"users"`
	Workspaces  int   `json:"workspaces"`
	Credentials int   `json:"credentials"`
	Memberships int   `json:"memberships"`
	Sessions    int   `json:"sessions"`
	SchemaBytes int64 `json:"schema_bytes"`
}

// Fixed read-only host facts; no configurable shell, inherited environment,
// user profile, credentials or external endpoint is consulted.
func workloadHost(t *testing.T) (string, uint64) {
	t.Helper()
	var model string
	var memory uint64
	switch runtime.GOOS {
	case "darwin":
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "machdep.cpu.brand_string", "hw.memsize")
		command.Env = []string{}
		raw, e := command.Output()
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if e != nil || len(raw) > 1024 || len(lines) != 2 {
			t.Fatal("WORKLOAD_EXACT_HOST_FACTS_UNAVAILABLE")
		}
		model = strings.TrimSpace(lines[0])
		memory, e = strconv.ParseUint(strings.TrimSpace(lines[1]), 10, 64)
		if e != nil {
			t.Fatal("WORKLOAD_EXACT_HOST_RAM_UNAVAILABLE")
		}
	case "linux":
		read := func(path string) string {
			file, e := os.Open(path)
			if e != nil {
				t.Fatal("WORKLOAD_EXACT_HOST_FACTS_UNAVAILABLE")
			}
			defer file.Close()
			raw, e := io.ReadAll(io.LimitReader(file, 1<<20+1))
			if e != nil || len(raw) > 1<<20 {
				t.Fatal("WORKLOAD_EXACT_HOST_FACTS_UNAVAILABLE")
			}
			return string(raw)
		}
		for _, line := range strings.Split(read("/proc/cpuinfo"), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && (strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware") {
				model = strings.TrimSpace(value)
				break
			}
		}
		for _, line := range strings.Split(read("/proc/meminfo"), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
				kib, e := strconv.ParseUint(fields[1], 10, 64)
				if e != nil || kib > 1<<50 {
					t.Fatal("WORKLOAD_EXACT_HOST_RAM_UNAVAILABLE")
				}
				memory = kib * 1024
				break
			}
		}
	default:
		t.Fatal("WORKLOAD_EXACT_HOST_PROFILE_UNSUPPORTED")
	}
	if model == "" || len(model) > 256 || strings.ContainsAny(model, "\r\n") || memory == 0 {
		t.Fatal("WORKLOAD_EXACT_HOST_FACTS_UNAVAILABLE")
	}
	return model, memory
}

func workloadLog(t *testing.T, value any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal("WORKLOAD_SAFE_EVENT_ENCODING_FAILED")
	}
	t.Log("M01_WORKLOAD_JSON=" + string(raw))
}
func workloadResources(t *testing.T, s *identity.Service) workloadResource {
	t.Helper()
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		t.Fatal("WORKLOAD_SELF_RESOURCE_READ_FAILED")
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	rss := usage.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	pool := s.DB.Stat()
	return workloadResource{
		CPUUS:       usage.Utime.Sec*1000000 + int64(usage.Utime.Usec) + usage.Stime.Sec*1000000 + int64(usage.Stime.Usec),
		RSSMaxBytes: rss, HeapBytes: mem.HeapAlloc, Goroutines: runtime.NumGoroutine(),
		PoolWaitUS: pool.EmptyAcquireWaitTime().Microseconds(), PoolEmptyAcquires: pool.EmptyAcquireCount(),
		PoolCanceledAcquires: pool.CanceledAcquireCount(), PoolAcquired: pool.AcquiredConns(), PoolTotal: pool.TotalConns(), PoolMax: pool.MaxConns(),
	}
}
func workloadData(t *testing.T, s *identity.Service) workloadVolume {
	t.Helper()
	var v workloadVolume
	e := s.DB.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM workspaces),
 (SELECT count(*) FROM password_credentials), (SELECT count(*) FROM workspace_memberships),
 (SELECT count(*) FROM sessions),
 (SELECT COALESCE(sum(pg_total_relation_size(c.oid)),0)::bigint FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relkind='r')`).Scan(
		&v.Users, &v.Workspaces, &v.Credentials, &v.Memberships, &v.Sessions, &v.SchemaBytes)
	if e != nil {
		t.Fatal("WORKLOAD_PRIVATE_VOLUME_READ_FAILED")
	}
	return v
}
func workloadPercentiles(values []int64) workloadQuantiles {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	at := func(p int) int64 { return values[(len(values)*p+99)/100-1] }
	return workloadQuantiles{len(values), at(50), at(95), at(99)}
}

// Receives only frozen per-request cookies/CSRF. It never changes a shared jar,
// logs response bodies, retries a request, or calls testing.Fatal in a worker.
func workloadRequest(ctx context.Context, c *client, request *http.Request, index int, operation, userID string, users map[string]bool, retrySeconds int) workloadResult {
	result := workloadResult{Index: index, Operation: operation, Class: "transport_error"}
	started := time.Now()
	response, e := c.http.Do(request.WithContext(ctx))
	if e != nil {
		result.ElapsedUS = time.Since(started).Microseconds()
		return result
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, workloadBodyLimit+1))
	result.ElapsedUS = time.Since(started).Microseconds()
	result.Status, result.ResponseBytes = response.StatusCode, len(raw)
	result.Class = "response_contract_error"
	if e != nil || len(raw) > workloadBodyLimit || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.Header.Get("Cache-Control") != "no-store" {
		return result
	}
	if _, e = uuid.Parse(response.Header.Get("X-Request-ID")); e != nil {
		return result
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return result
	}
	if response.StatusCode != http.StatusOK {
		result.Class = "unexpected_http_error"
		if c.schemas["Problem"].Validate(body) != nil || body["request_id"] != response.Header.Get("X-Request-ID") || response.Header.Get("Content-Type") != "application/problem+json" || len(response.Cookies()) != 0 {
			result.Class = "response_contract_error"
			return result
		}
		if operation == "login" && response.StatusCode == 503 && body["code"] == "AUTH_CAPACITY_EXCEEDED" && response.Header.Get("Retry-After") == strconv.Itoa(retrySeconds) {
			result.Class = "capacity_rejected"
		}
		if response.StatusCode == 429 && body["code"] == "RATE_LIMITED" {
			result.Class = "rate_rejected"
		}
		return result
	}
	if !workloadSuccessData(c, operation, userID, users, body) {
		return result
	}
	if operation == "login" {
		found := false
		for _, cookie := range response.Cookies() {
			if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
				return result
			}
			if cookie.Name == "__Host-portal_session" && cookie.Value != "" && cookie.MaxAge >= 0 {
				found = true
			}
		}
		if !found {
			return result
		}
	}
	result.Class = "success"
	return result
}

// Validate the real success envelope before interpreting its data projection.
// Problem responses deliberately keep their existing top-level error shape.
func workloadSuccessData(c *client, operation, userID string, users map[string]bool, body map[string]any) bool {
	schema := map[string]string{"login": "LoginResponse", "getMe": "MeResponse", "listUsers": "UserListResponse"}[operation]
	if schema == "" || c.schemas[schema].Validate(body) != nil {
		return false
	}
	data, ok := body["data"].(map[string]any)
	if !ok || data == nil {
		return false
	}
	switch operation {
	case "login", "getMe":
		user, ok := data["user"].(map[string]any)
		return ok && user["id"] == userID && data["audience"] == c.audience
	case "listUsers":
		items, ok := data["items"].([]any)
		if !ok || len(items) != len(users) {
			return false
		}
		seen := map[string]bool{}
		for _, item := range items {
			user, ok := item.(map[string]any)
			if !ok {
				return false
			}
			id, ok := user["id"].(string)
			if !ok || !users[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	return false
}

// Pure wire-contract regression. These invented JSON bodies test the same parser
// as workloadRequest; they never replace responses in the real HTTP workload.
func TestM01WorkloadSuccessWireContract(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const stamp = "2026-10-24T00:00:00Z"
	user := map[string]any{"id": id, "email": "wire@example.invalid", "display_name": "Synthetic wire user", "locale": "en", "status": "active", "is_platform_admin": false, "email_verified_at": nil, "version": 1, "created_at": stamp, "updated_at": stamp}
	c := &client{audience: "portal", schemas: schemas(t)}
	users := map[string]bool{id: true}
	for _, operation := range []string{"login", "getMe", "listUsers"} {
		t.Run(operation, func(t *testing.T) {
			data := map[string]any{"user": user, "workspace": nil, "audience": "portal"}
			if operation == "login" {
				data["session"] = map[string]any{"absolute_expires_at": stamp, "idle_timeout_seconds": 1800}
			}
			if operation == "listUsers" {
				data = map[string]any{"items": []any{user}, "next_cursor": nil}
			}
			decode := func(value any) map[string]any {
				raw, e := json.Marshal(value)
				if e != nil {
					t.Fatal(e)
				}
				var wire map[string]any
				if json.Unmarshal(raw, &wire) != nil {
					t.Fatal("WORKLOAD_WIRE_FIXTURE_INVALID")
				}
				return wire
			}
			if !workloadSuccessData(c, operation, id, users, decode(map[string]any{"data": data})) {
				t.Fatal("WORKLOAD_VALID_SUCCESS_ENVELOPE_REJECTED")
			}
			for _, invalid := range []any{data, map[string]any{"data": nil}, map[string]any{"data": []any{data}}} {
				if workloadSuccessData(c, operation, id, users, decode(invalid)) {
					t.Fatal("WORKLOAD_MISSING_OR_INVALID_ENVELOPE_ACCEPTED")
				}
			}
			wrong := decode(map[string]any{"data": data})
			if operation == "listUsers" {
				wrong["data"].(map[string]any)["items"] = []any{}
			} else {
				wrong["data"].(map[string]any)["user"].(map[string]any)["id"] = "00000000-0000-4000-8000-000000000002"
			}
			if workloadSuccessData(c, operation, id, users, wrong) {
				t.Fatal("WORKLOAD_INCORRECT_AUTHORIZED_DATA_ACCEPTED")
			}
		})
	}
}

func TestM01CurrentHTTPResourceBaseline(t *testing.T) {
	cpuModel, hostMemory := workloadHost(t)
	policy := config.DefaultRuntimePolicy()
	// A declared synthetic rate profile isolates hash/SQL admission. Authentication,
	// CSRF, TLS, canonical password cost and hash worker/queue limits stay intact.
	policy.Rates.LoginEmailCount, policy.Rates.LoginIPCount = 1000, 1000
	if policy.Identity.HashWorkers != 2 || policy.Identity.HashQueueCapacity != 16 {
		t.Fatal("WORKLOAD_CANONICAL_HASH_PROFILE_CHANGED")
	}
	s, server, portal, admin, password := setup(t, policy)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	adminID := admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)["user"].(map[string]any)["id"].(string)
	users := map[string]bool{adminID: true}
	memberID, memberPassword := "", ""
	for i := 0; i < workloadMembers; i++ {
		id, p := currentHTTPMember(t, s, portal, admin, fmt.Sprintf("workload-member-%d@example.invalid", i))
		users[id] = true
		if i == 0 {
			memberID, memberPassword = id, p
		}
	}
	currentHTTPLogin(portal, "workload-member-0@example.invalid", memberPassword)
	login := newClient(t, server, "portal", portal.schemas)
	login.refresh()
	for _, c := range []*client{login, portal, admin} {
		c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	var pgVersion string
	if s.DB.QueryRow(t.Context(), "SHOW server_version").Scan(&pgVersion) != nil {
		t.Fatal("WORKLOAD_DATABASE_VERSION_UNAVAILABLE")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM password_credentials WHERE password_phc LIKE '$argon2id$v=19$m=19456,t=2,p=1$%'", workloadMembers+1)
	volume := workloadData(t, s)
	if volume.Users != 6 || volume.Workspaces != 1 || volume.Credentials != 6 || volume.Memberships != 6 || volume.Sessions != 2 || volume.SchemaBytes <= 0 {
		t.Fatal("WORKLOAD_EXACT_SYNTHETIC_DATA_REQUIRED")
	}
	rawPolicy, _ := json.Marshal(policy)
	digest := sha256.Sum256(rawPolicy)
	workloadLog(t, map[string]any{"kind": "manifest", "format": "projectone.m01.workload.v1", "profile": "synthetic-six-account-tls-pg", "goos": runtime.GOOS, "goarch": runtime.GOARCH, "go_version": runtime.Version(), "logical_cpus": runtime.NumCPU(), "cpu_model": cpuModel, "host_memory_bytes": hostMemory, "gomaxprocs": runtime.GOMAXPROCS(0), "postgresql_version": pgVersion, "requests_per_stage": workloadRequests, "concurrency": []int{1, 10, 50}, "operations": []string{"login", "getMe", "listUsers"}, "runtime_policy_sha256": hex.EncodeToString(digest[:]), "hash_workers": policy.Identity.HashWorkers, "hash_queue": policy.Identity.HashQueueCapacity, "login_rate_email": policy.Rates.LoginEmailCount, "login_rate_ip": policy.Rates.LoginIPCount, "hash_phc_parameters": "m=19456,t=2,p=1", "request_seconds": policy.HTTP.RequestSeconds, "stage_deadline_seconds": int(workloadStageDeadline / time.Second), "resource_scope": "same-test-process-client-and-real-server; host-and-postgres-in-outer-receipt", "volume": volume})
	runtime.GC()
	baseline := workloadResources(t, s)
	workloadLog(t, map[string]any{"kind": "baseline", "resource": baseline})
	totalLoginSuccess := 0
	for _, concurrency := range []int{1, 10, 50} {
		resourceStarted := time.Now()
		before := workloadResources(t, s)
		ctx, cancel := context.WithTimeout(t.Context(), workloadStageDeadline)
		type job struct {
			index             int
			operation, userID string
			client            *client
			request           *http.Request
		}
		jobs := make(chan job, workloadRequests)
		results := make(chan workloadResult, workloadRequests)
		for i := 0; i < workloadRequests; i++ {
			switch i % 3 {
			case 0:
				jobs <- job{i, "login", memberID, login, retryRequest(t, login, "POST", "/api/v1/auth/login", map[string]any{"email": "workload-member-0@example.invalid", "password": memberPassword})}
			case 1:
				jobs <- job{i, "getMe", memberID, portal, retryRequest(t, portal, "GET", "/api/v1/me", nil)}
			case 2:
				jobs <- job{i, "listUsers", adminID, admin, retryRequest(t, admin, "GET", "/api/v1/admin/users?limit=10", nil)}
			}
		}
		close(jobs)
		var joined sync.WaitGroup
		ready := make(chan struct{})
		for range concurrency {
			joined.Add(1)
			go func() {
				defer joined.Done()
				<-ready
				for j := range jobs {
					requestContext, requestCancel := context.WithTimeout(ctx, config.Seconds(policy.HTTP.RequestSeconds))
					result := workloadRequest(requestContext, j.client, j.request, j.index, j.operation, j.userID, users, policy.HTTP.ServiceUnavailableRetrySeconds)
					requestCancel()
					results <- result
				}
			}()
		}
		// Registered before releasing workers: failure cancels and joins every request.
		t.Cleanup(func() { cancel(); joined.Wait() })
		started := time.Now()
		close(ready)
		ticker := time.NewTicker(workloadSampleInterval)
		defer ticker.Stop()
		observed := make([]workloadResult, 0, workloadRequests)
		samples := 0
		var lastSampleUS int64
		for len(observed) < workloadRequests {
			select {
			case result := <-results:
				observed = append(observed, result)
				workloadLog(t, map[string]any{"kind": "request", "concurrency": concurrency, "result": result})
			case <-ticker.C:
				samples++
				lastSampleUS = time.Since(started).Microseconds()
				workloadLog(t, map[string]any{"kind": "sample", "concurrency": concurrency, "elapsed_us": lastSampleUS, "resource": workloadResources(t, s)})
			}
		}
		joined.Wait()
		cancel()
		ticker.Stop()
		duration := time.Since(started).Microseconds()
		login.http.CloseIdleConnections()
		portal.http.CloseIdleConnections()
		admin.http.CloseIdleConnections()
		runtime.GC()
		after := workloadResources(t, s)
		resourceWindow := time.Since(resourceStarted).Microseconds()
		values := map[string][]int64{"all": {}, "login": {}, "getMe": {}, "listUsers": {}}
		errors, unexpected, loginSuccess := 0, 0, 0
		var totalElapsedUS int64
		classes := map[string]int{}
		for _, result := range observed {
			totalElapsedUS += result.ElapsedUS
			values["all"] = append(values["all"], result.ElapsedUS)
			values[result.Operation] = append(values[result.Operation], result.ElapsedUS)
			classes[result.Class]++
			if result.Class != "success" {
				errors++
			}
			if result.Class != "success" && (concurrency == 1 || result.Class != "capacity_rejected") {
				unexpected++
			}
			if result.Operation == "login" && result.Class == "success" {
				loginSuccess++
			}
		}
		quantiles := map[string]workloadQuantiles{}
		for operation, latencies := range values {
			quantiles[operation] = workloadPercentiles(latencies)
		}
		totalLoginSuccess += loginSuccess
		currentVolume := workloadData(t, s)
		workloadLog(t, map[string]any{"kind": "stage", "concurrency": concurrency, "requests": len(observed), "duration_us": duration, "samples": samples, "errors": errors, "error_rate": float64(errors) / workloadRequests, "unexpected_errors": unexpected, "classes": classes, "quantiles": quantiles, "before": before, "after": after, "cpu_delta_us": after.CPUUS - before.CPUUS, "resource_window_us": resourceWindow, "pool_wait_delta_us": after.PoolWaitUS - before.PoolWaitUS, "pool_empty_acquires_delta": after.PoolEmptyAcquires - before.PoolEmptyAcquires, "requests_joined": true, "volume": currentVolume})
		if lastSampleUS > duration || totalElapsedUS > int64(concurrency)*(duration+workloadTimingQuantumUS) {
			t.Fatal("WORKLOAD_REQUEST_OR_SAMPLE_TIME_OUTSIDE_JOINED_WINDOW")
		}
		if unexpected != 0 || after.HeapBytes > baseline.HeapBytes+workloadHeapGrowthLimit || after.Goroutines > baseline.Goroutines+workloadGoroutineGrowthLimit || after.PoolTotal > after.PoolMax {
			t.Fatal("WORKLOAD_BOUNDED_PROFILE_FAILED_SEE_REDACTED_EVENTS")
		}
		if currentVolume.Users != volume.Users || currentVolume.Credentials != volume.Credentials || currentVolume.Memberships != volume.Memberships || currentVolume.Workspaces != volume.Workspaces || currentVolume.Sessions != volume.Sessions+totalLoginSuccess {
			t.Fatal("WORKLOAD_ACTUAL_SESSION_OR_DATA_POSTIMAGE_MISMATCH")
		}
	}
	workloadLog(t, map[string]any{"kind": "complete", "stages": 3, "requests": 3 * workloadRequests, "login_successes": totalLoginSuccess, "requests_joined": true, "volume": workloadData(t, s), "claim": "bounded-local-profile-only; no-long-duration-leak-or-target-capacity-claim"})
}
