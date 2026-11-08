// Package report counts what the simulated fleet did and writes it as a JSON report.
// It measures; judging the numbers is left to whoever reads the report.
package report

import (
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"
)

// The calls a simulated agent makes, named as in api/agent/v1 where the contract names
// them. StreamOpen is opening the session stream, which carries the first heartbeat.
const (
	CallEnroll      = "enroll"
	CallHeartbeat   = "heartbeat"
	CallClaim       = "claim"
	CallAcknowledge = "acknowledge"
	CallEvents      = "events"
	CallRenew       = "renew"
	CallResult      = "result"
	CallStreamOpen  = "stream_open"
)

// Histogram buckets: each bound is growth times the one before, from the first bound to
// past the longest request, so a percentile is within a quarter of its true value.
const (
	firstBound = time.Millisecond
	growth     = 1.25
	buckets    = 56
)

var bounds = func() []time.Duration {
	out := make([]time.Duration, buckets)
	for i := range out {
		out[i] = time.Duration(float64(firstBound) * math.Pow(growth, float64(i)))
	}
	return out
}()

// Histogram counts durations in exponential buckets.
type Histogram struct {
	counts [buckets + 1]int64
	total  int64
	max    time.Duration
}

// Add counts d.
func (h *Histogram) Add(d time.Duration) {
	i := sort.Search(buckets, func(i int) bool { return bounds[i] >= d })
	h.counts[i]++
	h.total++
	h.max = max(h.max, d)
}

// Quantile is the bucket bound at or below which q of the durations fell, zero when none
// were counted, and the maximum for the overflow bucket.
func (h *Histogram) Quantile(q float64) time.Duration {
	if h.total == 0 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(h.total)))
	var seen int64
	for i, n := range h.counts {
		seen += n
		if seen >= rank {
			if i == buckets {
				return h.max
			}
			return min(bounds[i], h.max)
		}
	}
	return h.max
}

// ms is d in milliseconds, to a tenth.
func ms(d time.Duration) float64 { return math.Round(float64(d)/float64(time.Millisecond)*10) / 10 }

// Latency is a summary of a histogram in milliseconds.
type Latency struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

func (h *Histogram) summary() Latency {
	return Latency{P50: ms(h.Quantile(0.5)), P90: ms(h.Quantile(0.9)), P99: ms(h.Quantile(0.99)), Max: ms(h.max)}
}

// Calls counts one call.
type Calls struct {
	Succeeded int64            `json:"succeeded"`
	Failed    map[string]int64 `json:"failed"`
	// LatencyMilliseconds covers successful calls. A claim's includes the time control held it.
	LatencyMilliseconds Latency `json:"latency_milliseconds"`
	latency             Histogram
}

// Tasks counts the work agents received.
type Tasks struct {
	Leases       int64 `json:"leases"`
	Acknowledged int64 `json:"acknowledged"`
	Succeeded    int64 `json:"succeeded"`
	Resyncs      int64 `json:"resyncs"`
	// LostReplies counts replies a lose_replies fault dropped. Each was sent again.
	LostReplies int64 `json:"lost_replies"`
}

// Fault reports one scripted fault. Recovery is measured from the moment the fault ends:
// at once for disconnect and restart, after the duration for a partition. RecoveredAgents
// counts the hit agents that had a good heartbeat since; the seconds describe them.
type Fault struct {
	Kind            string   `json:"kind"`
	AtSeconds       int      `json:"at_seconds"`
	Percent         int      `json:"percent"`
	DurationSeconds int      `json:"duration_seconds,omitempty"`
	Agents          int      `json:"agents"`
	Measured        bool     `json:"measured"`
	RecoveredAgents int      `json:"recovered_agents"`
	FullRecovery    *float64 `json:"full_recovery_seconds"`
	P50Recovery     *float64 `json:"p50_recovery_seconds"`
	P99Recovery     *float64 `json:"p99_recovery_seconds"`
	recovery        []time.Duration
}

// WindowCalls counts one call within a window.
type WindowCalls struct {
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	// P99Milliseconds covers the window's successful calls.
	P99Milliseconds float64 `json:"p99_milliseconds"`
	latency         Histogram
}

// Window is one stretch of a run, so a steady period can be read apart from the storms
// around it. EndSeconds counts from the start of the run.
type Window struct {
	EndSeconds int                     `json:"end_seconds"`
	Connected  int                     `json:"connected"`
	OnStream   int                     `json:"on_stream"`
	Calls      map[string]*WindowCalls `json:"calls"`
	// GeneratorLagMilliseconds is the longest the simulator's own scheduling ran late in the
	// window. A long lag beside a slow call means the delay may not be control's.
	GeneratorLagMilliseconds float64 `json:"generator_lag_max_milliseconds"`
}

