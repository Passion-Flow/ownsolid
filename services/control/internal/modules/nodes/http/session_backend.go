package http

import (
	"context"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// admitAddress applies the per-address request limit of the machine paths.
func (h *Handler) admitAddress(ctx context.Context, host string, transfer bool) error {
	policy := h.Service.Identity.Config.Policy().Rates
	limit := identity.Limit{Purpose: "agent.request.ip", Identifier: host, Count: policy.AgentIPCount, Window: config.Seconds(policy.AgentIPWindowSeconds)}
	if transfer {
		limit = identity.Limit{Purpose: "agent.transfer.ip", Identifier: host, Count: policy.AgentTransferIPCount, Window: config.Seconds(policy.AgentTransferIPWindowSeconds)}
	}
	_, e := h.Service.Identity.Admit(ctx, limit)
	return e
}

// admitNode validates the credential form and applies the per-node request limit.
// Admit persists only an HMAC; an untrusted credential cannot fill the real node's bucket.
func (h *Handler) admitNode(ctx context.Context, node, credential string, transfer bool) error {
	if !security.ValidToken(credential) {
		return errors.New("AGENT_UNAUTHORIZED")
	}
	policy := h.Service.Identity.Config.Policy().Rates
	limit := identity.Limit{Purpose: "agent.node.requests", Identifier: node + ":" + credential, Count: policy.AgentNodeCount, Window: config.Seconds(policy.AgentNodeWindowSeconds)}
	if transfer {
		limit = identity.Limit{Purpose: "agent.node.model_files", Identifier: node + ":" + credential, Count: policy.AgentTransferNodeCount, Window: config.Seconds(policy.AgentTransferNodeWindowSeconds)}
	}
	_, e := h.Service.Identity.Admit(ctx, limit)
	return e
}

// SessionBackend serves agent session exchanges with the same limits, authentication and
// node service calls as the HTTPS heartbeat and claims paths.
type SessionBackend struct{ Handler *Handler }

func (b SessionBackend) Admit(ctx context.Context, address, node, credential string) error {
	if e := b.Handler.admitAddress(ctx, address, false); e != nil {
		return e
	}
	return b.Handler.admitNode(ctx, node, credential, false)
}

func (b SessionBackend) Heartbeat(ctx context.Context, node, credential string, h wire.Handshake) (wire.Disposition, error) {
	return b.Handler.Service.Heartbeat(ctx, node, credential, h)
}

func (b SessionBackend) Claim(ctx context.Context, node, credential, request string, wait time.Duration) (wire.Claim, error) {
	return b.Handler.Service.ClaimWaiting(ctx, node, credential, request, wait)
}
