package discovery

// GPUFaultSeverity is the urgency of a finding for an operator.
type GPUFaultSeverity string

const (
	// GPUFaultInfo describes a workload or informational event.
	GPUFaultInfo GPUFaultSeverity = "info"
	// GPUFaultWarning calls for investigation or correlation.
	GPUFaultWarning GPUFaultSeverity = "warning"
	// GPUFaultCritical calls for prompt operator attention.
	GPUFaultCritical GPUFaultSeverity = "critical"
)

// GPUFaultAdvice is advisory only. Device admission and remediation need
// authenticated evidence, verified identity and a separate policy decision.
type GPUFaultAdvice struct {
	Severity GPUFaultSeverity
	Summary  string
	Action   string
}

var nvidiaXIDAdvice = map[uint32]GPUFaultAdvice{
	13:  {GPUFaultWarning, "GPU execution exception", "Check the workload with Compute Sanitizer. If it recurs across unrelated workloads, run DCGM diagnostics."},
	31:  {GPUFaultWarning, "GPU virtual memory fault", "Investigate invalid memory access in the workload. Check the driver and device if the fault recurs across workloads."},
	32:  {GPUFaultWarning, "GPU command transfer error", "Inspect PCIe and kernel logs. Compare the driver and hardware findings before acting on the device."},
	43:  {GPUFaultInfo, "Workload stopped after a software fault", "Inspect and restart the affected workload after correcting its error."},
	45:  {GPUFaultInfo, "GPU channels were removed", "Correlate with the event that caused channel removal and follow its guidance."},
	48:  {GPUFaultCritical, "GPU double-bit ECC event", "Check for accompanying page retirement or remap events and the driver recovery action. Use field diagnostics when the cause is unclear."},
	63:  {GPUFaultInfo, "GPU memory retirement or remap was recorded", "Check related ECC events and whether the driver reports a pending remap."},
	64:  {GPUFaultCritical, "GPU memory retirement or remap failed", "Review driver recovery and remap state. Plan a maintenance window and follow the documented reset or reboot procedure."},
	74:  {GPUFaultWarning, "GPU NVLink error", "Inspect the NVLink status and peer events. Use the vendor's error-bit guidance to identify the affected endpoint."},
	79:  {GPUFaultCritical, "GPU became inaccessible on PCIe", "Correlate PCIe errors on this host and identify the physical device. Plan a host restart and escalate if the failure recurs."},
	92:  {GPUFaultWarning, "GPU corrected ECC rate warning", "Check memory error trends and the device's retirement or remap state."},
	94:  {GPUFaultWarning, "GPU memory error was contained", "Restart the affected workload. Review the pending remap and plan any required maintenance."},
	95:  {GPUFaultCritical, "GPU memory error affected device-wide operation", "Check the driver recovery action, drain affected work and follow the vendor reset procedure."},
	119: {GPUFaultCritical, "GPU system processor timed out", "Check driver and firmware evidence and follow the driver recovery action. Escalate if it recurs."},
	120: {GPUFaultCritical, "GPU system processor reported an error", "Check driver and firmware evidence and follow the driver recovery action. Escalate if it recurs."},
	140: {GPUFaultCritical, "GPU ECC recovery did not complete", "Gather memory and remap state and follow the driver recovery action before returning the device to service."},
	154: {GPUFaultInfo, "GPU recovery action changed", "Review the accompanying Xid and the current recovery action reported by the driver."},
	155: {GPUFaultWarning, "GPU reported a software-defined NVLink event", "Check whether the link transition was intentional and correlate with endpoint state before acting."},
}

var dcgmIncidentAdvice = map[string]GPUFaultAdvice{
	"DCGM_FR_ROW_REMAP_FAILURE":  nvidiaXIDAdvice[64],
	"DCGM_FR_UNCONTAINED_ERROR":  nvidiaXIDAdvice[95],
	"DCGM_FR_CONTAINED_ERROR":    nvidiaXIDAdvice[94],
	"DCGM_FR_NVLINK_DOWN":        {GPUFaultWarning, "An expected NVLink was reported down", "Check link configuration, peer state and the most recent hardware evidence."},
	"DCGM_FR_GPU_RECOVERY_RESET": {GPUFaultCritical, "Driver reports that a GPU reset is required", "Confirm the current driver recovery state and the device identity before planning a reset."},
	"DCGM_FR_THERMAL_VIOLATIONS": {GPUFaultWarning, "GPU thermal limit was reached", "Inspect cooling and the duration of thermal throttling against the device thresholds."},
}

// AdviceForNVIDIAXID returns guidance for a known or unclassified driver event.
func AdviceForNVIDIAXID(code uint32) GPUFaultAdvice {
	if advice, ok := nvidiaXIDAdvice[code]; ok {
		return advice
	}
	return GPUFaultAdvice{GPUFaultWarning, "Unclassified NVIDIA driver event", "Check the NVIDIA Xid catalogue for this driver and GPU model and correlate the surrounding events."}
}

// AdviceForDCGMIncident returns advice for a recognized DCGM health finding.
// A field sample, pass result or unknown identifier has no advice.
func AdviceForDCGMIncident(code string) (GPUFaultAdvice, bool) {
	advice, ok := dcgmIncidentAdvice[code]
	return advice, ok
}
