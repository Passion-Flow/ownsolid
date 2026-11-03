package inference

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestGPUProbeNeverTreatsInvalidFieldsOrOtherProcessesAsFree(t *testing.T) {
	id := "GPU-00000000-0000-0000-0000-000000000003"
	valid := id + ", NVIDIA Test GPU, 580.65.06, 16384, 16000, 7.5, [N/A]\n"
	gpus, e := parseGPUs([]byte(valid), time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateGPU(gpus, nil, id); e != nil {
		t.Fatal(e)
	}
	if e = ValidateGPU(gpus, []string{id}, id); e == nil || e.Error() != "INFERENCE_GPU_BUSY" {
		t.Fatal("unowned compute process considered free", e)
	}
	gpus[0].DriverVersion = "570.10"
	if e = ValidateGPU(gpus, nil, id); e == nil || e.Error() != "INFERENCE_DRIVER_UNSUPPORTED" {
		t.Fatal(e)
	}
	for _, body := range []string{valid + valid, id + ", NVIDIA Test GPU, 580.65, 16384, N/A, 7.5, Disabled\n", id + ", NVIDIA Test GPU, 580.65, 16384, 16000, NaN, Disabled\n", "No devices found\n"} {
		if _, e = parseGPUs([]byte(body), time.Now()); e == nil {
			t.Fatal("invalid probe established GPU availability")
		}
	}
	if _, e = parseComputeProcesses([]byte(id + ", N/A\n")); e == nil {
		t.Fatal("unavailable process list considered free")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		if _, _, e = ProbeNVIDIA(context.Background()); e == nil || e.Error() != "INFERENCE_PLATFORM_UNSUPPORTED" {
			t.Fatal("host substituted for NVIDIA target", e)
		}
	}
}
