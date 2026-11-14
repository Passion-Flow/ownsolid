package inference

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/discovery"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type commandOutput struct {
	bytes.Buffer
	maximum  int
	exceeded bool
}

func (b *commandOutput) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.maximum {
		b.exceeded = true
		return len(value), nil
	}
	return b.Buffer.Write(value)
}
func protectedNVIDIAProgram() (string, error) {
	path, e := filepath.EvalSymlinks("/usr/bin/nvidia-smi")
	if e != nil || !strings.HasPrefix(path, "/usr/") {
		return "", fail("INFERENCE_NVIDIA_UNAVAILABLE")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return "", fail("INFERENCE_NVIDIA_UNAVAILABLE")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 {
			return "", fail("INFERENCE_NVIDIA_UNAVAILABLE")
		}
		if current == path && !info.Mode().IsRegular() || current != path && !info.IsDir() {
			return "", fail("INFERENCE_NVIDIA_UNAVAILABLE")
		}
		if current == "/" {
			break
		}
	}
	return path, nil
}
func nvidiaOutput(ctx context.Context, program string, fields string, policy settings.Policy) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, settings.Seconds(policy.GPUProbeSeconds))
	defer cancel()
	command := exec.CommandContext(bounded, program, fields, "--format=csv,noheader,nounits")
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output := &commandOutput{maximum: 65536}
	diagnostic := &commandOutput{maximum: 4096}
	command.Stdout = output
	command.Stderr = diagnostic
	if command.Run() != nil || output.exceeded || diagnostic.exceeded {
		return nil, fail("INFERENCE_NVIDIA_UNAVAILABLE")
	}
	return output.Bytes(), nil
}
func ProbeNVIDIA(ctx context.Context, policies ...settings.Policy) ([]wire.NVIDIAGPU, []string, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return nil, nil, fail("INFERENCE_PLATFORM_UNSUPPORTED")
	}
	facts, e := discovery.Collect("/")
	if e != nil || facts.OSID != "ubuntu" || facts.OSVersion != "24.04" {
		return nil, nil, fail("INFERENCE_PLATFORM_UNSUPPORTED")
	}
	if facts.MemoryTotalBytes < wire.ApprovedInferenceRecipe().MinimumHostMemoryBytes {
		return nil, nil, fail("INFERENCE_HOST_MEMORY_LOW")
	}
	program, e := protectedNVIDIAProgram()
	if e != nil {
		return nil, nil, e
	}
	body, e := nvidiaOutput(ctx, program, "--query-gpu=uuid,name,driver_version,memory.total,memory.free,compute_cap,mig.mode.current", policy)
	if e != nil {
		return nil, nil, e
	}
	gpus, e := parseGPUs(body, time.Now().UTC())
	if e != nil {
		return nil, nil, e
	}
	body, e = nvidiaOutput(ctx, program, "--query-compute-apps=gpu_uuid,pid", policy)
	if e != nil {
		return nil, nil, e
	}
	busy, e := parseComputeProcesses(body)
	if e != nil {
		return nil, nil, e
	}
	return gpus, busy, nil
}

var driverVersion = regexp.MustCompile(`^[0-9]{3,4}\.[0-9]{1,3}(\.[0-9]{1,3})?$`)

