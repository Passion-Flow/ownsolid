package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func TestInstallationLeaseRefusalBeforeHandlerAndBeforeSuccess(t *testing.T) {
	for _, phase := range []string{"admission", "success_header"} {
		t.Run(phase, func(t *testing.T) {
			calls, executed := 0, false
			check := func() error {
				calls++
				if phase == "admission" || calls > 1 {
					return errors.New("INSTALLATION_LOCK_LOST")
				}
				return nil
			}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executed = true
				w.Header().Set("Content-Length", "999")
				w.Header().Set("Content-Disposition", "attachment; filename=private-result")
				w.Header().Set("Set-Cookie", "private-issued-session")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "private-success-DTO")
			})
			w := httptest.NewRecorder()
			serveInstallationLeaseResponse(w, httptest.NewRequest("GET", "https://admin.example.invalid/api/v1/me", nil), config.DefaultHTTPPolicy(), make(chan struct{}, 1), check, next)
			var problem map[string]any
			if json.Unmarshal(w.Body.Bytes(), &problem) != nil || w.Code != 503 || problem["code"] != "SERVICE_NOT_READY" || len(problem) != 5 || strings.Contains(w.Body.String(), "private-") || w.Header().Get("Content-Length") != "" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("lease failure exposed successful headers or DTO", w.Code, w.Body.String())
			}
			if _, e := uuid.Parse(w.Header().Get("X-Request-ID")); e != nil {
				t.Fatal("canonical request identifier absent")
			}
			if executed != (phase == "success_header") {
				t.Fatal("wrong authority boundary")
			}
		})
	}
}

func TestInstallationLeasePreservesUnknownCommitResponse(t *testing.T) {
	calls := 0
	check := func() error {
		calls++
		if calls > 1 {
			return errors.New("INSTALLATION_LOCK_LOST")
		}
		return nil
	}
	w := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpserver.Problem(w, "synthetic-uncertain", errors.New("STORAGE_COMMIT_OUTCOME_UNKNOWN"))
	})
	serveInstallationLeaseResponse(w, httptest.NewRequest("POST", "https://admin.example.invalid/api/v1/workspaces", nil), config.DefaultHTTPPolicy(), make(chan struct{}, 1), check, next)
	if w.Code != 409 || w.Header().Get("Retry-After") != "" || !strings.Contains(w.Body.String(), "STORAGE_COMMIT_OUTCOME_UNKNOWN") || calls != 1 {
		t.Fatal("commit uncertainty was overwritten or retried", w.Code, w.Body.String(), calls)
	}
}

type leaseOptionalWriter struct {
	*httptest.ResponseRecorder
	duplex, deadline bool
}

func (w *leaseOptionalWriter) EnableFullDuplex() error         { w.duplex = true; return nil }
func (w *leaseOptionalWriter) SetReadDeadline(time.Time) error { w.deadline = true; return nil }
func (w *leaseOptionalWriter) ReadFrom(r io.Reader) (int64, error) {
	b, e := io.ReadAll(r)
	if e != nil {
		return 0, e
	}
	n, e := w.ResponseRecorder.Write(b)
	return int64(n), e
}

func TestInstallationLeasePreservesStreamingOptionalInterfaces(t *testing.T) {
	w := &leaseOptionalWriter{ResponseRecorder: httptest.NewRecorder()}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Hijacker); ok {
			t.Fatal("unsupported hijack interface manufactured")
		}
		reader, ok := w.(io.ReaderFrom)
		if !ok {
			t.Fatal("original reader interface removed")
		}
		controller := http.NewResponseController(w)
		if controller.EnableFullDuplex() != nil || controller.SetReadDeadline(time.Now().Add(time.Second)) != nil {
			t.Fatal("original controller interface lost")
		}
		if _, e := reader.ReadFrom(strings.NewReader("data: current\n\n")); e != nil {
			t.Fatal(e)
		}
		if e := controller.Flush(); e != nil {
			t.Fatal(e)
		}
	})
	serveInstallationLeaseResponse(w, httptest.NewRequest("GET", "https://admin.example.invalid/events", nil), config.DefaultHTTPPolicy(), make(chan struct{}, 1), func() error { return nil }, next)
	if !w.duplex || !w.deadline || !w.Flushed || w.Code != 200 || w.Body.String() != "data: current\n\n" {
		t.Fatal("streaming contract changed")
	}
}

func TestInstallationLeaseAdmissionIsBoundedBeforeSQL(t *testing.T) {
	admission := make(chan struct{}, 1)
	admission <- struct{}{}
	check := func() error { t.Fatal("over-capacity request reached pinned SQL"); return nil }
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("over-capacity request reached business owner") })
	w := httptest.NewRecorder()
	serveInstallationLeaseResponse(w, httptest.NewRequest("GET", "https://admin.example.invalid/api/v1/me", nil), config.DefaultHTTPPolicy(), admission, check, next)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "AUTH_CAPACITY_EXCEEDED") {
		t.Fatal("bounded admission contract changed", w.Code, w.Body.String())
	}
}

