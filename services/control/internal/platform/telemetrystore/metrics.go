package telemetrystore

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
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

// Sample is one current value of a process metric. Names and labels come from code,
// never input, so the number of series stays fixed.
type Sample struct {
	Name   string
	Value  float64
	Labels []Label
}

// Label is one label of a sample, added to the service and instance labels.
type Label struct {
	Name, Value string
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
	labels := `service="` + labelEscaper.Replace(service) + `",instance="` + labelEscaper.Replace(instance) + `"`
	p := &MetricsPusher{client: client, interval: interval, labels: labels, logger: logger, started: time.Now()}
	p.sources = []SampleSource{p.processSamples}
	return p, nil
}

// The services a series names: control, or an agent gateway beside it (ADR 0048).
const (
	ServiceControl      = "control"
	ServiceAgentGateway = "agent-gateway"
)

// OpenMetrics returns the pusher for this process, labelled as service. Add its sources,
// then start Run for the values to reach the store.
func OpenMetrics(s Settings, service string, logger *slog.Logger) (*MetricsPusher, error) {
	client, e := NewClient(s.MetricsAddress, s.RequestTimeout)
	if e != nil {
		return nil, e
	}
	instance, e := os.Hostname()
	if e != nil || instance == "" {
		instance = unknownInstance
	}
	return NewMetricsPusher(client, s.MetricsPushInterval, service, instance, logger)
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
			body.WriteByte('{')
			body.WriteString(p.labels)
			for _, l := range s.Labels {
				body.WriteString(`,` + l.Name + `="` + labelEscaper.Replace(l.Value) + `"`)
			}
			body.WriteString("} ")
			body.WriteString(strconv.FormatFloat(s.Value, 'g', -1, 64))
			body.WriteByte('\n')
		}
	}
	return body.Bytes()
}

func (p *MetricsPusher) processSamples() []Sample {
	return []Sample{
		{Name: "ownsolid_process_uptime_seconds", Value: time.Since(p.started).Seconds()},
		{Name: "ownsolid_telemetry_metric_pushes_failed_total", Value: float64(p.failed.Load())},
	}
}
