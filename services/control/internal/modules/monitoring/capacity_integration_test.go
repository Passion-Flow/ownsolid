//go:build repository_integration && monitoring_integration && monitoring_capacity

package monitoring_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type workloadStage struct {
	Concurrency int `json:"concurrency"`
	Requests    int `json:"requests"`
}
type workloadConfig struct {
	FormatVersion     int             `json:"format_version"`
	DeadlineSeconds   int             `json:"deadline_seconds"`
	RequestSeconds    int             `json:"request_seconds"`
	FaultMilliseconds int             `json:"fault_milliseconds"`
	MaximumBodyBytes  int64           `json:"maximum_body_bytes"`
	MaximumErrors     int             `json:"maximum_errors"`
	StopOnError       bool            `json:"stop_on_error"`
	Stages            []workloadStage `json:"stages"`
}

func (c workloadConfig) validate() error {
	total := 0
	if c.FormatVersion != 1 || c.DeadlineSeconds < 1 || c.DeadlineSeconds > 60 || c.RequestSeconds < 1 || c.RequestSeconds > c.DeadlineSeconds || c.FaultMilliseconds < 10 || c.FaultMilliseconds > 5000 || c.FaultMilliseconds >= c.RequestSeconds*1000 || c.MaximumBodyBytes < 4096 || c.MaximumBodyBytes > 4<<20 || c.MaximumErrors < 0 || c.MaximumErrors > 100 || len(c.Stages) < 1 || len(c.Stages) > 4 {
		return errors.New("MONITORING_WORKLOAD_INVALID")
	}
	for _, s := range c.Stages {
		if s.Concurrency < 1 || s.Concurrency > 8 || s.Requests < s.Concurrency || s.Requests > 1000 {
			return errors.New("MONITORING_WORKLOAD_INVALID")
		}
		total += s.Requests
	}
	if total > 2000 || c.MaximumErrors >= total {
		return errors.New("MONITORING_WORKLOAD_INVALID")
	}
	return nil
}

