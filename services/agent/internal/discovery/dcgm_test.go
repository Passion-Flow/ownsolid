package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const (
	gpuA    = "GPU-aabbccdd-0000-0000-0000-000000000000"
	gpuB    = "GPU-bbccddee-0000-0000-0000-000000000000"
	gpuUtil = "DCGM_FI_DEV_GPU_UTIL_RATIO"
	gpuECC  = "DCGM_FI_DEV_ECC_DBE_VOL_TOTAL"
)

func gaugeText(name, samples string) string {
	return "# TYPE " + name + " gauge\n" + samples
}

func gpuSample(name, uuid, value string) string {
	return fmt.Sprintf("%s{UUID=%q} %s\n", name, uuid, value)
}

func TestParseDCGMPhysicalReadings(t *testing.T) {
	body := gaugeText(gpuUtil, gpuSample(gpuUtil, gpuB, "0.75")+gpuSample(gpuUtil, gpuA, "0")) +
		"# TYPE " + gpuECC + " counter\n" +
		gpuSample(gpuECC, gpuA, "0") +
		gpuSample(gpuECC, gpuB, "9223372036854775794") +
		gaugeText("DCGM_FI_DEV_BOARD_POWER_WATTS", gpuSample("DCGM_FI_DEV_BOARD_POWER_WATTS", gpuB, "140737488355330.0")) +
		fmt.Sprintf("%s{UUID=%q,GPU_I_ID=%q} 0.75\n", gpuUtil, gpuA, "1") +
		fmt.Sprintf("%s{UUID=%q,pod=%q} 4.5\n", gpuUtil, gpuA, "fixture") +
		fmt.Sprintf("%s{UUID=%q} 0.25\n", gpuUtil, "MIG-00000000-0000-0000-0000-000000000000") +
		"DCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} 100\n"
	devices, err := parseDCGM([]byte(body))
	if err != nil || len(devices) != 2 {
		t.Fatalf("parseDCGM = %v, %v", devices, err)
	}
	if devices[0].UUID != gpuA || devices[1].UUID != gpuB {
		t.Fatalf("unexpected device order: %v", devices)
	}
	if value, ok := devices[0].Samples[gpuUtil]; !ok || value != 0 {
		t.Fatalf("zero utilization missing: %v", devices[0].Samples)
	}
	if value, ok := devices[0].Samples[gpuECC]; !ok || value != 0 {
		t.Fatalf("zero ECC missing: %v", devices[0].Samples)
	}
	if len(devices[1].Samples) != 1 || devices[1].Samples[gpuUtil] != 0.75 {
		t.Fatalf("unsupported samples leaked into %v", devices[1].Samples)
	}
}

func TestParseDCGMFailures(t *testing.T) {
	sample := gpuSample(gpuUtil, gpuA, "0.5")
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{"empty exporter", "# HELP ignored ignored\n", ErrDCGMNoSamples},
		{"unsupported only", gaugeText(gpuUtil, gpuSample(gpuUtil, gpuA, "9223372036854775792")), ErrDCGMNoSamples},
		{"legacy units only", "DCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} 100\n", ErrDCGMNoSamples},
		{"malformed", "{", ErrDCGMInvalid},
		{"bad UUID", gaugeText(gpuUtil, gpuSample(gpuUtil, "0", "0")), ErrDCGMInvalid},
		{"two UUID labels", gaugeText(gpuUtil, fmt.Sprintf("%s{UUID=%q,uuid=%q} 0\n", gpuUtil, gpuA, gpuB)), ErrDCGMInvalid},
		{"duplicate", gaugeText(gpuUtil, sample+gpuSample(gpuUtil, "GPU-"+strings.ToUpper(gpuA[4:]), "0")), ErrDCGMInvalid},
		{"untyped", sample, ErrDCGMInvalid},
		{"non-finite", gaugeText(gpuUtil, gpuSample(gpuUtil, gpuA, "NaN")), ErrDCGMInvalid},
		{"infinite", gaugeText(gpuUtil, gpuSample(gpuUtil, gpuA, "+Inf")), ErrDCGMInvalid},
		{"ratio above one", gaugeText(gpuUtil, gpuSample(gpuUtil, gpuA, "1.2")), ErrDCGMInvalid},
		{"fractional ECC", "# TYPE " + gpuECC + " counter\n" + gpuSample(gpuECC, gpuA, "0.25"), ErrDCGMInvalid},
		{"negative ECC", "# TYPE " + gpuECC + " counter\n" + gpuSample(gpuECC, gpuA, "-1"), ErrDCGMInvalid},
		{"unexpected timestamp", gaugeText(gpuUtil, gpuSample(gpuUtil, gpuA, "0 42")), ErrDCGMInvalid},
		{"invalid UTF-8", sample + "\xff", ErrDCGMInvalid},
		{"too many lines", strings.Repeat("\n", maximumDCGMLines+1), ErrDCGMInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseDCGM([]byte(test.body))
			if got != nil || !errors.Is(err, test.want) {
				t.Fatalf("parseDCGM = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestCollectDCGMLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" || r.Header.Get("Accept") != "text/plain; version=0.0.4" {
			t.Errorf("unexpected scrape request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, gaugeText(gpuUtil, gpuSample(gpuUtil, gpuA, "0.5")))
	}))
	defer server.Close()

	start := time.Now().UTC()
	got, err := CollectDCGM(t.Context(), netip.MustParseAddrPort(server.Listener.Addr().String()), time.Second)
	if err != nil || len(got.Devices) != 1 || got.Devices[0].Samples[gpuUtil] != 0.5 {
		t.Fatalf("CollectDCGM = %v, %v", got, err)
	}
	if got.ObservedAt.Before(start) || got.ObservedAt.After(time.Now()) {
		t.Fatalf("unexpected scrape time: %s", got.ObservedAt)
	}
}

func TestCollectDCGMRefusesBadTransport(t *testing.T) {
	for _, test := range []struct {
		name  string
		serve http.HandlerFunc
		want  error
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/metrics" {
				t.Error("collector followed a redirect")
			}
			http.Redirect(w, r, "/redirected", http.StatusFound)
		}, ErrDCGMUnavailable},
		{"content type", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
		}, ErrDCGMInvalid},
		{"large response", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, strings.Repeat("x", maximumDCGMBytes+1))
		}, ErrDCGMInvalid},
		{"stalled body", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}, ErrDCGMUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.serve)
			defer server.Close()
			timeout := 2 * time.Second
			if test.name == "stalled body" {
				timeout = 100 * time.Millisecond
			}
			endpoint := netip.MustParseAddrPort(server.Listener.Addr().String())
			got, err := CollectDCGM(t.Context(), endpoint, timeout)
			if got.Devices != nil || !errors.Is(err, test.want) {
				t.Fatalf("CollectDCGM = %v, %v; want %v", got, err, test.want)
			}
		})
	}

	for _, address := range []string{"0.0.0.0:9400", "192.0.2.1:9400", "[::1%zone]:9400", "127.0.0.1:0"} {
		if _, err := CollectDCGM(t.Context(), netip.MustParseAddrPort(address), time.Second); !errors.Is(err, ErrDCGMConfiguration) {
			t.Errorf("address %s: %v", address, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CollectDCGM(ctx, netip.MustParseAddrPort("127.0.0.1:9400"), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}
