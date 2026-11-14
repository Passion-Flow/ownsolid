package knowledge

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

func childResidentBytes(parent context.Context, pid int, probeLimit time.Duration) (int64, error) {
	raw, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if e != nil {
		return 0, e
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[2] != "kB" {
				break
			}
			value, e := strconv.ParseInt(fields[1], 10, 64)
			if e != nil || value < 0 {
				return 0, failure("KNOWLEDGE_WORKER_MEMORY_MONITOR_FAILED")
			}
			return value * 1024, nil
		}
	}
	return 0, failure("KNOWLEDGE_WORKER_MEMORY_MONITOR_FAILED")
}
