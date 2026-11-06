package nodes

import (
	"context"
	"time"
)

// EnableLivenessStartedAt enables liveness for a process that began to listen at started, so
// a test need not wait out the restart grace.
func (s *Service) EnableLivenessStartedAt(ctx context.Context, started time.Time) error {
	return s.enableLiveness(ctx, started)
}

// HeartbeatDigest exposes the digest for tests.
var HeartbeatDigest = heartbeatDigest

// WakesDelivering says the outbox reader of s has started.
func (s *Service) WakesDelivering() bool { return s.delivering.Load() }

// PruneTasks runs one pass over finished tasks.
func (s *Service) PruneTasks(ctx context.Context) error { return s.pruneTasks(ctx) }

// WatchedNodes is how many nodes have a claim waiting on this process.
func (s *Service) WatchedNodes() int {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	return len(s.wake.waiters)
}
