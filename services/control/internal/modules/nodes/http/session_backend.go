package http

import (
	"context"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentadmission"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
)

// SessionBackend serves agent session exchanges with the same admission, authentication
// and node service calls as the HTTPS heartbeat, claims and acknowledgement paths.
type SessionBackend struct{ Handler *Handler }

// Admit applies the call's admission (ADR 0018). The caller runs release when the
// exchange ends.
func (b SessionBackend) Admit(ctx context.Context, address, node, credential string, call wire.Call) (func(), error) {
	if !security.ValidToken(credential) {
		return nil, errors.New("AGENT_UNAUTHORIZED")
	}
	return b.Handler.Admission.Admit(ctx, agentadmission.Request{Call: call, Address: address, Node: node, Credential: credential})
}

// CheckPeer applies the certificate rule of the HTTPS paths to the session.
func (b SessionBackend) CheckPeer(ctx context.Context, node string, peer agentidentity.Peer) error {
	return b.Handler.Service.CheckPeer(ctx, node, peer)
}

func (b SessionBackend) Heartbeat(ctx context.Context, node, credential string, h wire.Handshake) (wire.Disposition, error) {
	return b.Handler.Service.Heartbeat(ctx, node, credential, h)
}

func (b SessionBackend) Claim(ctx context.Context, node, credential, request string, input wire.ClaimRequest) (wire.Claim, error) {
	return b.Handler.Service.ClaimWaiting(ctx, node, credential, request, input, b.Handler.claimSeat)
}

func (b SessionBackend) Acknowledge(ctx context.Context, node, credential, request string, ack wire.TaskAcknowledgement) (wire.RenewedLease, error) {
	return b.Handler.Service.Acknowledge(ctx, node, credential, ack.TaskID, wire.TaskAcknowledgementRequest{Sequence: ack.Sequence, LeaseToken: ack.LeaseToken}, request)
}

// claimSeat takes a seat for one look for work of a held claim, which holds none while
// it waits.
func (h *Handler) claimSeat(ctx context.Context) (func(), error) {
	return h.Admission.Enter(ctx, wire.CallClaim.Priority())
}
