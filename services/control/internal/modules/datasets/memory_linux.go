package datasets

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func childResidentBytes(parent context.Context, pid int, probeLimit time.Duration) (int64, error) {
	if pid < 1 {
		return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
	}
	raw, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if e != nil {
		return 0, linuxChildSampleError(pid, e)
	}
	return linuxChildResidentBytes(raw)
}

func linuxChildSampleError(pid int, err error) error {
	// A missing proc entry alone could also mean an unavailable proc mount.
	// Probe only this owned positive PID, without signaling it. A live or
	// uninspectable PID remains a failed sample, even if it exits shortly after.
	if pid > 0 && errors.Is(err, os.ErrNotExist) && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return errProcessorExitSampleUnavailable
	}
	return err
}

func linuxChildResidentBytes(raw []byte) (int64, error) {
	terminal := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			terminal = len(fields) >= 2 && (fields[1] == "Z" || fields[1] == "X")
		}
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[2] != "kB" {
				return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
			}
			value, e := strconv.ParseInt(fields[1], 10, 64)
			if e != nil || value < 0 || value > (1<<63-1)/1024 {
				return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
			}
			return value * 1024, nil
		}
	}
	if terminal {
		return 0, errProcessorExitSampleUnavailable
	}
	return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
}
