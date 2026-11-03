package v1

import (
	"errors"
	"math"
	"time"
)

const ManagementVersion = "1.1"
const ReadOnlyProfile = "read_only"
const SystemdComponentsProfile = "systemd_components"
const NodeExporter = "node_exporter"
const NodeExporterVersion = "1.12.1"
const NodeExporterSHA256 = "b51d8a76aa2a9156a55d501aca6276fae09e262259a5e4e831d2c2222f084e63"
const NodeExporterArchiveBytes int64 = 12168577

var componentActions = map[string]bool{"install": true, "start": true, "stop": true, "remove": true, "observe": true}

func ComponentOperation(action string) string { return "node_exporter." + action + ".v1" }
func IsComponentOperation(operation string) bool {
	for action := range maintenanceActions {
		if ComponentOperation(action) == operation {
			return true
		}
	}
	return false
}
func Capabilities(profile string) []string {
	result := []string{CollectFacts}
	if IsInferenceProfile(profile) {
		for _, action := range []string{"deploy", "stop", "remove", "observe", "verify"} {
			result = append(result, InferenceOperation(action))
		}
	}
	if IsTrainingProfile(profile) {
		result = append(result, TrainingRunOperation)
	}
	if profile == DockerModelLifecycleProfile {
		result = append(result, ModelPreparationRunOperation, DerivedInferenceOperation)
	}
	if IsManagementProfile(profile) {
		for _, action := range []string{"install", "start", "stop", "remove", "observe"} {
			result = append(result, ComponentOperation(action))
		}
	}
	if profile == SystemdMaintenanceProfile {
		for _, action := range []string{"configure", "restart", "upgrade", "rollback", "logs"} {
			result = append(result, ComponentOperation(action))
		}
	}
	return result
}
func ProfileVersion(profile string) string {
	if profile == DockerModelLifecycleProfile {
		return ModelLifecycleVersion
	}
	if profile == DockerAIProfile {
		return AIVersion
	}
	if profile == DockerInferenceProfile {
		return InferenceVersion
	}
	if profile == SystemdMaintenanceProfile {
		return MaintenanceVersion
	}
	if profile == SystemdComponentsProfile {
		return ManagementVersion
	}
	return Version
}
func KnownProfile(profile string) bool {
	return profile == ReadOnlyProfile || IsManagementProfile(profile) || IsInferenceProfile(profile)
}

type ComponentCommand struct {
	Component       string           `json:"component"`
	Version         string           `json:"version"`
	Action          string           `json:"action"`
	ArtifactSHA256  string           `json:"artifact_sha256"`
	ArchiveBytes    int64            `json:"archive_bytes"`
	Config          *ComponentConfig `json:"config,omitempty"`
	ExpectedVersion string           `json:"expected_version,omitempty"`
}

