package monitoringmetrics

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/felixge/httpsnoop"
	"github.com/google/uuid"
)

type Series struct {
	Method               string   `json:"method"`
	Route                string   `json:"route"`
	Outcome              string   `json:"outcome"`
	Count                uint64   `json:"count"`
	ResponseBytes        uint64   `json:"response_bytes"`
	DurationMicroseconds uint64   `json:"duration_microseconds"`
	Buckets              []uint64 `json:"buckets"`
}
type Snapshot struct {
	Epoch               string    `json:"epoch"`
	StartedAt           time.Time `json:"started_at"`
	ObservedAt          time.Time `json:"observed_at"`
	Inflight            uint64    `json:"inflight"`
	Overflow            bool      `json:"overflow"`
	SkippedLogs         uint64    `json:"skipped_logs"`
	LatencyMilliseconds []float64 `json:"latency_milliseconds"`
	Series              []Series  `json:"series"`
}
type Recorder struct {
	mu       sync.Mutex
	policy   Policy
	logger   *slog.Logger
	epoch    string
	start    time.Time
	inflight uint64
	overflow bool
	skipped  uint64
	series   map[string]*Series
	logStart time.Time
	logs     int
}

func New(p Policy, l *slog.Logger) (*Recorder, error) {
	if p.Validate() != nil || p.StructuredLogging && l == nil {
		return nil, errors.New("MONITORING_CONFIGURATION_INVALID")
	}
	p.LatencyMilliseconds = append([]float64(nil), p.LatencyMilliseconds...)
	return &Recorder{policy: p, logger: l, epoch: uuid.NewString(), start: time.Now().UTC(), series: map[string]*Series{}}, nil
}
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Snapshot{Epoch: r.epoch, StartedAt: r.start, ObservedAt: time.Now().UTC(), Inflight: r.inflight, Overflow: r.overflow, SkippedLogs: r.skipped, LatencyMilliseconds: append([]float64{}, r.policy.LatencyMilliseconds...), Series: []Series{}}
	keys := make([]string, 0, len(r.series))
	for k := range r.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := *r.series[k]
		v.Buckets = append([]uint64{}, v.Buckets...)
		out.Series = append(out.Series, v)
	}
	return out
}
func method(v string) string {
	switch v {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return v
	}
	return "OTHER"
}
func route(v string) string {
	v = cellroute.Relative(v)
	switch {
	case strings.HasPrefix(v, "/api/v1/admin/monitoring"):
		return "monitoring"
	case strings.HasPrefix(v, "/api/v1/admin/"):
		return "admin"
	case v == wire.SessionPath || strings.HasPrefix(v, "/agent/v1/") || strings.HasPrefix(v, "/api/v1/agent/"):
		return "agent"
	case strings.HasPrefix(v, "/gateway/v1/") || strings.HasPrefix(v, "/api/v1/gateway/"):
		return "gateway"
	case strings.HasPrefix(v, "/api/v1/workspaces/"):
		return "workspace"
	case strings.HasPrefix(v, "/api/v1/"):
		return "api"
	case v == "/health/live" || v == "/health/ready" || v == "/healthz" || v == "/readyz":
		return "health"
	}
	return "other"
}

type requestState struct {
	mu          sync.Mutex
	status      int
	bytes       uint64
	overflow    bool
	writeError  bool
	hijacked    bool
	hijackError bool
}

func (s *requestState) wrote(n int64, e error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 {
		s.bytes = add(s.bytes, uint64(n), &s.overflow)
	}
	// A successful Write commits an implicit 200 even for an empty body. Keep
	// failed zero-byte writes unknown; their transport outcome remains explicit.
	if s.status == 0 && (n > 0 || e == nil) {
		s.status = http.StatusOK
	}
	if e != nil {
		s.writeError = true
	}
}
func (s *requestState) header(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == 0 && (code >= 200 || code == 101) {
		s.status = code
	}
}
func (s *requestState) flushed(e error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e != nil {
		s.writeError = true
	} else if s.status == 0 {
		s.status = http.StatusOK
	}
}

