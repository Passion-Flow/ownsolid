package discovery

import "testing"

func TestAdviceForNVIDIAXID(t *testing.T) {
	expected := map[uint32]GPUFaultSeverity{
		13: GPUFaultWarning, 31: GPUFaultWarning, 32: GPUFaultWarning,
		43: GPUFaultInfo, 45: GPUFaultInfo, 48: GPUFaultCritical,
		63: GPUFaultInfo, 64: GPUFaultCritical, 74: GPUFaultWarning,
		79: GPUFaultCritical, 92: GPUFaultWarning, 94: GPUFaultWarning,
		95: GPUFaultCritical, 119: GPUFaultCritical, 120: GPUFaultCritical,
		140: GPUFaultCritical, 154: GPUFaultInfo, 155: GPUFaultWarning,
	}
	if len(expected) != len(nvidiaXIDAdvice) {
		t.Fatal("each Xid rule needs an explicit severity test")
	}
	for code, severity := range expected {
		advice := AdviceForNVIDIAXID(code)
		if advice.Severity != severity || advice.Summary == "" || advice.Action == "" {
			t.Errorf("Xid %d: got %+v, want %s with action", code, advice, severity)
		}
	}
	if advice := AdviceForNVIDIAXID(22013); advice.Severity != GPUFaultWarning || advice.Summary != "Unclassified NVIDIA driver event" {
		t.Fatalf("unknown Xid incorrectly classified: %+v", advice)
	}
}

func TestAdviceForDCGMIncident(t *testing.T) {
	expected := map[string]GPUFaultSeverity{
		"DCGM_FR_ROW_REMAP_FAILURE":  GPUFaultCritical,
		"DCGM_FR_UNCONTAINED_ERROR":  GPUFaultCritical,
		"DCGM_FR_CONTAINED_ERROR":    GPUFaultWarning,
		"DCGM_FR_NVLINK_DOWN":        GPUFaultWarning,
		"DCGM_FR_GPU_RECOVERY_RESET": GPUFaultCritical,
		"DCGM_FR_THERMAL_VIOLATIONS": GPUFaultWarning,
	}
	if len(expected) != len(dcgmIncidentAdvice) {
		t.Fatal("each DCGM rule needs an explicit severity test")
	}
	for code, severity := range expected {
		advice, ok := AdviceForDCGMIncident(code)
		if !ok || advice.Severity != severity || advice.Summary == "" || advice.Action == "" {
			t.Errorf("%s: got %+v, want %s with action", code, advice, severity)
		}
	}
	for _, code := range []string{"DCGM_FI_DEV_GPU_TEMP_CELSIUS", "DCGM_FI_DEV_ECC_DBE_VOL_TOTAL", "DCGM_FR_OK", ""} {
		if advice, ok := AdviceForDCGMIncident(code); ok || advice != (GPUFaultAdvice{}) {
			t.Errorf("non-incident %q incorrectly classified: %+v", code, advice)
		}
	}
}
