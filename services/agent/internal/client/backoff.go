package client

import (
	"context"
	"math/rand/v2"
	"time"
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
