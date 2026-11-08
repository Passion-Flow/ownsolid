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

var errLinuxChildRSSUnavailable = errors.New("Linux child RSS unavailable")

func childResidentBytes(parent context.Context, pid int, probeLimit time.Duration) (int64, error) {
	if pid < 1 {
		return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
	}
	proc := "/proc/" + strconv.Itoa(pid)
	raw, err := os.ReadFile(proc + "/status")
	if err != nil {
		return 0, linuxChildSampleError(pid, err)
	}
	resident, err := linuxChildResidentBytes(raw)
	if !errors.Is(err, errLinuxChildRSSUnavailable) {
		return resident, err
	}
	// exit_mm can remove VmRSS before exit_notify makes the task a zombie.
	stat, err := os.ReadFile(proc + "/stat")
	if err != nil {
		return 0, linuxChildSampleError(pid, err)
	}
	if linuxTaskExiting(stat) {
		return 0, errProcessorExitSampleUnavailable
	}
	return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
}

func linuxTaskExiting(raw []byte) bool {
	const (
		flagsIndex = 6 // Field 9, after the PID and parenthesized command name.
		pfExiting  = 0x00000004
	)
	stat := string(raw)
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= flagsIndex {
		return false
	}
	flags, err := strconv.ParseUint(fields[flagsIndex], 10, 32)
	return err == nil && flags&pfExiting != 0
}

func linuxChildSampleError(pid int, err error) error {
	// The child can disappear before open (ENOENT) or after open (ESRCH).
	// Probe only this owned positive PID, without signaling it. A live or
	// uninspectable PID remains a failed sample, even if it exits shortly after.
	if pid > 0 && (errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)) &&
		errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return errProcessorExitSampleUnavailable
	}
	return err
}

func linuxChildResidentBytes(raw []byte) (int64, error) {
	var state string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				state = fields[1]
			}
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
	switch state {
	case "Z", "X":
		return 0, errProcessorExitSampleUnavailable
	case "R", "S", "D", "T", "t", "I":
		return 0, errLinuxChildRSSUnavailable
	default:
		return 0, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
	}
}
