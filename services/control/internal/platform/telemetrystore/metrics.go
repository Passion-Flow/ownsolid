package telemetrystore

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	metricsImportPath = "/api/v1/import/prometheus"
	// unknownInstance labels the series if the operating system reports no host name.
	unknownInstance = "unknown"
)

// Sample is one current value of a process metric. Names come from code, never input.
type Sample struct {
	Name  string
	Value float64
}

// SampleSource returns current values. It runs on the pusher's goroutine at every push,
// so it must be cheap and safe to call while the process serves requests.
type SampleSource func() []Sample

// labelEscaper applies the escaping of the Prometheus text format to a label value.
var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// MetricsPusher sends the values of its sources to the metrics store on a fixed
// interval. Values are current readings, so a failed push is not retried: the next one
// carries newer values, and the store being down costs only those points.
type MetricsPusher struct {
	client   *Client
	interval time.Duration
	labels   string
	logger   *slog.Logger
	sources  []SampleSource
	started  time.Time
	failed   atomic.Uint64
	failing  bool
}

// NewMetricsPusher returns a pusher that labels every series with service and instance.
func NewMetricsPusher(client *Client, interval time.Duration, service, instance string, logger *slog.Logger) (*MetricsPusher, error) {
	if interval <= 0 {
		return nil, errors.New("metrics push interval must be positive")
	}
	labels := `{service="` + labelEscaper.Replace(service) + `",instance="` + labelEscaper.Replace(instance) + `"}`
	p := &MetricsPusher{client: client, interval: interval, labels: labels, logger: logger, started: time.Now()}
	p.sources = []SampleSource{p.processSamples}
	return p, nil
}

// OpenMetrics returns the pusher for this control process. Add its sources, then start
// Run for the values to reach the store.
func OpenMetrics(s Settings, logger *slog.Logger) (*MetricsPusher, error) {
	client, e := NewClient(s.MetricsAddress, s.RequestTimeout)
	if e != nil {
		return nil, e
	}
	instance, e := os.Hostname()
	if e != nil || instance == "" {
		instance = unknownInstance
	}
	return NewMetricsPusher(client, s.MetricsPushInterval, "control", instance, logger)
}

// Add registers a source. Call it before Run.
func (p *MetricsPusher) Add(source SampleSource) { p.sources = append(p.sources, source) }

// Run pushes at once and then on every interval until ctx ends.
func (p *MetricsPusher) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.pushAndReport(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}
}

// pushAndReport logs when the store stops and starts answering, not on every failure.
func (p *MetricsPusher) pushAndReport(ctx context.Context) {
	e := p.push(ctx)
	switch {
	case e == nil:
		if p.failing {
			p.failing = false
			p.logger.Info("telemetry_metrics_push_recovered", "failed_total", p.failed.Load())
		}
	case ctx.Err() != nil:
		// Shutdown interrupted the push; the store did not fail.
	default:
		p.failed.Add(1)
		if !p.failing {
			p.failing = true
			p.logger.Warn("telemetry_metrics_push_failed", "error", e.Error())
		}
	}
}

func (p *MetricsPusher) push(ctx context.Context) error {
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, p.client.endpoint(metricsImportPath, nil), bytes.NewReader(p.render()))
	if e != nil {
		return e
	}
	request.Header.Set("Content-Type", "text/plain; version=0.0.4")
	return p.client.exchange(request, nil)
}

// render writes the Prometheus text format, one line per value. The store stamps the
// time it receives them.
func (p *MetricsPusher) render() []byte {
	var body bytes.Buffer
	for _, source := range p.sources {
		for _, s := range source() {
			body.WriteString(s.Name)
			body.WriteString(p.labels)
			body.WriteByte(' ')
			body.WriteString(strconv.FormatFloat(s.Value, 'g', -1, 64))
			body.WriteByte('\n')
		}
	}
	return body.Bytes()
}

func (p *MetricsPusher) processSamples() []Sample {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return []Sample{
		{"ownsolid_process_uptime_seconds", time.Since(p.started).Seconds()},
		{"ownsolid_go_goroutines", float64(runtime.NumGoroutine())},
		{"ownsolid_go_heap_alloc_bytes", float64(memory.HeapAlloc)},
		{"ownsolid_go_sys_bytes", float64(memory.Sys)},
		{"ownsolid_go_gc_cycles_total", float64(memory.NumGC)},
		{"ownsolid_go_gc_pause_seconds_total", time.Duration(memory.PauseTotalNs).Seconds()},
		{"ownsolid_telemetry_metric_pushes_failed_total", float64(p.failed.Load())},
	}
}