// Decode also requires the literal false/error fields; absence must not silently
// select zero-value policy different from the exact declared workload schema.
func decodeWorkload(raw []byte) (workloadConfig, error) {
	var fields map[string]json.RawMessage
	if release.StrictJSON(raw, &fields) != nil || len(fields) != 8 {
		return workloadConfig{}, errors.New("MONITORING_WORKLOAD_INVALID")
	}
	for _, key := range []string{"format_version", "deadline_seconds", "request_seconds", "fault_milliseconds", "maximum_body_bytes", "maximum_errors", "stop_on_error", "stages"} {
		if _, ok := fields[key]; !ok {
			return workloadConfig{}, errors.New("MONITORING_WORKLOAD_INVALID")
		}
	}
	var c workloadConfig
	if release.StrictJSON(raw, &c) != nil || c.validate() != nil {
		return workloadConfig{}, errors.New("MONITORING_WORKLOAD_INVALID")
	}
	return c, nil
}
func TestMonitoringWorkloadRequiredFalseAndInvalidSource(t *testing.T) {
	c := workloadConfig{FormatVersion: 1, DeadlineSeconds: 30, RequestSeconds: 5, FaultMilliseconds: 100, MaximumBodyBytes: 1 << 20, MaximumErrors: 0, StopOnError: false, Stages: []workloadStage{{1, 3}}}
	raw, _ := json.Marshal(c)
	if out, e := decodeWorkload(raw); e != nil || out.StopOnError {
		t.Fatal("literal false", e)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	delete(fields, "stop_on_error")
	bad, _ := json.Marshal(fields)
	if _, e := decodeWorkload(bad); e == nil {
		t.Fatal("missing false policy accepted")
	}
	fields["stop_on_error"] = "false"
	bad, _ = json.Marshal(fields)
	if _, e := decodeWorkload(bad); e == nil {
		t.Fatal("string false accepted")
	}
}

type requestFact struct {
	Stage               int        `json:"stage"`
	Sequence            int        `json:"sequence"`
	ElapsedMicroseconds int64      `json:"elapsed_microseconds"`
	Status              int        `json:"status"`
	Outcome             string     `json:"outcome"`
	Workspace           string     `json:"workspace_id"`
	ProcessEpoch        string     `json:"process_epoch"`
	ObservedAt          *time.Time `json:"observed_at"`
}
type capacityReport struct {
	Format                 string                            `json:"format"`
	Status                 string                            `json:"status"`
	Qualification          string                            `json:"qualification"`
	Engine                 string                            `json:"engine"`
	Workspace              string                            `json:"workspace_id"`
	Workload               workloadConfig                    `json:"workload"`
	StartedAt              time.Time                         `json:"started_at"`
	FinishedAt             time.Time                         `json:"finished_at"`
	Observations           []requestFact                     `json:"observations"`
	P95Microseconds        *int64                            `json:"p95_microseconds"`
	P99Microseconds        *int64                            `json:"p99_microseconds"`
	Errors                 int                               `json:"errors"`
	RuntimeBefore          monitoringmetrics.RuntimeSnapshot `json:"runtime_before"`
	RuntimeAfter           monitoringmetrics.RuntimeSnapshot `json:"runtime_after"`
	HTTP                   monitoringmetrics.Snapshot        `json:"http"`
	RecoveryMicroseconds   *int64                            `json:"recovery_microseconds"`
	OwnedConnectionsJoined bool                              `json:"owned_connections_joined"`
	ScopeCleanup           string                            `json:"scope_cleanup"`
	Dimensions             map[string]string                 `json:"dimensions"`
}

// The wrapper injects exact canonical read-only nonsecret workload input and a new
// private external output path; neither accepts an arbitrary customer endpoint.
const workloadInputEnvironment = "PROJECT_ONE_MONITORING_WORKLOAD_FILE"
const workloadReportEnvironment = "PROJECT_ONE_MONITORING_REPORT_FILE"

func TestActualMonitoringCapacity(t *testing.T) {
	t.Run("declared_authenticated_read_stages_and_cancel_recovery", func(t *testing.T) {
		input, output := os.Getenv(workloadInputEnvironment), os.Getenv(workloadReportEnvironment)
		if !filepath.IsAbs(input) || !filepath.IsAbs(output) || filepath.Clean(input) != input || filepath.Clean(output) != output {
			t.Fatal("exact declared input/private output required")
		}
		info, e := os.Lstat(input)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			t.Fatal("bounded regular input required")
		}
		raw, e := os.ReadFile(input)
		policy, decode := decodeWorkload(raw)
		if e != nil || decode != nil {
			t.Fatal("strict workload configuration required")
		}
		if _, e = os.Lstat(output); !os.IsNotExist(e) {
			t.Fatal("report may not overwrite")
		}
		parent, e := os.Lstat(filepath.Dir(output))
		if e != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
			t.Fatal("private output directory required")
		}
		g := setup(t, true)
		report := capacityReport{Format: "projectone.monitoring.capacity.v1", Status: "failed", Qualification: "Actual isolated selected authenticated diagnostic-read workload only; no model/GPU/HA/SLO or customer-production capacity qualification.", Engine: string(g.f.Store.Dialect()), Workspace: g.f.Workspace, Workload: policy, StartedAt: time.Now().UTC(), Observations: []requestFact{}, RuntimeBefore: monitoringmetrics.Runtime(), ScopeCleanup: "outer fresh repository runner must independently verify", Dimensions: map[string]string{"workload": "authenticated_monitoring_read", "input_tokens": "not_applicable_no_model_execution", "output_tokens": "not_applicable_no_model_execution", "context_tokens": "not_applicable_no_model_execution", "model": "not_applicable_no_model_execution", "hardware": runtime.GOOS + "/" + runtime.GOARCH + " actual control process; no GPU observation", "data": "fresh product fixture; no gateway requests or node observations"}}
		defer func() {
			report.FinishedAt = time.Now().UTC()
			report.RuntimeAfter = monitoringmetrics.Runtime()
			report.HTTP = g.m.Snapshot()
			raw, e := json.MarshalIndent(report, "", "  ")
			if e != nil {
				t.Error(e)
				return
			}
			f, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				t.Error("exclusive report create", e)
				return
			}
			if _, e = f.Write(append(raw, '\n')); e != nil {
				t.Error(e)
			}
			if e = f.Sync(); e != nil {
				t.Error(e)
			}
			if e = f.Close(); e != nil {
				t.Error(e)
			}
			directory, e := os.Open(filepath.Dir(output))
			if e != nil {
				t.Error(e)
				return
			}
			defer directory.Close()
			if e = directory.Sync(); e != nil {
				t.Error(e)
			}
		}()
		operation, cancel := context.WithTimeout(g.ctx, time.Duration(policy.DeadlineSeconds)*time.Second)
		defer cancel()
		handler := &monitoringhttp.Handler{Service: g.s}
		server := httptest.NewServer(g.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, e := g.i.Authenticate(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "admin")
			if e != nil {
				http.Error(w, "ACCESS_DENIED", 401)
				return
			}
			out, status, e := handler.Handle(r, a, uuid.NewString())
			if e != nil {
				http.Error(w, e.Error(), 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(out)
		})))
		defer server.Close()
		client := server.Client()
		client.Timeout = time.Duration(policy.RequestSeconds) * time.Second
		var mu sync.Mutex
		request := func(stage, sequence int) {
			started := time.Now()
			fact := requestFact{Stage: stage, Sequence: sequence, Outcome: "transport_error"}
			r, e := http.NewRequestWithContext(operation, "GET", server.URL+monitoringhttp.Path+"?workspace_id="+g.f.Workspace, nil)
			if e == nil {
				r.Header.Set("Authorization", "Bearer "+g.adminToken)
				resp, e := client.Do(r)
				if e == nil {
					fact.Status = resp.StatusCode
					body, e := io.ReadAll(io.LimitReader(resp.Body, policy.MaximumBodyBytes+1))
					resp.Body.Close()
					if e == nil && int64(len(body)) <= policy.MaximumBodyBytes {
						var snapshot monitoring.Snapshot
						if resp.StatusCode == 200 && json.Unmarshal(body, &snapshot) == nil && snapshot.WorkspaceID == g.f.Workspace && snapshot.Process.Epoch != "" && !strings.Contains(string(body), g.adminToken) {
							fact.Outcome = "succeeded"
							fact.Workspace = snapshot.WorkspaceID
							fact.ProcessEpoch = snapshot.Process.Epoch
							stamp := snapshot.ObservedAt
							fact.ObservedAt = &stamp
						} else {
							fact.Outcome = "response_invalid"
						}
					} else {
						fact.Outcome = "body_incomplete"
					}
				}
			}
			fact.ElapsedMicroseconds = time.Since(started).Microseconds()
			mu.Lock()
			report.Observations = append(report.Observations, fact)
			if fact.Outcome != "succeeded" {
				report.Errors++
			}
			stop := report.Errors > policy.MaximumErrors || policy.StopOnError && report.Errors > 0
			mu.Unlock()
			if stop {
				cancel()
			}
		}
		for stage, s := range policy.Stages {
			if operation.Err() != nil {
				break
			}
			jobs := make(chan int)
			var wg sync.WaitGroup
			for worker := 0; worker < s.Concurrency; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for sequence := range jobs {
						if operation.Err() == nil {
							request(stage, sequence)
						}
					}
				}()
			}
			for i := 0; i < s.Requests; i++ {
				select {
				case jobs <- i:
				case <-operation.Done():
				}
			}
			close(jobs)
			wg.Wait()
		}
		expected := 0
		for _, s := range policy.Stages {
			expected += s.Requests
		}
		sort.Slice(report.Observations, func(i, j int) bool {
			a, b := report.Observations[i], report.Observations[j]
			return a.Stage < b.Stage || a.Stage == b.Stage && a.Sequence < b.Sequence
		})
		latency := make([]int64, 0, len(report.Observations))
		for _, f := range report.Observations {
			latency = append(latency, f.ElapsedMicroseconds)
		}
		sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
		if len(latency) > 0 {
			p95, p99 := latency[(len(latency)*95+99)/100-1], latency[(len(latency)*99+99)/100-1]
			report.P95Microseconds = &p95
			report.P99Microseconds = &p99
		}
		// Cancel a real owned DB request while a current actor lock is held, then
		// release and join that exact transaction before measuring actual recovery.
		locked, releaseLock, joined := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			joined <- g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				var id string
				if e := q.QueryRow(ctx, g.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, g.f.User)).Scan(&id); e != nil {
					return e
				}
				close(locked)
				select {
				case <-releaseLock:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		select {
		case <-locked:
		case e := <-joined:
			t.Fatal(e)
		case <-g.ctx.Done():
			t.Fatal("owned lock deadline")
		}
		faultCtx, faultCancel := context.WithTimeout(operation, time.Duration(policy.FaultMilliseconds)*time.Millisecond)
		out, e := g.s.Get(faultCtx, g.admin, g.f.Workspace, "", uuid.NewString())
		faultCancel()
		close(releaseLock)
		if e == nil || out.FormatVersion != 0 {
			t.Fatal("cancelled snapshot exposed data")
		}
		select {
		case e := <-joined:
			if e != nil {
				t.Fatal(e)
			}
			report.OwnedConnectionsJoined = true
		case <-g.ctx.Done():
			t.Fatal("owned connection did not join")
		}
		recoveryStart := time.Now()
		if _, e := g.s.Get(operation, g.admin, g.f.Workspace, "", uuid.NewString()); e != nil {
			t.Fatal("actual fresh recovery failed", e)
		}
		elapsed := time.Since(recoveryStart).Microseconds()
		report.RecoveryMicroseconds = &elapsed
		if len(report.Observations) != expected || report.Errors > policy.MaximumErrors || operation.Err() != nil {
			t.Fatal("declared workload incomplete/error/deadline; failed receipt retained")
		}
		if g.m.Snapshot().Inflight != 0 {
			t.Fatal("HTTP request did not join")
		}
		report.Status = "passed_isolated_diagnostic_read_profile_only"
	})
}
