//go:build linux

package discovery

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
)

func limitedFile(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("DISCOVERY_SOURCE_UNAVAILABLE")
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// Collect reads this node's facts.
func Collect(diskPath string) (wire.Facts, error) {
	return CollectReporting(context.Background(), diskPath)
}

// CollectReporting reads this node's facts and reports each step to the task in ctx.
func CollectReporting(ctx context.Context, diskPath string) (wire.Facts, error) {
	var f wire.Facts
	if !filepath.IsAbs(diskPath) {
		return f, errors.New("DISK_PATH_INVALID")
	}
	path, e := filepath.EvalSymlinks(diskPath)
	if e != nil {
		return f, errors.New("DISK_PATH_UNAVAILABLE")
	}
	info, e := os.Stat(path)
	if e != nil || !info.IsDir() {
		return f, errors.New("DISK_PATH_UNAVAILABLE")
	}
	progress.Step(ctx, wire.ProgressStepReadSystem)
	b, e := limitedFile("/etc/os-release", 65536)
	if e != nil {
		return f, e
	}
	f.OSID, f.OSVersion, e = parseOS(b)
	if e != nil {
		return f, e
	}
	progress.Info(ctx, "Operating system: %s %s", f.OSID, f.OSVersion)
	progress.Step(ctx, wire.ProgressStepReadMemory)
	b, e = limitedFile("/proc/meminfo", 1048576)
	if e != nil {
		return f, e
	}
	f.MemoryTotalBytes, e = parseMemory(b)
	if e != nil {
		return f, e
	}
	f.Hostname, e = os.Hostname()
	if e != nil {
		return f, errors.New("HOSTNAME_UNAVAILABLE")
	}
	progress.Info(ctx, "Memory: %d bytes", f.MemoryTotalBytes)
	progress.Step(ctx, wire.ProgressStepReadDisk)
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil || stat.Bsize <= 0 {
		return f, errors.New("DISK_DISCOVERY_FAILED")
	}
	const max = 9007199254740991
	if stat.Blocks > max/uint64(stat.Bsize) || stat.Bavail > stat.Blocks {
		return f, errors.New("DISK_DISCOVERY_INVALID")
	}
	f.DiskPath = path
	f.DiskTotalBytes = int64(stat.Blocks) * stat.Bsize
	f.DiskAvailableBytes = int64(stat.Bavail) * stat.Bsize
	progress.Info(ctx, "Disk at %s: %d bytes, %d available", path, f.DiskTotalBytes, f.DiskAvailableBytes)
	f.Architecture = runtime.GOARCH
	f.LogicalCPUs = runtime.NumCPU()
	f.ObservedAt = time.Now().UTC()
	f.AgentVersion = wire.AgentVersion
	return f, f.Validate()
}