func parseGPUs(body []byte, observed time.Time) ([]wire.NVIDIAGPU, error) {
	if len(body) > 65536 || !utf8.Valid(body) {
		return nil, fail("INFERENCE_GPU_PROBE_INVALID")
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = 7
	out := []wire.NVIDIAGPU{}
	seen := map[string]bool{}
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil || len(out) >= 32 {
			return nil, fail("INFERENCE_GPU_PROBE_INVALID")
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		if !gpuID.MatchString(row[0]) || seen[row[0]] || !driverVersion.MatchString(row[2]) || len(row[1]) < 1 || len(row[1]) > 128 {
			return nil, fail("INFERENCE_GPU_PROBE_INVALID")
		}
		for _, r := range row[1] {
			if unicode.IsControl(r) {
				return nil, fail("INFERENCE_GPU_PROBE_INVALID")
			}
		}
		total, a := strconv.ParseInt(row[3], 10, 64)
		free, b := strconv.ParseInt(row[4], 10, 64)
		compute, c := strconv.ParseFloat(row[5], 64)
		if a != nil || b != nil || c != nil || total < 1 || total > 1048576 || free < 0 || free > total || math.IsNaN(compute) || math.IsInf(compute, 0) || compute < 1 || compute > 99 {
			return nil, fail("INFERENCE_GPU_PROBE_INVALID")
		}
		mig := row[6]
		if mig != "Disabled" && mig != "Enabled" && mig != "[N/A]" && mig != "N/A" {
			return nil, fail("INFERENCE_GPU_PROBE_INVALID")
		}
		out = append(out, wire.NVIDIAGPU{UUID: row[0], Name: row[1], DriverVersion: row[2], MemoryTotalMiB: total, MemoryFreeMiB: free, ComputeCapability: compute, MIGMode: mig, ObservedAt: observed})
		seen[row[0]] = true
	}
	if len(out) == 0 {
		return nil, fail("INFERENCE_GPU_UNAVAILABLE")
	}
	return out, nil
}
func parseComputeProcesses(body []byte) ([]string, error) {
	if len(body) > 65536 || !utf8.Valid(body) {
		return nil, fail("INFERENCE_GPU_PROBE_INVALID")
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = 2
	out := []string{}
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil || len(out) >= 256 {
			return nil, fail("INFERENCE_GPU_PROBE_INVALID")
		}
		id := strings.TrimSpace(row[0])
		pid, e := strconv.ParseInt(strings.TrimSpace(row[1]), 10, 64)
		if e != nil || !gpuID.MatchString(id) || pid < 1 {
			return nil, fail("INFERENCE_GPU_PROBE_INVALID")
		}
		out = append(out, id)
	}
	return out, nil
}
func ValidateGPU(gpus []wire.NVIDIAGPU, busy []string, id string) error {
	if !gpuID.MatchString(id) {
		return fail("INFERENCE_CONFIGURATION_INVALID")
	}
	for _, gpu := range gpus {
		if gpu.UUID != id {
			continue
		}
		recipe := wire.ApprovedInferenceRecipe()
		major, e := strconv.Atoi(strings.Split(gpu.DriverVersion, ".")[0])
		if e != nil || major < recipe.MinimumDriverMajor {
			return fail("INFERENCE_DRIVER_UNSUPPORTED")
		}
		if gpu.ComputeCapability < recipe.MinimumComputeCapability {
			return fail("INFERENCE_GPU_UNSUPPORTED")
		}
		if gpu.MIGMode == "Enabled" {
			return fail("INFERENCE_MIG_UNSUPPORTED")
		}
		if gpu.MemoryTotalMiB < recipe.MinimumGPUMemoryMiB || gpu.MemoryFreeMiB < gpu.MemoryTotalMiB*wire.InferenceFreeMemoryPercent/100 {
			return fail("INFERENCE_GPU_MEMORY_LOW")
		}
		for _, processGPU := range busy {
			if processGPU == id {
				return fail("INFERENCE_GPU_BUSY")
			}
		}
		return nil
	}
	return fail("INFERENCE_GPU_UNAVAILABLE")
}

// ValidateCommandGPU preserves the base contract and adds the explicit BF16 SM8/free-memory gate.
func ValidateCommandGPU(gpus []wire.NVIDIAGPU, busy []string, command wire.InferenceCommand) error {
	if command.Validate(command.Operation()) != nil {
		return fail("INFERENCE_CONFIGURATION_INVALID")
	}
	if e := ValidateGPU(gpus, busy, command.GPUUUID); e != nil {
		return e
	}
	if command.Derived != nil {
		recipe := wire.ApprovedDerivedServingRecipe()
		for _, gpu := range gpus {
			if gpu.UUID == command.GPUUUID {
				if gpu.ComputeCapability < recipe.MinimumComputeCapability {
					return fail("INFERENCE_GPU_UNSUPPORTED")
				}
				if gpu.MemoryFreeMiB < recipe.MinimumGPUMemoryMiB {
					return fail("INFERENCE_GPU_MEMORY_LOW")
				}
			}
		}
	}
	return nil
}
