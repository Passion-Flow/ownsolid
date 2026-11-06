package runtime

import (
	"math/rand/v2"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

// pacer decides how long the run loop waits between cycles, the one place an agent retries
// a heartbeat or a claim (ADR 0013). The pacing is read for every decision, so a change
// from control applies to the next wait.
type pacer struct {
	failures int
}

func newPacer() *pacer { return &pacer{} }

// afterFailure returns the full-jitter delay for one more consecutive failure, and at
// least control's retry hint when the failure carries one (ADR 0018).
func (p *pacer) afterFailure(pacing wire.Pacing, failure error) time.Duration {
	delay := pacing.Backoff(rand.Int64N, p.failures)
	p.failures++
	if hint, ok := client.RetryHint(failure); ok {
		delay = max(delay, client.AfterHint(hint))
	}
	return delay
}

// afterSuccess resets the backoff and returns what is left of a jittered interval after
// the elapsed cycle. A cycle that outran its interval leaves nothing to wait.
func (p *pacer) afterSuccess(pacing wire.Pacing, elapsed time.Duration) time.Duration {
	p.failures = 0
	return max(0, pacing.Interval(rand.Int64N)-elapsed)
}

