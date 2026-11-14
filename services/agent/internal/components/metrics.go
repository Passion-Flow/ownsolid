package components

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"strings"
	"unicode/utf8"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const maximumMetricsBytes = 2 * 1024 * 1024
const maximumMetricSamples = 4096

func invalidMetrics() error { return errors.New("COMPONENT_METRICS_INVALID") }
func label(metric *dto.Metric, name string) (string, bool) {
	value := ""
	found := false
	for _, pair := range metric.Label {
		if pair.GetName() == name {
			if found {
				return "", false
			}
			value = pair.GetValue()
			found = true
		}
	}
	return value, found
}
func singletonGauge(families map[string]*dto.MetricFamily, name string) (int64, error) {
	family := families[name]
	if family == nil || family.GetType() != dto.MetricType_GAUGE || len(family.Metric) != 1 || family.Metric[0].Gauge == nil || len(family.Metric[0].Label) != 0 {
		return 0, invalidMetrics()
	}
	value := family.Metric[0].Gauge.GetValue()
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 9007199254740991 || math.Trunc(value) != value {
		return 0, invalidMetrics()
	}
	return int64(value), nil
}
func rootGauge(families map[string]*dto.MetricFamily, name string) (int64, error) {
	family := families[name]
	if family == nil || family.GetType() != dto.MetricType_GAUGE {
		return 0, invalidMetrics()
	}
	found := false
	var result int64
	for _, metric := range family.Metric {
		mount, ok := label(metric, "mountpoint")
		if !ok {
			return 0, invalidMetrics()
		}
		if mount != "/" {
			continue
		}
		if found || metric.Gauge == nil {
			return 0, invalidMetrics()
		}
		value := metric.Gauge.GetValue()
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 9007199254740991 || math.Trunc(value) != value {
			return 0, invalidMetrics()
		}
		result = int64(value)
		found = true
	}
	if !found {
		return 0, invalidMetrics()
	}
	return result, nil
}

func ParseMetrics(body []byte) (wire.ComponentMetrics, error) {
	return ParseMetricsVersion(body, wire.NodeExporterVersion)
}
func ParseMetricsVersion(body []byte, expectedVersion string) (wire.ComponentMetrics, error) {
	if _, approved := wire.RecipeForVersion(expectedVersion); !approved {
		return wire.ComponentMetrics{}, invalidMetrics()
	}
	var out wire.ComponentMetrics
	if len(body) > maximumMetricsBytes || !utf8.Valid(body) || bytes.Count(body, []byte("\n")) > maximumMetricSamples*2 {
		return out, invalidMetrics()
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, e := parser.TextToMetricFamilies(bytes.NewReader(body))
	if e != nil {
		return out, invalidMetrics()
	}
	count := 0
	for _, family := range families {
		count += len(family.Metric)
		if count > maximumMetricSamples {
			return out, invalidMetrics()
		}
	}
	build := families["node_exporter_build_info"]
	if build == nil || build.GetType() != dto.MetricType_GAUGE || len(build.Metric) != 1 || build.Metric[0].Gauge == nil || build.Metric[0].Gauge.GetValue() != 1 {
		return out, invalidMetrics()
	}
	version, ok := label(build.Metric[0], "version")
	if !ok || version != expectedVersion {
		return out, invalidMetrics()
	}
	cpu := families["node_cpu_seconds_total"]
	if cpu == nil || cpu.GetType() != dto.MetricType_COUNTER || len(cpu.Metric) == 0 {
		return out, invalidMetrics()
	}
	seen := map[string]bool{}
	idle := false
	for _, metric := range cpu.Metric {
		if metric.Counter == nil {
			return out, invalidMetrics()
		}
		core, ok := label(metric, "cpu")
		if !ok || core == "" || len(core) > 16 {
			return out, invalidMetrics()
		}
		mode, ok := label(metric, "mode")
		if !ok || mode == "" || len(mode) > 32 {
			return out, invalidMetrics()
		}
		key := core + ":" + mode
		if seen[key] {
			return out, invalidMetrics()
		}
		seen[key] = true
		value := metric.Counter.GetValue()
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return out, invalidMetrics()
		}
		out.CPUSecondsTotal += value
		if mode == "idle" {
			out.CPUIdleSecondsTotal += value
			idle = true
		}
	}
	if !idle {
		return out, invalidMetrics()
	}
	if out.MemoryTotalBytes, e = singletonGauge(families, "node_memory_MemTotal_bytes"); e != nil {
		return out, e
	}
	if out.MemoryAvailableBytes, e = singletonGauge(families, "node_memory_MemAvailable_bytes"); e != nil {
		return out, e
	}
	if out.FilesystemTotalBytes, e = rootGauge(families, "node_filesystem_size_bytes"); e != nil {
		return out, e
	}
	if out.FilesystemAvailableBytes, e = rootGauge(families, "node_filesystem_avail_bytes"); e != nil {
		return out, e
	}
	return out, out.Validate()
}
func scrape(ctx context.Context, client *http.Client, endpoint string) (wire.ComponentMetrics, error) {
	return scrapeVersion(ctx, client, endpoint, wire.NodeExporterVersion)
}
func scrapeVersion(ctx context.Context, client *http.Client, endpoint, expectedVersion string) (wire.ComponentMetrics, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return wire.ComponentMetrics{}, errors.New("COMPONENT_METRICS_UNAVAILABLE")
	}
	r.Header.Set("Accept", "text/plain; version=0.0.4")
	response, e := client.Do(r)
	if e != nil {
		return wire.ComponentMetrics{}, errors.New("COMPONENT_METRICS_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") {
		return wire.ComponentMetrics{}, invalidMetrics()
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, maximumMetricsBytes+1))
	if e != nil {
		return wire.ComponentMetrics{}, errors.New("COMPONENT_METRICS_UNAVAILABLE")
	}
	return ParseMetricsVersion(body, expectedVersion)
}
func Scrape(ctx context.Context) (wire.ComponentMetrics, error) {
	return ScrapeVersion(ctx, wire.NodeExporterVersion)
}
func ScrapeVersion(ctx context.Context, version string, policies ...settings.Policy) (wire.ComponentMetrics, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	transport := &http.Transport{MaxConnsPerHost: 1, DisableKeepAlives: true, ResponseHeaderTimeout: settings.Seconds(policy.MetricsHeaderSeconds)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: settings.Seconds(policy.MetricsSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("COMPONENT_METRICS_INVALID") }}
	recipe, approved := wire.RecipeForVersion(version)
	if !approved {
		return wire.ComponentMetrics{}, errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	return scrapeVersion(ctx, client, "http://"+recipe.ListenAddress+"/metrics", version)
}
