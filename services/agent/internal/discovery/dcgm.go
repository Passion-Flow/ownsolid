package discovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	maximumDCGMBytes    = 2 * 1024 * 1024
	maximumDCGMLines    = 8192
	maximumExactInteger = 1<<53 - 1
	dcgmBlankDouble     = 140737488355328
	dcgmBlankInteger    = 0x7ffffffffffffff0
)

var (
	// ErrDCGMConfiguration means the address or timeout needs correction.
	ErrDCGMConfiguration = errors.New("DCGM_CONFIGURATION_INVALID: configure a numeric loopback address and a positive timeout")
	// ErrDCGMUnavailable means the configured exporter could not be read.
	ErrDCGMUnavailable = errors.New("DCGM_UNAVAILABLE: check the local exporter and retry")
	// ErrDCGMInvalid means the response was malformed or ambiguous.
	ErrDCGMInvalid = errors.New("DCGM_RESPONSE_INVALID: check the exporter version and physical GPU metrics configuration")
	// ErrDCGMNoSamples means the exporter returned no usable physical GPU readings.
	ErrDCGMNoSamples = errors.New("DCGM_NO_SAMPLES: check driver access, physical devices and configured fields")
)

var physicalGPUUUID = regexp.MustCompile(`^GPU-[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

type dcgmField struct {
	counter bool
	integer bool
	minimum float64
	maximum float64
}

// The canonical DCGM 4.7 names carry the units. Optional fields may be absent
// on a working device, and never acquire an inferred zero.
var dcgmFields = map[string]dcgmField{
	"DCGM_FI_DEV_GPU_UTIL_RATIO":                     {maximum: 1},
	"DCGM_FI_DEV_FB_CAPACITY_BYTES":                  {integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_FB_USED_BYTES":                      {integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_FB_FREE_BYTES":                      {integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_GPU_TEMP_CELSIUS":                   {minimum: -273.15, maximum: 1000},
	"DCGM_FI_DEV_BOARD_POWER_WATTS":                  {maximum: maximumExactInteger},
	"DCGM_FI_DEV_SM_CLOCK_HERTZ":                     {integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_MEMORY_CLOCK_HERTZ":                 {integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_ECC_SBE_VOL_TOTAL":                  {counter: true, integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_ECC_DBE_VOL_TOTAL":                  {counter: true, integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_NVLINK_THROUGHPUT_BYTES_PER_SECOND": {maximum: maximumExactInteger},
	"DCGM_FI_DEV_NVLINK_RECOVERY_ERROR_TOTAL":        {counter: true, integer: true, maximum: maximumExactInteger},
	"DCGM_FI_DEV_NVLINK_RECOVERY_FAILED_TOTAL":       {counter: true, integer: true, maximum: maximumExactInteger},
}

// DCGMDevice contains readings for one physical GPU. Samples are keyed by
// canonical DCGM field name, using the units documented for that field.
// PCIAddress is empty when the exporter did not supply the physical address.
type DCGMDevice struct {
	UUID       string
	PCIAddress string
	Samples    map[string]float64
}

// DCGMSnapshot groups the physical GPU readings from one request.
// ObservedAt is when the agent began the scrape, since the exporter caches readings.
type DCGMSnapshot struct {
	ObservedAt time.Time
	Devices    []DCGMDevice
}

// CollectDCGM reads one snapshot from an operator-configured local exporter.
// It accepts a numeric loopback address and never follows redirects or uses a proxy.
func CollectDCGM(ctx context.Context, endpoint netip.AddrPort, timeout time.Duration) (DCGMSnapshot, error) {
	if !endpoint.IsValid() || !endpoint.Addr().IsLoopback() || endpoint.Addr().Zone() != "" || endpoint.Port() == 0 || timeout <= 0 {
		return DCGMSnapshot{}, ErrDCGMConfiguration
	}
	transport := &http.Transport{DisableKeepAlives: true, MaxResponseHeaderBytes: 16 * 1024}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint.String()+"/metrics", nil)
	if err != nil {
		return DCGMSnapshot{}, ErrDCGMConfiguration
	}
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	started := time.Now().UTC()
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return DCGMSnapshot{}, ctx.Err()
		}
		return DCGMSnapshot{}, ErrDCGMUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DCGMSnapshot{}, ErrDCGMUnavailable
	}
	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" || params["version"] != "" && params["version"] != "0.0.4" {
		return DCGMSnapshot{}, ErrDCGMInvalid
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumDCGMBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return DCGMSnapshot{}, ctx.Err()
		}
		return DCGMSnapshot{}, ErrDCGMUnavailable
	}
	devices, err := parseDCGM(body)
	if err != nil {
		return DCGMSnapshot{}, err
	}
	return DCGMSnapshot{ObservedAt: started, Devices: devices}, nil
}

func parseDCGM(body []byte) ([]DCGMDevice, error) {
	if len(body) > maximumDCGMBytes || bytes.Count(body, []byte("\n")) > maximumDCGMLines || !utf8.Valid(body) {
		return nil, ErrDCGMInvalid
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return nil, ErrDCGMInvalid
	}
	devices := map[string]*DCGMDevice{}
	pciOwners := map[string]string{}
	for name, field := range dcgmFields {
		family := families[name]
		if family == nil {
			continue
		}
		expected := dto.MetricType_GAUGE
		if field.counter {
			expected = dto.MetricType_COUNTER
		}
		if family.GetType() != expected {
			return nil, ErrDCGMInvalid
		}
		seen := map[string]bool{}
		for _, metric := range family.Metric {
			uuid, address, err := dcgmPhysicalIdentity(metric)
			if err != nil {
				return nil, err
			}
			if uuid == "" {
				continue
			}
			if seen[uuid] || metric.TimestampMs != nil {
				return nil, ErrDCGMInvalid
			}
			seen[uuid] = true
			device := devices[uuid]
			if device == nil {
				device = &DCGMDevice{UUID: uuid}
				devices[uuid] = device
			}
			if address != "" {
				if (device.PCIAddress != "" && device.PCIAddress != address) ||
					(pciOwners[address] != "" && pciOwners[address] != uuid) {
					return nil, ErrDCGMInvalid
				}
				device.PCIAddress = address
				pciOwners[address] = uuid
			}
			value := metric.GetGauge().GetValue()
			if field.counter {
				value = metric.GetCounter().GetValue()
			}
			// The I64 sentinels round to the same float in Prometheus text parsing.
			if value == float64(dcgmBlankInteger) || value >= dcgmBlankDouble && value <= dcgmBlankDouble+3 {
				continue
			}
			if math.IsNaN(value) || value < field.minimum || value > field.maximum || field.integer && math.Trunc(value) != value {
				return nil, ErrDCGMInvalid
			}
			if device.Samples == nil {
				device.Samples = make(map[string]float64)
			}
			device.Samples[name] = value
		}
	}
	result := make([]DCGMDevice, 0, len(devices))
	for _, device := range devices {
		if len(device.Samples) > 0 {
			result = append(result, *device)
		}
	}
	if len(result) == 0 {
		return nil, ErrDCGMNoSamples
	}
	slices.SortFunc(result, func(a, b DCGMDevice) int { return strings.Compare(a.UUID, b.UUID) })
	return result, nil
}

func dcgmPhysicalIdentity(metric *dto.Metric) (string, string, error) {
	var uuid, address string
	var haveUUID bool
	for _, label := range metric.Label {
		key := label.GetName()
		switch key {
		case "pod", "container", "namespace", "pod_name", "container_name", "pod_namespace", "pod_uid", "vgpu", "nvlink", "nvswitch":
			return "", "", nil
		}
		if strings.HasPrefix(key, "GPU_I_") || strings.HasPrefix(key, "GPU_CI_") || strings.HasPrefix(key, "dra_") || strings.HasPrefix(key, "hpc_") || strings.HasPrefix(key, "pod_label_") {
			return "", "", nil
		}
		if key == "UUID" || key == "uuid" {
			if haveUUID {
				return "", "", ErrDCGMInvalid
			}
			uuid, haveUUID = label.GetValue(), true
		}
		if key == "pci_bus_id" {
			address = label.GetValue()
		}
	}
	if strings.HasPrefix(uuid, "MIG-") {
		return "", "", nil
	}
	if !physicalGPUUUID.MatchString(uuid) {
		return "", "", ErrDCGMInvalid
	}
	if address != "" {
		address = canonicalPCIAddress(address)
		if !strings.Contains(address, ".") {
			return "", "", ErrDCGMInvalid
		}
	}
	return "GPU-" + strings.ToLower(uuid[4:]), address, nil
}
