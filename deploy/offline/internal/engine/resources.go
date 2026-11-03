package engine

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const minimumResourceReservePercent = 30
const maximumResourceReservePercent = 90
const millicoresPerCore = 1000

type ResourceRequirement struct {
	MemoryBytes int64 `json:"memory_bytes"`
	MilliCPU    int64 `json:"milli_cpu"`
}
type HostResources struct {
	MemoryTotalBytes     int64   `json:"memory_total_bytes"`
	MemoryAvailableBytes int64   `json:"memory_available_bytes"`
	LogicalCPUs          int64   `json:"logical_cpus"`
	LoadOneMinute        float64 `json:"load_one_minute"`
	Source               string  `json:"source"`
}
type ResourceAdmission struct {
	Status              string              `json:"status"`
	ObservedAt          string              `json:"observed_at"`
	Requirement         ResourceRequirement `json:"requirement"`
	ReservePercent      int                 `json:"reserve_percent"`
	EngineCapacity      ResourceRequirement `json:"engine_capacity"`
	RunningReservations ResourceRequirement `json:"running_reservations"`
	Host                HostResources       `json:"host"`
	Qualification       string              `json:"qualification"`
}

func addResource(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
func remainingResource(total int64, reserve int) int64 {
	return total/100*int64(100-reserve) + (total%100)*int64(100-reserve)/100
}
func validHost(h HostResources) bool {
	return h.MemoryTotalBytes > 0 && h.MemoryAvailableBytes >= 0 && h.MemoryAvailableBytes <= h.MemoryTotalBytes && h.LogicalCPUs > 0 && h.LogicalCPUs <= math.MaxInt64/millicoresPerCore && h.LoadOneMinute >= 0 && !math.IsInf(h.LoadOneMinute, 0) && !math.IsNaN(h.LoadOneMinute) && h.Source != ""
}
func (c *Client) AdmitRuntimeResources(ctx context.Context, need ResourceRequirement, reserve int) (ResourceAdmission, error) {
	return c.admitRuntimeResources(ctx, need, reserve, readHostResources)
}
func (c *Client) admitRuntimeResources(ctx context.Context, need ResourceRequirement, reserve int, hostReader func(context.Context, int64) (HostResources, error)) (ResourceAdmission, error) {
	result := ResourceAdmission{Status: "resource_admission_refused", Requirement: need, ReservePercent: reserve, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Qualification: "point_in_time_readonly_admission_not_reservation_GPU_or_target_qualification"}
	if need.MemoryBytes <= 0 || need.MilliCPU <= 0 || reserve < minimumResourceReservePercent || reserve > maximumResourceReservePercent {
		return result, failure("OFFLINE_ENGINE_RESOURCE_REQUIREMENT_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.config.RequestSeconds)*time.Second)
	defer cancel()
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/info", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, e
	}
	var info struct {
		MemTotal int64
		NCPU     int64
	}
	if status != http.StatusOK || wire.UniqueJSON(raw, &info) != nil || info.MemTotal <= 0 || info.NCPU <= 0 || info.NCPU > math.MaxInt64/millicoresPerCore {
		return result, failure("OFFLINE_ENGINE_RESOURCE_CAPACITY_INVALID")
	}
	result.EngineCapacity = ResourceRequirement{MemoryBytes: info.MemTotal, MilliCPU: info.NCPU * millicoresPerCore}
	raw, status, e = c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/json", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, e
	}
	var objects []struct {
		ID    string `json:"Id"`
		State string
	}
	if status != http.StatusOK || wire.UniqueJSON(raw, &objects) != nil || objects == nil {
		return result, failure("OFFLINE_ENGINE_RESOURCE_INVENTORY_INVALID")
	}
	seen := map[string]bool{}
	for _, object := range objects {
		if !containerIdentity.MatchString(object.ID) || seen[object.ID] || object.State != "running" {
			return result, failure("OFFLINE_ENGINE_RESOURCE_INVENTORY_INVALID")
		}
		seen[object.ID] = true
		raw, status, e = c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+object.ID+"/json", nil, nil, c.config.RequestSeconds)
		if e != nil {
			return result, e
		}
		// Decode only the required counters, never return environment, argv or paths.
		var resources struct {
			ID    string `json:"Id"`
			State struct {
				Running          bool
				Restarting, Dead bool
			}
			HostConfig struct{ Memory, NanoCpus int64 }
		}
		if status != http.StatusOK || wire.UniqueJSON(raw, &resources) != nil || resources.ID != object.ID || !resources.State.Running || resources.State.Restarting || resources.State.Dead || resources.HostConfig.Memory <= 0 || resources.HostConfig.NanoCpus <= 0 {
			return result, failure("OFFLINE_ENGINE_UNBOUNDED_OR_CHANGED_RUNNING_RESOURCES")
		}
		cpu := resources.HostConfig.NanoCpus / nanoCPUPerMilliCPUResource
		if resources.HostConfig.NanoCpus%nanoCPUPerMilliCPUResource != 0 {
			cpu++
		}
		var ok bool
		result.RunningReservations.MemoryBytes, ok = addResource(result.RunningReservations.MemoryBytes, resources.HostConfig.Memory)
		if !ok {
			return result, failure("OFFLINE_ENGINE_RESOURCE_RESERVATION_OVERFLOW")
		}
		result.RunningReservations.MilliCPU, ok = addResource(result.RunningReservations.MilliCPU, cpu)
		if !ok {
			return result, failure("OFFLINE_ENGINE_RESOURCE_RESERVATION_OVERFLOW")
		}
	}
	memory, ok := addResource(result.RunningReservations.MemoryBytes, need.MemoryBytes)
	if !ok || memory > remainingResource(result.EngineCapacity.MemoryBytes, reserve) {
		return result, failure("OFFLINE_ENGINE_MEMORY_HEADROOM_REQUIRED")
	}
	cpu, ok := addResource(result.RunningReservations.MilliCPU, need.MilliCPU)
	if !ok || cpu > remainingResource(result.EngineCapacity.MilliCPU, reserve) {
		return result, failure("OFFLINE_ENGINE_CPU_HEADROOM_REQUIRED")
	}
	host, e := hostReader(ctx, c.config.ResponseBytes)
	if e != nil {
		return result, e
	}
	result.Host = host
	if !validHost(host) {
		return result, failure("OFFLINE_ENGINE_HOST_RESOURCE_COUNTERS_INVALID")
	}
	requiredAvailable := host.MemoryTotalBytes - remainingResource(host.MemoryTotalBytes, reserve)
	requiredAvailable, ok = addResource(requiredAvailable, need.MemoryBytes)
	if !ok || host.MemoryAvailableBytes < requiredAvailable {
		return result, failure("OFFLINE_ENGINE_HOST_MEMORY_HEADROOM_REQUIRED")
	}
	if host.LoadOneMinute+float64(need.MilliCPU)/millicoresPerCore > float64(host.LogicalCPUs)*float64(100-reserve)/100 {
		return result, failure("OFFLINE_ENGINE_HOST_CPU_HEADROOM_REQUIRED")
	}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_ENGINE_RESOURCE_OBSERVATION_INTERRUPTED")
	}
	result.Status = "passed_point_in_time_resource_admission"
	return result, nil
}