// Report is the result of a run.
type Report struct {
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Agents    int       `json:"agents"`
	Connected int       `json:"connected_at_end"`
	OnStream  int       `json:"on_stream_at_end"`
	// Gateways counts the agents on a stream at the end by agent gateway, when the run has
	// gateways (ADR 0048).
	Gateways map[string]int    `json:"gateways_at_end,omitempty"`
	Revoked  int               `json:"revoked"`
	Calls    map[string]*Calls `json:"calls"`
	Tasks    Tasks             `json:"tasks"`
	Faults   []*Fault          `json:"faults"`
	Windows  []Window          `json:"windows"`
}

// Recorder collects a run's counts from every agent goroutine.
type Recorder struct {
	mu     sync.Mutex
	report Report
	window map[string]*WindowCalls
	lag    time.Duration
}

// New starts a report for agents agents.
func New(agents int, started time.Time) *Recorder {
	return &Recorder{report: Report{StartedAt: started.UTC(), Agents: agents, Calls: map[string]*Calls{}, Faults: []*Fault{}, Windows: []Window{}}, window: map[string]*WindowCalls{}}
}

func (r *Recorder) windowCalls(name string) *WindowCalls {
	c := r.window[name]
	if c == nil {
		c = &WindowCalls{}
		r.window[name] = c
	}
	return c
}

// CloseWindow ends the current window at end seconds into the run.
func (r *Recorder) CloseWindow(end, connected, onStream int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.window {
		c.P99Milliseconds = c.latency.summary().P99
	}
	r.report.Windows = append(r.report.Windows, Window{EndSeconds: end, Connected: connected, OnStream: onStream, Calls: r.window, GeneratorLagMilliseconds: ms(r.lag)})
	r.window = map[string]*WindowCalls{}
	r.lag = 0
}

// ObserveLag records how late the simulator's scheduling ran, keeping the current window's
// longest.
func (r *Recorder) ObserveLag(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lag = max(r.lag, d)
}

func (r *Recorder) calls(name string) *Calls {
	c := r.report.Calls[name]
	if c == nil {
		c = &Calls{Failed: map[string]int64{}}
		r.report.Calls[name] = c
	}
	return c
}

// Succeeded counts a successful call that took d.
func (r *Recorder) Succeeded(call string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.calls(call)
	c.Succeeded++
	c.latency.Add(d)
	w := r.windowCalls(call)
	w.Succeeded++
	w.latency.Add(d)
}

// Failed counts a failed call under code.
func (r *Recorder) Failed(call, code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls(call).Failed[code]++
	r.windowCalls(call).Failed++
}

// Task changes the task counts.
func (r *Recorder) Task(change func(*Tasks)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	change(&r.report.Tasks)
}

// AddFault starts the report of a fault that hit agents agents, and returns its index.
func (r *Recorder) AddFault(f Fault) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.report.Faults = append(r.report.Faults, &f)
	return len(r.report.Faults) - 1
}

// Recovered records that one agent hit by fault index had a good heartbeat after d.
func (r *Recorder) Recovered(index int, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.report.Faults[index]
	f.RecoveredAgents++
	f.recovery = append(f.recovery, d)
}

// Snapshot is a copy of the counts so far, with latencies and recoveries summarised.
func (r *Recorder) Snapshot() Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.report
	out.Calls = map[string]*Calls{}
	for name, c := range r.report.Calls {
		copied := *c
		copied.Failed = map[string]int64{}
		for code, n := range c.Failed {
			copied.Failed[code] = n
		}
		copied.LatencyMilliseconds = c.latency.summary()
		out.Calls[name] = &copied
	}
	out.Windows = append([]Window{}, r.report.Windows...)
	out.Faults = make([]*Fault, len(r.report.Faults))
	for i, f := range r.report.Faults {
		copied := *f
		copied.recovery = nil
		if f.Measured && f.RecoveredAgents > 0 {
			sorted := append([]time.Duration(nil), f.recovery...)
			sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
			seconds := func(q float64) *float64 {
				d := sorted[max(0, int(math.Ceil(q*float64(len(sorted))))-1)]
				v := math.Round(d.Seconds()*1000) / 1000
				return &v
			}
			copied.P50Recovery, copied.P99Recovery = seconds(0.5), seconds(0.99)
			if f.RecoveredAgents == f.Agents {
				copied.FullRecovery = seconds(1)
			}
		}
		out.Faults[i] = &copied
	}
	return out
}

// Finish records the state at the end of the run and returns the final report.
func (r *Recorder) Finish(ended time.Time, connected, onStream, revoked int, gateways map[string]int) Report {
	r.mu.Lock()
	r.report.EndedAt = ended.UTC()
	r.report.Connected, r.report.OnStream, r.report.Revoked, r.report.Gateways = connected, onStream, revoked, gateways
	r.mu.Unlock()
	return r.Snapshot()
}

// Encode is report as indented JSON.
func Encode(report Report) ([]byte, error) {
	raw, e := json.MarshalIndent(report, "", "  ")
	return append(raw, '\n'), e
}
