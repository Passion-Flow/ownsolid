package nodes

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func (s *Service) Heartbeat(ctx context.Context, id, credential string, h wire.Handshake) (wire.Disposition, error) {
	return s.heartbeatSelected(ctx, id, credential, h)
}