const nanoCPUPerMilliCPUResource = 1000000

func parseLoad(raw string) (float64, error) {
	bad := func() (float64, error) { return 0, failure("OFFLINE_ENGINE_HOST_LOAD_COUNTER_INVALID") }
	fields := strings.Fields(strings.Trim(strings.TrimSpace(raw), "{} "))
	if len(fields) != 3 && len(fields) != 5 {
		return bad()
	}
	var first float64
	for i := 0; i < 3; i++ {
		value, e := strconv.ParseFloat(fields[i], 64)
		if e != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return bad()
		}
		if i == 0 {
			first = value
		}
	}
	if len(fields) == 5 {
		running, total, ok := strings.Cut(fields[3], "/")
		if !ok {
			return bad()
		}
		a, e := strconv.ParseUint(running, 10, 64)
		if e != nil {
			return bad()
		}
		b, e := strconv.ParseUint(total, 10, 64)
		if e != nil || b == 0 || a > b {
			return bad()
		}
		if _, e = strconv.ParseUint(fields[4], 10, 64); e != nil {
			return bad()
		}
	}
	return first, nil
}

func parseLinuxMemory(raw string) (int64, int64, error) {
	values := map[string]int64{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 0 || parts[0] != "MemTotal:" && parts[0] != "MemAvailable:" {
			continue
		}
		if len(parts) != 3 || parts[2] != "kB" {
			return 0, 0, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
		}
		if _, exists := values[parts[0]]; exists {
			return 0, 0, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
		}
		n, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || n < 0 || n > math.MaxInt64/1024 {
			return 0, 0, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
		}
		values[parts[0]] = n * 1024
	}
	total, ok := values["MemTotal:"]
	if !ok {
		return 0, 0, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
	}
	available, ok := values["MemAvailable:"]
	if !ok || total <= 0 || available > total {
		return 0, 0, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
	}
	return total, available, nil
}

// ValidateRecorded checks a historical admission's internal consistency, not current capacity.
func (a ResourceAdmission) ValidateRecorded() error {
	bad := func() error { return failure("OFFLINE_ENGINE_ADMISSION_SNAPSHOT_INVALID") }
	if a.Status != "passed_point_in_time_resource_admission" || a.ReservePercent < minimumResourceReservePercent || a.ReservePercent > maximumResourceReservePercent || a.Requirement.MemoryBytes <= 0 || a.Requirement.MilliCPU <= 0 || a.EngineCapacity.MemoryBytes <= 0 || a.EngineCapacity.MilliCPU <= 0 || !validHost(a.Host) {
		return bad()
	}
	if _, e := time.Parse(time.RFC3339Nano, a.ObservedAt); e != nil {
		return bad()
	}
	memory, ok := addResource(a.RunningReservations.MemoryBytes, a.Requirement.MemoryBytes)
	if !ok || memory > remainingResource(a.EngineCapacity.MemoryBytes, a.ReservePercent) {
		return bad()
	}
	cpu, ok := addResource(a.RunningReservations.MilliCPU, a.Requirement.MilliCPU)
	if !ok || cpu > remainingResource(a.EngineCapacity.MilliCPU, a.ReservePercent) {
		return bad()
	}
	available, ok := addResource(a.Host.MemoryTotalBytes-remainingResource(a.Host.MemoryTotalBytes, a.ReservePercent), a.Requirement.MemoryBytes)
	if !ok || a.Host.MemoryAvailableBytes < available || a.Host.LoadOneMinute+float64(a.Requirement.MilliCPU)/millicoresPerCore > float64(a.Host.LogicalCPUs)*float64(100-a.ReservePercent)/100 {
		return bad()
	}
	return nil
}
