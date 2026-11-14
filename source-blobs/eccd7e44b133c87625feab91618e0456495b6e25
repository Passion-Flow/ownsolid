package v1

import "time"

// Random returns a uniform value in [0, n) for n > 0. math/rand/v2.Int64N is one; tests
// pass a fixed function to reach the ends of a range.
type Random func(n int64) int64

// HintSpreadPercent spreads a wait after a retry hint above the hint itself, so agents
// refused together with one hint do not return together (ADR 0018).
const HintSpreadPercent = 50

// FullJitter returns a delay uniform between zero and base*2^failures, capped at limit.
// Spreading the whole range keeps agents that failed together from returning together.
func FullJitter(random Random, base, limit time.Duration, failures int) time.Duration {
	ceiling := min(base, limit)
	for i := 0; i < failures && ceiling < limit; i++ {
		ceiling = min(ceiling*2, limit)
	}
	return time.Duration(random(int64(ceiling) + 1))
}

// AfterHint is how long to wait after a failure with the retry hint: uniform between the
// hint and the hint plus HintSpreadPercent, never less than the hint.
func AfterHint(random Random, hint time.Duration) time.Duration {
	return hint + time.Duration(random(int64(hint)*HintSpreadPercent/100+1))
}

// Interval is one jittered heartbeat interval: uniform within JitterPercent of
// HeartbeatSeconds, so the mean rate does not depend on the jitter (ADR 0013).
func (p Pacing) Interval(random Random) time.Duration {
	mean := time.Duration(p.HeartbeatSeconds) * time.Second
	spread := mean * time.Duration(p.JitterPercent) / 100
	return mean - spread + time.Duration(random(2*int64(spread)+1))
}

// Backoff is the full-jitter delay after failures consecutive failed cycles, bounded by
// the reconnect base and maximum of p.
func (p Pacing) Backoff(random Random, failures int) time.Duration {
	return FullJitter(random, time.Duration(p.ReconnectBaseSeconds)*time.Second, time.Duration(p.ReconnectMaxSeconds)*time.Second, failures)
}
