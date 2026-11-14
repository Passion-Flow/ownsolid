package inference

import (
	"io"
	"os"
	"strconv"
	"strings"
)

// CheckAvailableHostMemory is read-only and runs only before a new allocation/start.
func CheckAvailableHostMemory() error {
	file, e := os.Open("/proc/meminfo")
	if e != nil {
		return fail("INFERENCE_HOST_MEMORY_LOW")
	}
	defer file.Close()
	body, e := io.ReadAll(io.LimitReader(file, 65537))
	if e != nil || len(body) > 65536 {
		return fail("INFERENCE_HOST_MEMORY_LOW")
	}
	return validateAvailableHostMemory(string(body))
}
func validateAvailableHostMemory(body string) error {
	seen := false
	var available uint64
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemAvailable:" {
			continue
		}
		if seen || len(fields) != 3 || fields[2] != "kB" {
			return fail("INFERENCE_HOST_MEMORY_LOW")
		}
		var e error
		available, e = strconv.ParseUint(fields[1], 10, 54)
		if e != nil {
			return fail("INFERENCE_HOST_MEMORY_LOW")
		}
		seen = true
	}
	if !seen || available < 9*1024*1024 {
		return fail("INFERENCE_HOST_MEMORY_LOW")
	}
	return nil
}