// Middleware preserves the underlying optional interface set using the exact
// reviewed httpsnoop Wrap. No body/header content or untrusted label is retained.
func (r *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		started := time.Now()
		s := &requestState{}
		r.mu.Lock()
		r.inflight = add(r.inflight, 1, &r.overflow)
		r.mu.Unlock()
		panicked := true
		defer func() { r.finish(req, w, s, time.Since(started), panicked) }()
		wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(code int) { next(code); s.header(code) }
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(b []byte) (int, error) { n, e := next(b); s.wrote(int64(n), e); return n, e }
			},
			WriteString: func(next httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
				return func(b string) (int, error) { n, e := next(b); s.wrote(int64(n), e); return n, e }
			},
			ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
				return func(src io.Reader) (int64, error) { n, e := next(src); s.wrote(n, e); return n, e }
			},
			Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc { return func() { next(); s.flushed(nil) } },
			FlushError: func(next httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
				return func() error { e := next(); s.flushed(e); return e }
			},
			Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
				return func() (net.Conn, *bufio.ReadWriter, error) {
					c, b, e := next()
					s.mu.Lock()
					s.hijacked = e == nil
					s.hijackError = e != nil
					s.mu.Unlock()
					return c, b, e
				}
			},
		})
		next.ServeHTTP(wrapped, req)
		panicked = false
	})
}
func (r *Recorder) finish(req *http.Request, w http.ResponseWriter, s *requestState, duration time.Duration, panicked bool) {
	s.mu.Lock()
	status, written, overflow := s.status, s.bytes, s.overflow
	outcome := "succeeded"
	switch {
	case panicked:
		outcome = "panic"
	case s.hijackError:
		outcome = "hijack_error"
	case s.hijacked:
		outcome = "hijacked"
	case s.writeError:
		outcome = "write_error"
	case req.Context().Err() != nil:
		outcome = "client_cancelled"
	case status >= 400:
		outcome = "http_error"
	case status == 0:
		outcome = "unobserved"
	}
	s.mu.Unlock()
	m, path := method(req.Method), route(req.URL.Path)
	elapsed := duration.Microseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	key := m + ":" + path + ":" + outcome
	r.mu.Lock()
	if r.inflight > 0 {
		r.inflight--
	}
	r.overflow = r.overflow || overflow
	v := r.series[key]
	if v == nil {
		v = &Series{Method: m, Route: path, Outcome: outcome, Buckets: make([]uint64, len(r.policy.LatencyMilliseconds)+1)}
		r.series[key] = v
	}
	v.Count = add(v.Count, 1, &r.overflow)
	v.ResponseBytes = add(v.ResponseBytes, written, &r.overflow)
	v.DurationMicroseconds = add(v.DurationMicroseconds, uint64(elapsed), &r.overflow)
	bucket := sort.SearchFloat64s(r.policy.LatencyMilliseconds, float64(duration)/float64(time.Millisecond))
	v.Buckets[bucket] = add(v.Buckets[bucket], 1, &r.overflow)
	logIt := false
	if r.policy.StructuredLogging {
		now := time.Now()
		if r.logStart.IsZero() || now.Sub(r.logStart) >= time.Second {
			r.logStart = now
			r.logs = 0
		}
		if r.logs < r.policy.MaximumLogsPerSecond {
			r.logs++
			logIt = true
		} else {
			r.skipped = add(r.skipped, 1, &r.overflow)
		}
	}
	r.mu.Unlock()
	if logIt {
		attrs := []any{"event", "http.completed", "process_epoch", r.epoch, "method", m, "route_class", path, "outcome", outcome, "duration_microseconds", elapsed, "response_bytes", written}
		if status != 0 {
			attrs = append(attrs, "status", status)
		}
		id := w.Header().Get("X-Request-ID")
		if parsed, e := uuid.Parse(id); e == nil && parsed.String() == id && parsed != uuid.Nil {
			attrs = append(attrs, "request_id", id)
		}
		r.logger.InfoContext(context.WithoutCancel(req.Context()), "http.completed", attrs...)
	}
}