func TestInstallationLeaseObserverBoundsProbeAndJoinsIdleOwner(t *testing.T) {
	g := newRuntimeLifecycle(t.Context())
	active, stopped := make(chan struct{}), make(chan struct{})
	g.Loop("IDLE_BACKGROUND_OWNER", func(ctx context.Context) {
		close(active)
		<-ctx.Done()
		close(stopped)
	})
	<-active
	probeDeadline := make(chan time.Duration, 1)
	g.Go("INSTALLATION_LEASE", func(ctx context.Context) error {
		e := auditInstallationLease(ctx, time.Millisecond, 20*time.Millisecond, func(probe context.Context) error {
			deadline, ok := probe.Deadline()
			if !ok {
				return errors.New("PROBE_DEADLINE_MISSING")
			}
			probeDeadline <- time.Until(deadline)
			<-probe.Done()
			return probe.Err()
		})
		if ctx.Err() == nil && e != nil {
			g.Fail(errors.New("INSTALLATION_LOCK_LOST"))
			return context.Canceled
		}
		return e
	})
	select {
	case <-g.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle owner remained unbounded without an HTTP request")
	}
	budget := <-probeDeadline
	if budget <= 0 || budget > 20*time.Millisecond {
		t.Fatal("probe did not inherit its configured deadline", budget)
	}
	cleanup, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if e := g.Close(cleanup); e == nil || e.Error() != "INSTALLATION_LOCK_LOST" {
		t.Fatal("original authority failure was not retained", e)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("idle background owner not joined")
	}
	select {
	case <-g.joined:
	default:
		t.Fatal("runtime join not confirmed")
	}
}

func TestInstallationLeaseObserverCancellationAndInvalidPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	if e := auditInstallationLease(ctx, time.Nanosecond, time.Second, func(context.Context) error { calls++; return nil }); !errors.Is(e, context.Canceled) || calls != 0 {
		t.Fatal("normal canceled lifecycle attempted a new lease probe", e, calls)
	}
	for _, interval := range []time.Duration{0, -time.Second} {
		if e := auditInstallationLease(t.Context(), interval, time.Second, func(context.Context) error { return nil }); e == nil || e.Error() != "INSTALLATION_LEASE_POLICY_INVALID" {
			t.Fatal("lease observation could be disabled", e)
		}
	}
	if e := auditInstallationLease(t.Context(), time.Second, 0, func(context.Context) error { return nil }); e == nil {
		t.Fatal("unbounded lease probe admitted")
	}
}

func TestInstallationLeaseSlotTimeoutLatchesOriginalOwner(t *testing.T) {
	for _, phase := range []string{"request", "observer"} {
		t.Run(phase, func(t *testing.T) {
			// No database is opened. The occupied serialization slot ensures that
			// neither path can reach SQL before its configured probe deadline.
			pool := new(pgxpool.Pool)
			store, e := transaction.FromPostgreSQL(pool)
			if e != nil {
				t.Fatal(e)
			}
			d := &selectedDatabase{postgres: pool, store: store, verify: make(chan struct{}, 1)}
			d.verify <- struct{}{}
			g := newRuntimeLifecycle(t.Context())
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()
				_ = g.Close(cleanup)
			})
			policy := config.DefaultRuntimePolicy()
			policy.HTTP.InstallationLeaseIntervalSeconds = 1
			policy.HTTP.ReadHeaderSeconds, policy.HTTP.RequestSeconds = 1, 1
			if phase == "request" {
				h := installationLeaseMiddleware(g, d, policy.HTTP)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("timed-out owner reached domain") }))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "https://admin.example.invalid/api/v1/me", nil))
				if w.Code != 503 {
					t.Fatal("unobserved authority was admitted", w.Code)
				}
			} else {
				if e = startInstallationLeaseMonitor(g, d, config.Config{Runtime: &policy}); e != nil {
					t.Fatal(e)
				}
				select {
				case <-g.ctx.Done():
				case <-time.After(4 * time.Second):
					t.Fatal("verification-slot wait escaped configured observation bound")
				}
			}
			<-d.verify
			if !d.leaseLost.Load() || g.Failure() == nil || g.Failure().Error() != "INSTALLATION_LOCK_LOST" {
				t.Fatal("unknown observation failed to latch original owner", g.Failure())
			}
			if e = d.Verify(t.Context()); e == nil || e.Error() != "INSTALLATION_LOCK_LOST" {
				t.Fatal("later verification cleared lost authority", e)
			}
		})
	}
}
