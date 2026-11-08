package v1

import (
	"math/rand/v2"
	"time"
)

// Bounds and default for the local preference check interval. These are client
// policy, not fields in the session protocol.
const (
	MinGatewayRebalanceSeconds     = 5
	DefaultGatewayRebalanceSeconds = 60
	MaxGatewayRebalanceSeconds     = 3600
)

// SessionRebalance schedules checks of higher-ranked gateways for one live session.
// The zero value starts a fresh schedule.
type SessionRebalance struct {
	rank      int
	candidate int
	next      time.Time
}

// Candidate returns the next rank to check, or -1 if no check is due. Between
// checks it waits one interval plus uniform jitter of up to one interval.
func (r *SessionRebalance) Candidate(now time.Time, interval time.Duration, rank int) int {
	if rank <= 0 || interval <= 0 {
		*r = SessionRebalance{}
		return -1
	}
	if rank != r.rank {
		*r = SessionRebalance{rank: rank}
	}
	if r.next.IsZero() {
		r.next = now.Add(interval + time.Duration(rand.Int64N(int64(interval))))
		return -1
	}
	if now.Before(r.next) {
		return -1
	}
	candidate := r.candidate
	r.candidate = (candidate + 1) % rank
	r.next = now.Add(interval + time.Duration(rand.Int64N(int64(interval))))
	return candidate
}

// AcceptsSessionRebalance reports whether an answered heartbeat can replace a live
// fallback session. Pacing is validated separately so an invalid pacing hint cannot
// stop the agent.
func (d *Disposition) AcceptsSessionRebalance() bool {
	return d != nil && !d.ServerTime.IsZero() && (d.Status == "active" || d.Status == "disabled")
}
