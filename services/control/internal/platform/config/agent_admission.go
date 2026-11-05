package config

import (
	"errors"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Bounds of AgentAdmissionPolicy.
const (
	maximumAgentSeats            = 4096
	maximumAgentQueueLength      = 65536
	maximumAgentWaitMilliseconds = 10000
	maximumAgentCallsPerMinute   = 60000
	maximumAgentCallBurst        = 10000
	minimumAgentTrackedKeys      = 1024
	maximumAgentTrackedKeys      = 10000000
)

// AgentAdmissionPolicy decides in memory, before any database work, whether this control
// process serves an agent call now (ADR 0017). Limits apply per process.
type AgentAdmissionPolicy struct {
	// Seats is how many agent calls this process serves at once, over HTTPS and on the
	// session stream together. Agent calls do not use the HTTP request slots.
	Seats int `yaml:"seats"`
	// Quarantine, Heartbeat, Telemetry and Bulk bound each priority, highest first.
	Quarantine AgentPriorityPolicy `yaml:"quarantine"`
	Heartbeat  AgentPriorityPolicy `yaml:"heartbeat"`
	Telemetry  AgentPriorityPolicy `yaml:"telemetry"`
	Bulk       AgentPriorityPolicy `yaml:"bulk"`
	// Address limits the calls of one client address, before any credential is checked.
	Address AgentCallLimit `yaml:"address"`
	// Calls limits each call of one node.
	Calls AgentCallLimits `yaml:"calls"`
	// TrackedKeys bounds the addresses and node calls held in memory for the limits.
	TrackedKeys int `yaml:"tracked_keys"`
}

// AgentPriorityPolicy bounds the calls of one priority.
type AgentPriorityPolicy struct {
	// MaxSeats caps the seats this priority holds at once.
	MaxSeats int `yaml:"max_seats"`
	// QueueLength is how many calls may wait for a seat; a full queue refuses at once.
	QueueLength int `yaml:"queue_length"`
	// WaitMilliseconds is the longest a call waits for a seat before it is refused.
	WaitMilliseconds int `yaml:"wait_milliseconds"`
	// RetrySeconds is the retry hint of a call refused for lack of a seat.
	RetrySeconds int `yaml:"retry_seconds"`
}

// AgentCallLimit is a token bucket: PerMinute calls on average, Burst at once.
type AgentCallLimit struct {
	PerMinute int `yaml:"per_minute"`
	Burst     int `yaml:"burst"`
}

// AgentCallLimits holds the per-node limit of every call that names a node. Enrollment
// names none and is limited by address and by rates.agent_enroll_ip_count.
type AgentCallLimits struct {
	Heartbeat   AgentCallLimit `yaml:"heartbeat"`
	Claim       AgentCallLimit `yaml:"claim"`
	Acknowledge AgentCallLimit `yaml:"acknowledge"`
	Renew       AgentCallLimit `yaml:"renew"`
	Result      AgentCallLimit `yaml:"result"`
	Stop        AgentCallLimit `yaml:"stop"`
	Events      AgentCallLimit `yaml:"events"`
	Download    AgentCallLimit `yaml:"download"`
	Upload      AgentCallLimit `yaml:"upload"`
}

// DefaultAgentAdmissionPolicy fits the default pacing with room for a reconnect burst.
func DefaultAgentAdmissionPolicy() AgentAdmissionPolicy {
	return AgentAdmissionPolicy{
		Seats:       64,
		Quarantine:  AgentPriorityPolicy{MaxSeats: 64, QueueLength: 1024, WaitMilliseconds: 2000, RetrySeconds: 1},
		Heartbeat:   AgentPriorityPolicy{MaxSeats: 64, QueueLength: 4096, WaitMilliseconds: 2000, RetrySeconds: 2},
		Telemetry:   AgentPriorityPolicy{MaxSeats: 16, QueueLength: 1024, WaitMilliseconds: 1000, RetrySeconds: 5},
		Bulk:        AgentPriorityPolicy{MaxSeats: 16, QueueLength: 256, WaitMilliseconds: 500, RetrySeconds: 10},
		Address:     AgentCallLimit{PerMinute: 1200, Burst: 300},
		TrackedKeys: 100000,
		Calls: AgentCallLimits{
			Heartbeat:   AgentCallLimit{PerMinute: 30, Burst: 10},
			Claim:       AgentCallLimit{PerMinute: 60, Burst: 20},
			Acknowledge: AgentCallLimit{PerMinute: 60, Burst: 20},
			Renew:       AgentCallLimit{PerMinute: 120, Burst: 30},
			Result:      AgentCallLimit{PerMinute: 60, Burst: 20},
			Stop:        AgentCallLimit{PerMinute: 60, Burst: 20},
			Events:      AgentCallLimit{PerMinute: 120, Burst: 30},
			Download:    AgentCallLimit{PerMinute: 240, Burst: 60},
			Upload:      AgentCallLimit{PerMinute: 240, Burst: 60},
		},
	}
}

// Priority returns the bounds of p.
func (p AgentAdmissionPolicy) Priority(priority agentwire.Priority) AgentPriorityPolicy {
	switch priority {
	case agentwire.PriorityQuarantine:
		return p.Quarantine
	case agentwire.PriorityHeartbeat:
		return p.Heartbeat
	case agentwire.PriorityTelemetry:
		return p.Telemetry
	}
	return p.Bulk
}

// Call returns the per-node limit of c. Enrollment has none and reports false.
func (p AgentAdmissionPolicy) Call(c agentwire.Call) (AgentCallLimit, bool) {
	switch c {
	case agentwire.CallHeartbeat:
		return p.Calls.Heartbeat, true
	case agentwire.CallClaim:
		return p.Calls.Claim, true
	case agentwire.CallAcknowledge:
		return p.Calls.Acknowledge, true
	case agentwire.CallRenew:
		return p.Calls.Renew, true
	case agentwire.CallResult:
		return p.Calls.Result, true
	case agentwire.CallStop:
		return p.Calls.Stop, true
	case agentwire.CallEvents:
		return p.Calls.Events, true
	case agentwire.CallDownload:
		return p.Calls.Download, true
	case agentwire.CallUpload:
		return p.Calls.Upload, true
	}
	return AgentCallLimit{}, false
}

func (l AgentCallLimit) valid() bool {
	return l.PerMinute >= 1 && l.PerMinute <= maximumAgentCallsPerMinute && l.Burst >= 1 && l.Burst <= maximumAgentCallBurst
}

func (p AgentPriorityPolicy) valid(seats int) bool {
	return p.MaxSeats >= 1 && p.MaxSeats <= seats && p.QueueLength >= 0 && p.QueueLength <= maximumAgentQueueLength &&
		p.WaitMilliseconds >= 0 && p.WaitMilliseconds <= maximumAgentWaitMilliseconds &&
		p.RetrySeconds >= 1 && p.RetrySeconds <= agentwire.MaxRetryAfterSeconds
}

// Validate checks the bounds and one relation: telemetry and bulk together never hold
// every seat, so heartbeats and quarantine reports always find one.
func (p AgentAdmissionPolicy) Validate() error {
	if p.Seats < 1 || p.Seats > maximumAgentSeats || p.TrackedKeys < minimumAgentTrackedKeys || p.TrackedKeys > maximumAgentTrackedKeys || !p.Address.valid() {
		return errors.New("CONFIG_AGENT_ADMISSION_INVALID")
	}
	for _, priority := range agentwire.Priorities() {
		if !p.Priority(priority).valid(p.Seats) {
			return errors.New("CONFIG_AGENT_ADMISSION_INVALID")
		}
	}
	for _, c := range agentwire.Calls() {
		if limit, ok := p.Call(c); ok && !limit.valid() || !ok && c != agentwire.CallEnroll {
			return errors.New("CONFIG_AGENT_ADMISSION_INVALID")
		}
	}
	if p.Telemetry.MaxSeats+p.Bulk.MaxSeats >= p.Seats {
		return errors.New("CONFIG_AGENT_ADMISSION_RESERVE_INVALID")
	}
	return nil
}
