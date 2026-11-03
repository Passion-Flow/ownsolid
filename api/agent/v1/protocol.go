// Package v1 defines the shared machine protocol, without runtime or database dependencies.
package v1

import (
	"errors"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Version = "1.0"
const AgentVersion = "0.10.0"
const CollectFacts = "collect_facts.v1"

type Facts struct {
	OSID               string    `json:"os_id"`
	OSVersion          string    `json:"os_version"`
	Architecture       string    `json:"architecture"`
	Hostname           string    `json:"hostname"`
	LogicalCPUs        int       `json:"logical_cpus"`
	MemoryTotalBytes   int64     `json:"memory_total_bytes"`
	DiskPath           string    `json:"disk_path"`
	DiskTotalBytes     int64     `json:"disk_total_bytes"`
	DiskAvailableBytes int64     `json:"disk_available_bytes"`
	ObservedAt         time.Time `json:"observed_at"`
	AgentVersion       string    `json:"agent_version"`
}
type Handshake struct {
	ProtocolVersion  string                `json:"protocol_version"`
	Capabilities     []string              `json:"capabilities"`
	Facts            Facts                 `json:"facts"`
	ExecutionProfile string                `json:"execution_profile,omitempty"`
	Component        *ComponentObservation `json:"component,omitempty"`
	Inference        *InferenceObservation `json:"inference,omitempty"`
}
type Enrollment struct {
	JoinToken string `json:"join_token"`
	Handshake
}
type Enrolled struct {
	NodeID           string `json:"node_id"`
	Credential       string `json:"credential"`
	ProtocolVersion  string `json:"protocol_version"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
}
type Disposition struct {
	Status     string    `json:"status"`
	ServerTime time.Time `json:"server_time"`
}
type Lease struct {
	Preparation    *lifecycle.Command `json:"model_preparation,omitempty"`
	TaskID         string             `json:"task_id"`
	Operation      string             `json:"operation"`
	LeaseToken     string             `json:"lease_token"`
	LeaseExpiresAt time.Time          `json:"lease_expires_at"`
	Attempt        int                `json:"attempt"`
	Component      *ComponentCommand  `json:"component,omitempty"`
	Inference      *InferenceCommand  `json:"inference,omitempty"`
	Training       *training.Command  `json:"training,omitempty"`
}
type Claim struct {
	Lease *Lease `json:"lease"`
}
type Result struct {
	LeaseToken string                `json:"lease_token"`
	Outcome    string                `json:"outcome"`
	ErrorCode  string                `json:"error_code"`
	Facts      *Facts                `json:"facts,omitempty"`
	Component  *ComponentObservation `json:"component,omitempty"`
	Logs       *ComponentLogs        `json:"logs,omitempty"`
	Inference  *InferenceObservation `json:"inference,omitempty"`
}

var osPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

func text(v string, max int) bool {
	if !utf8.ValidString(v) || len(v) == 0 || len(v) > max || strings.TrimSpace(v) != v {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (f Facts) Validate() error {
	const max = 9007199254740991
	if !osPattern.MatchString(f.OSID) || !text(f.OSVersion, 64) || (f.Architecture != "amd64" && f.Architecture != "arm64") || !text(f.Hostname, 255) || !text(f.AgentVersion, 32) || !text(f.DiskPath, 1024) || !strings.HasPrefix(f.DiskPath, "/") || f.LogicalCPUs < 1 || f.LogicalCPUs > 16384 || f.MemoryTotalBytes < 1 || f.MemoryTotalBytes > max || f.DiskTotalBytes < 1 || f.DiskTotalBytes > max || f.DiskAvailableBytes < 0 || f.DiskAvailableBytes > f.DiskTotalBytes || f.ObservedAt.IsZero() || f.ObservedAt.Year() < 2020 || f.ObservedAt.Year() > 2200 {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func (h Handshake) Validate() error {
	profile := h.Profile()
	if !KnownProfile(profile) || h.ProtocolVersion != ProfileVersion(profile) {
		return errors.New("AGENT_PROTOCOL_UNSUPPORTED")
	}
	expected := Capabilities(profile)
	if len(h.Capabilities) != len(expected) {
		return errors.New("AGENT_PROTOCOL_UNSUPPORTED")
	}
	for index, capability := range expected {
		if h.Capabilities[index] != capability {
			return errors.New("AGENT_PROTOCOL_UNSUPPORTED")
		}
	}
	if h.Component != nil {
		if !IsManagementProfile(profile) {
			return errors.New("AGENT_PROTOCOL_UNSUPPORTED")
		}
		if e := h.Component.ValidateForProfile(profile); e != nil {
			return e
		}
	}
	if h.Inference != nil {
		if !IsInferenceProfile(profile) {
			return errors.New("AGENT_PROFILE_REJECTED")
		}
		if e := h.Inference.Validate(); e != nil {
			return e
		}
	}
	return h.Facts.Validate()
}
func (h Handshake) Profile() string {
	if h.ExecutionProfile == "" {
		return ReadOnlyProfile
	}
	return h.ExecutionProfile
}
