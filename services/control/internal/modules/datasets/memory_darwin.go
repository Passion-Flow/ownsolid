package datasets

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func childResidentBytes(parent context.Context, pid int, probeLimit time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(parent, probeLimit)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/ps", "-o", "rss=", "-p", strconv.Itoa(pid))
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	raw, e := command.Output()
	if e != nil {
		return 0, e
	}
	value, e := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if e != nil || value < 0 {
		return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
	}
	return value * 1024, nil
}
