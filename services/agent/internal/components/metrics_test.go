package components

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const syntheticMetrics = `# TYPE node_exporter_build_info gauge
node_exporter_build_info{version="1.12.1"} 1
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 80
node_cpu_seconds_total{cpu="0",mode="user"} 20
# TYPE node_memory_MemTotal_bytes gauge
node_memory_MemTotal_bytes 1024
# TYPE node_memory_MemAvailable_bytes gauge
node_memory_MemAvailable_bytes 512
# TYPE node_filesystem_size_bytes gauge
node_filesystem_size_bytes{mountpoint="/",device="synthetic",fstype="ext4"} 2048
# TYPE node_filesystem_avail_bytes gauge
node_filesystem_avail_bytes{mountpoint="/",device="synthetic",fstype="ext4"} 1024
`

func TestBoundedMetricsRequireActualFiniteTypedSamples(t *testing.T) {
	m, e := ParseMetrics([]byte(syntheticMetrics))
	if e != nil || m.CPUSecondsTotal != 100 || m.CPUIdleSecondsTotal != 80 || m.MemoryTotalBytes != 1024 {
		t.Fatal("valid synthetic metrics rejected", e)
	}
	for _, body := range []string{strings.Replace(syntheticMetrics, " 80\n", " NaN\n", 1), strings.Replace(syntheticMetrics, "version=\"1.12.1\"", "version=\"unknown\"", 1), strings.Replace(syntheticMetrics, "node_memory_MemAvailable_bytes 512", "node_memory_MemAvailable_bytes 2048", 1), strings.ReplaceAll(syntheticMetrics, "mountpoint=\"/\"", "mountpoint=\"/other\""), syntheticMetrics + "node_cpu_seconds_total{cpu=\"0\",mode=\"idle\"} 80\n", strings.Repeat("x", maximumMetricsBytes+1)} {
		if _, e = ParseMetrics([]byte(body)); e == nil {
			t.Fatal("invalid metric body fabricated a value")
		}
	}
}
func TestMetricsFailureIsNotZeroSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	if _, e := scrape(context.Background(), server.Client(), server.URL); e == nil {
		t.Fatal("unavailable exporter returned successful zeros")
	}
}