func Command(action string) ComponentCommand {
	return ComponentCommand{Component: NodeExporter, Version: NodeExporterVersion, Action: action, ArtifactSHA256: NodeExporterSHA256, ArchiveBytes: NodeExporterArchiveBytes}
}
func (c ComponentCommand) Validate(operation string) error {
	if c.Component != NodeExporter || c.Version != NodeExporterVersion || !componentActions[c.Action] || operation != ComponentOperation(c.Action) || c.ArtifactSHA256 != NodeExporterSHA256 || c.ArchiveBytes != NodeExporterArchiveBytes || c.Config != nil || c.ExpectedVersion != "" {
		return errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	return nil
}

type ComponentMetrics struct {
	CPUSecondsTotal          float64 `json:"cpu_seconds_total"`
	CPUIdleSecondsTotal      float64 `json:"cpu_idle_seconds_total"`
	MemoryTotalBytes         int64   `json:"memory_total_bytes"`
	MemoryAvailableBytes     int64   `json:"memory_available_bytes"`
	FilesystemTotalBytes     int64   `json:"filesystem_total_bytes"`
	FilesystemAvailableBytes int64   `json:"filesystem_available_bytes"`
}

func (m ComponentMetrics) Validate() error {
	const maximum = 9007199254740991
	if math.IsNaN(m.CPUSecondsTotal) || math.IsInf(m.CPUSecondsTotal, 0) || math.IsNaN(m.CPUIdleSecondsTotal) || math.IsInf(m.CPUIdleSecondsTotal, 0) || m.CPUSecondsTotal <= 0 || m.CPUIdleSecondsTotal < 0 || m.CPUIdleSecondsTotal > m.CPUSecondsTotal || m.CPUSecondsTotal > maximum || m.MemoryTotalBytes < 1 || m.MemoryTotalBytes > maximum || m.MemoryAvailableBytes < 0 || m.MemoryAvailableBytes > m.MemoryTotalBytes || m.FilesystemTotalBytes < 1 || m.FilesystemTotalBytes > maximum || m.FilesystemAvailableBytes < 0 || m.FilesystemAvailableBytes > m.FilesystemTotalBytes {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

type ComponentObservation struct {
	Component        string            `json:"component"`
	Version          string            `json:"version"`
	Managed          bool              `json:"managed"`
	State            string            `json:"state"`
	ObservedAt       time.Time         `json:"observed_at"`
	ErrorCode        string            `json:"error_code"`
	Metrics          *ComponentMetrics `json:"metrics,omitempty"`
	Config           *ComponentConfig  `json:"config,omitempty"`
	PreviousVersion  string            `json:"previous_version,omitempty"`
	MaintenanceError string            `json:"maintenance_error,omitempty"`
	MaintenanceCause string            `json:"maintenance_cause,omitempty"`
}

var ComponentErrors = map[string]bool{"": true, "COMPONENT_SYSTEMD_UNAVAILABLE": true, "COMPONENT_PRIVILEGE_REQUIRED": true, "COMPONENT_USER_INVALID": true, "COMPONENT_CONFLICT": true, "COMPONENT_PORT_CONFLICT": true, "COMPONENT_DISK_SPACE_LOW": true, "COMPONENT_ASSET_UNAVAILABLE": true, "COMPONENT_ASSET_INVALID": true, "COMPONENT_ARCHIVE_INVALID": true, "COMPONENT_ARCHITECTURE_MISMATCH": true, "COMPONENT_STATE_INVALID": true, "COMPONENT_IO_FAILED": true, "COMPONENT_SERVICE_FAILED": true, "COMPONENT_METRICS_UNAVAILABLE": true, "COMPONENT_METRICS_INVALID": true, "COMPONENT_CANCELLED": true, "COMPONENT_PROFILE_UNAVAILABLE": true}

func (o ComponentObservation) Validate() error {
	if o.Component != NodeExporter || o.Version != NodeExporterVersion || o.ObservedAt.IsZero() || o.ObservedAt.Year() < 2020 || o.ObservedAt.Year() > 2200 || !ComponentErrors[o.ErrorCode] || o.Config != nil || o.PreviousVersion != "" || o.MaintenanceError != "" || o.MaintenanceCause != "" {
		return errors.New("VALIDATION_FAILED")
	}
	return o.validateState()
}
func (o ComponentObservation) validateState() error {
	if o.State != "running" && o.State != "stopped" && o.State != "not_installed" && o.State != "unknown" {
		return errors.New("VALIDATION_FAILED")
	}
	if (o.State == "running" || o.State == "stopped") && !o.Managed || o.State == "not_installed" && o.Managed {
		return errors.New("VALIDATION_FAILED")
	}
	if o.Metrics != nil {
		if o.State != "running" || !o.Managed {
			return errors.New("VALIDATION_FAILED")
		}
		return o.Metrics.Validate()
	}
	return nil
}

type LeaseRenewal struct {
	LeaseToken string `json:"lease_token"`
}
type RenewedLease struct {
	LeaseExpiresAt       time.Time `json:"lease_expires_at"`
	ValidForMilliseconds int       `json:"valid_for_milliseconds"`
}

func IsManagementProfile(profile string) bool {
	return profile == SystemdComponentsProfile || profile == SystemdMaintenanceProfile
}
func OperationAllowed(profile, operation string) bool {
	if !KnownProfile(profile) {
		return false
	}
	for _, allowed := range Capabilities(profile) {
		if allowed == operation {
			return true
		}
	}
	return false
}
func (c ComponentCommand) ValidateForProfile(profile, operation string) error {
	if !OperationAllowed(profile, operation) {
		return errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	if profile == SystemdMaintenanceProfile {
		return c.ValidateMaintenance(operation)
	}
	return c.Validate(operation)
}
func (o ComponentObservation) ValidateForProfile(profile string) error {
	if profile == SystemdMaintenanceProfile {
		return o.ValidateMaintenance()
	}
	if profile == SystemdComponentsProfile {
		return o.Validate()
	}
	return errors.New("AGENT_PROFILE_REJECTED")
}
