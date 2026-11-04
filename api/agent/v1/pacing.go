package v1

import "errors"

// Bounds of Pacing. An agent refuses values outside them and keeps what it has.
const (
	MinHeartbeatSeconds     = 5
	MaxHeartbeatSeconds     = 120
	MaxJitterPercent        = 50
	MaxReconnectBaseSeconds = 60
	MaxReconnectMaxSeconds  = 900
)

// Pacing is how control paces an agent at runtime (ADR 0013). Every heartbeat reply
// carries it, so a change reaches the fleet within one interval and without a restart.
type Pacing struct {
	// HeartbeatSeconds is the mean time between two heartbeats of an idle agent.
	HeartbeatSeconds int `json:"heartbeat_seconds"`
	// JitterPercent spreads each interval evenly around HeartbeatSeconds.
	JitterPercent int `json:"jitter_percent"`
	// ReconnectBaseSeconds and ReconnectMaxSeconds bound the full-jitter backoff after a
	// failed cycle: the delay is uniform between zero and base*2^failures, capped at max.
	ReconnectBaseSeconds int `json:"reconnect_base_seconds"`
	ReconnectMaxSeconds  int `json:"reconnect_max_seconds"`
}

// DefaultPacing is the pacing of an agent that has not heard from control, and of a cell
// whose operator has changed nothing. It fits every allowed online window.
func DefaultPacing() Pacing {
	return Pacing{HeartbeatSeconds: HeartbeatSeconds, JitterPercent: 10, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 60}
}

// Validate checks the protocol bounds of p.
func (p Pacing) Validate() error {
	if p.HeartbeatSeconds < MinHeartbeatSeconds || p.HeartbeatSeconds > MaxHeartbeatSeconds ||
		p.JitterPercent < 0 || p.JitterPercent > MaxJitterPercent ||
		p.ReconnectBaseSeconds < 1 || p.ReconnectBaseSeconds > MaxReconnectBaseSeconds ||
		p.ReconnectMaxSeconds < p.ReconnectBaseSeconds || p.ReconnectMaxSeconds > MaxReconnectMaxSeconds {
		return errors.New("AGENT_PACING_INVALID")
	}
	return nil
}

// ClaimWaitSeconds is how long an idle agent lets control hold its claim: the shortest
// possible interval, so new work starts at once and only the jitter remainder is slept.
func (p Pacing) ClaimWaitSeconds() int {
	return p.HeartbeatSeconds * (100 - p.JitterPercent) / 100
}

// FitsOnlineWindow reports whether a node paced by p stays online through one late
// heartbeat: half the longest interval again must fit inside onlineSeconds.
func (p Pacing) FitsOnlineWindow(onlineSeconds int) bool {
	return 3*p.HeartbeatSeconds*(100+p.JitterPercent) <= 200*onlineSeconds
}

// IdleSeconds is how long control keeps a silent session of an agent paced by p open.
func (p Pacing) IdleSeconds() int {
	return max(SessionIdleSeconds, 3*p.HeartbeatSeconds)
}
