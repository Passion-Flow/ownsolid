package client

import (
	"context"
	"math/rand/v2"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// FullJitter returns a delay uniform between zero and base*2^failures, capped at limit.
// Spreading the whole range keeps agents that failed together from returning together.
func FullJitter(base, limit time.Duration, failures int) time.Duration {
	return fullJitter(rand.Int64N, base, limit, failures)
}

func fullJitter(random func(int64) int64, base, limit time.Duration, failures int) time.Duration {
	ceiling := min(base, limit)
	for i := 0; i < failures && ceiling < limit; i++ {
		ceiling = min(ceiling*2, limit)
	}
	return time.Duration(random(int64(ceiling) + 1))
}

// sleep waits for d or until ctx ends, and reports whether d passed.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Retry paces a call repeated while control cannot be reached, with the full-jitter
// bounds of the default pacing. It holds no clock of its own, so a caller that polls
// local work on a short tick asks it whether the next attempt is due.
type Retry struct {
	failures int
	due      time.Time
}

// Due reports whether the next attempt may start at now.
func (r *Retry) Due(now time.Time) bool { return !now.Before(r.due) }

// Failed records a failed attempt at now and returns the delay before the next one.
// first is true when the previous attempt had succeeded.
func (r *Retry) Failed(now time.Time) (delay time.Duration, first bool) {
	pacing := wire.DefaultPacing()
	delay = FullJitter(time.Duration(pacing.ReconnectBaseSeconds)*time.Second, time.Duration(pacing.ReconnectMaxSeconds)*time.Second, r.failures)
	first = r.failures == 0
	r.failures++
	r.due = now.Add(delay)
	return delay, first
}

// Succeeded resets the backoff and reports whether the attempt ended a run of failures.
func (r *Retry) Succeeded() (recovered bool) {
	recovered = r.failures > 0
	r.failures = 0
	r.due = time.Time{}
	return recovered
}
