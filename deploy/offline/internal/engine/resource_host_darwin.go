package engine

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var availableMemoryPattern = regexp.MustCompile(`(?m)^System-wide memory free percentage: ([0-9]+)%$`)

type hostCounterOutput struct {
	bytes.Buffer
	remaining int64
}

func (w *hostCounterOutput) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.remaining {
		return 0, failure("OFFLINE_ENGINE_HOST_COUNTER_OUTPUT_LIMIT")
	}
	w.remaining -= int64(len(raw))
	return w.Buffer.Write(raw)
}
func hostCommand(ctx context.Context, budget int64, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "LC_ALL=C"}
	output := &hostCounterOutput{remaining: budget}
	command.Stdout = output
	command.Stderr = output
	if command.Run() != nil {
		return nil, failure("OFFLINE_ENGINE_HOST_COUNTER_UNAVAILABLE")
	}
	return output.Bytes(), nil
}
func readHostResources(ctx context.Context, budget int64) (HostResources, error) {
	var result HostResources
	totalRaw, e := hostCommand(ctx, budget, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if e != nil {
		return result, e
	}
	total, e := strconv.ParseInt(strings.TrimSpace(string(totalRaw)), 10, 64)
	if e != nil || total <= 0 {
		return result, failure("OFFLINE_ENGINE_HOST_COUNTER_UNAVAILABLE")
	}
	raw, e := hostCommand(ctx, budget, "/usr/bin/memory_pressure", "-Q")
	if e != nil {
		return result, e
	}
	matches := availableMemoryPattern.FindAllSubmatch(raw, -1)
	if len(matches) != 1 {
		return result, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
	}
	percent, e := strconv.ParseInt(string(matches[0][1]), 10, 64)
	if e != nil || percent < 0 || percent > 100 {
		return result, failure("OFFLINE_ENGINE_HOST_MEMORY_COUNTER_INVALID")
	}
	raw, e = hostCommand(ctx, budget, "/usr/sbin/sysctl", "-n", "vm.loadavg")
	if e != nil {
		return result, e
	}
	load, e := parseLoad(string(raw))
	if e != nil {
		return result, e
	}
	result = HostResources{MemoryTotalBytes: total, MemoryAvailableBytes: total / 100 * percent, LogicalCPUs: int64(runtime.NumCPU()), LoadOneMinute: load, Source: "macOS_readonly_pressure_query_and_sysctl"}
	return result, nil
}
