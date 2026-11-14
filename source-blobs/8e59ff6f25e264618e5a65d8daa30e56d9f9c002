package v1

import (
	"strconv"
	"time"
)

const (
	// SessionDrainHeader and SessionCloseHeader are optional first-reply durations
	// in nanoseconds, derived from the dedicated socket's drain and close deadlines.
	// Older v1 agents ignore them; absence or invalid values retain legacy behavior.
	SessionDrainHeader = "ownsolid-connection-drain-ns"
	SessionCloseHeader = "ownsolid-connection-close-ns"
	// MaxSessionConnectionHint bounds untrusted duration parsing. It covers the
	// current one-day maximum connection age plus its existing exchange grace.
	MaxSessionConnectionHint = 48 * time.Hour
)

// SessionConnectionAge is a conservative local monotonic schedule from an
// authenticated first reply. It does not change the server's connection age.
type SessionConnectionAge struct {
	DrainAt, CloseAt time.Time
	duration         time.Duration
}

// ParseSessionConnectionAge accepts one canonical positive duration per header.
// started is the local first-heartbeat start, so transit time cannot extend it.
func ParseSessionConnectionAge(drainValues, closeValues []string, started time.Time) (SessionConnectionAge, bool) {
	parse := func(values []string) (time.Duration, bool) {
		if len(values) != 1 || len(values[0]) > 20 {
			return 0, false
		}
		n, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || n <= 0 || n > int64(MaxSessionConnectionHint) || strconv.FormatInt(n, 10) != values[0] {
			return 0, false
		}
		return time.Duration(n), true
	}
	drain, ok := parse(drainValues)
	if !ok {
		return SessionConnectionAge{}, false
	}
	closing, ok := parse(closeValues)
	if !ok || closing < drain || started.IsZero() {
		return SessionConnectionAge{}, false
	}
	return SessionConnectionAge{started.Add(drain), started.Add(closing), drain}, true
}

// Due reports whether a between-exchange authentication probe is due. The lead
// covers a request and two paced cycles but uses at most half the original age.
func (a SessionConnectionAge) Due(now time.Time, request, maximumInterval time.Duration) bool {
	if a.duration <= 0 || request <= 0 || maximumInterval <= 0 {
		return false
	}
	lead := min(a.duration/2, request+2*maximumInterval)
	return !now.Before(a.DrainAt.Add(-lead))
}

// ProbeBudget preserves half a still-positive drain window for the old stream.
// Expired age keeps the normal bound; the owner separately applies its idle bound.
func (a SessionConnectionAge) ProbeBudget(now time.Time, request time.Duration) time.Duration {
	if remaining := a.DrainAt.Sub(now); remaining > 0 {
		return min(request, remaining/2)
	}
	return request
}
