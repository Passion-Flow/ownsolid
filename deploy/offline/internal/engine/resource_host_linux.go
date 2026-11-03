package engine

import (
	"context"
	"io"
	"os"
	"runtime"
)

func hostFile(name string, budget int64) ([]byte, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, failure("OFFLINE_ENGINE_HOST_COUNTER_UNAVAILABLE")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, budget+1))
	if e != nil || int64(len(raw)) > budget {
		return nil, failure("OFFLINE_ENGINE_HOST_COUNTER_OUTPUT_LIMIT")
	}
	return raw, nil
}
func readHostResources(ctx context.Context, budget int64) (HostResources, error) {
	var result HostResources
	if ctx.Err() != nil {
		return result, failure("OFFLINE_ENGINE_RESOURCE_OBSERVATION_INTERRUPTED")
	}
	raw, e := hostFile("/proc/meminfo", budget)
	if e != nil {
		return result, e
	}
	total, available, e := parseLinuxMemory(string(raw))
	if e != nil {
		return result, e
	}
	raw, e = hostFile("/proc/loadavg", budget)
	if e != nil {
		return result, e
	}
	load, e := parseLoad(string(raw))
	if e != nil {
		return result, e
	}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_ENGINE_RESOURCE_OBSERVATION_INTERRUPTED")
	}
	return HostResources{MemoryTotalBytes: total, MemoryAvailableBytes: available, LogicalCPUs: int64(runtime.NumCPU()), LoadOneMinute: load, Source: "Linux_proc_MemAvailable_and_loadavg"}, nil
}
