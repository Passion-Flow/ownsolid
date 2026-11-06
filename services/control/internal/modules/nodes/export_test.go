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
