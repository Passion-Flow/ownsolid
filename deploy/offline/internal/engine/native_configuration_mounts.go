package engine

import (
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
)

const maximumNativeMountInfoBytes = 1 << 20

// Kernel mount checks complement, but cannot replace, the host Engine's exact
// volume labels and consumer checks. A container cannot observe a Docker name.
// No endpoint, shell, process or arbitrary Engine execution is added here.
func VerifyNativeConfigurationMounts(source, target, action string) error {
	bad := failure("OFFLINE_ENGINE_NATIVE_CONFIGURATION_MOUNTS_REFUSED")
	if runtime.GOOS != "linux" {
		return bad
	}
	f, e := os.Open("/proc/self/mountinfo")
	if e != nil {
		return bad
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, maximumNativeMountInfoBytes+1))
	if e != nil || len(raw) > maximumNativeMountInfoBytes {
		return bad
	}
	return validateNativeConfigurationMounts(string(raw), source, target, action)
}

func validateNativeConfigurationMounts(raw, source, target, action string) error {
	bad := failure("OFFLINE_ENGINE_NATIVE_CONFIGURATION_MOUNTS_REFUSED")
	if action != "install" && action != "inspect" && action != "recover" {
		return bad
	}
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	sources, targets := 0, 0
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, " - ")
		if len(parts) != 2 {
			return bad
		}
		left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(left) < 6 || len(right) < 3 {
			return bad
		}
		name := decode.Replace(left[4])
		options := strings.Split(left[5], ",")
		if name == source {
			sources++
			if !slices.Contains(options, "ro") {
				return bad
			}
		}
		if name == target {
			targets++
			if action != "inspect" && !slices.Contains(options, "rw") {
				return bad
			}
			if !slices.Contains([]string{"ext4", "xfs", "btrfs"}, right[0]) {
				return bad
			}
		}
		if strings.HasPrefix(name, target+"/") || strings.HasPrefix(name, source+"/") {
			return bad
		}
	}
	if targets != 1 || action == "install" && sources != 1 || sources > 1 {
		return bad
	}
	return nil
}
