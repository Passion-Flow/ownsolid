package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// The agent session is the v1 control channel: one long-lived, agent-initiated gRPC
// stream over HTTP/2 that carries heartbeats and task claims. Bulk transfers, results and
// lease renewals stay on the HTTPS paths. When the stream cannot be opened, for example
// behind a proxy that speaks only HTTP/1.1, the agent uses the same exchanges over HTTPS
// and long-polls the claims path. ADR 0007 records the design.
const (
	// SessionService and SessionMethod name the gRPC method; SessionPath is its HTTP/2 path.
	SessionService = "ownsolid.agent.v1.AgentSession"
	SessionMethod  = "Connect"
	SessionPath    = "/" + SessionService + "/" + SessionMethod

	// SessionNodeHeader, SessionSiteHeader and SessionCellHeader carry the node identity
	// and its scope as gRPC metadata. The credential travels in "authorization" as a bearer
	// token, exactly as on the HTTPS paths.
	SessionNodeHeader = "ownsolid-node-id"
	SessionSiteHeader = "ownsolid-site-id"
	SessionCellHeader = "ownsolid-cell-id"

	// MaxSessionMessageBytes bounds one encoded session message in either direction.
	MaxSessionMessageBytes = 65536
	// MaxClaimWaitSeconds bounds how long control holds a claim open for new work. It is
	// the heartbeat interval, so an idle agent still reports once per interval.
	MaxClaimWaitSeconds = HeartbeatSeconds
	// SessionIdleSeconds closes a session that sends nothing for three intervals.
	SessionIdleSeconds = 3 * HeartbeatSeconds
	// SessionFallbackRetrySeconds is how long an agent stays on HTTPS long-polling after
	// the stream could not be opened before it tries the stream again.
	SessionFallbackRetrySeconds = 300
)

// ClaimRequest asks for the next task lease. With WaitSeconds above zero, control holds
// the claim until work arrives or the wait ends. An empty object claims without waiting.
type ClaimRequest struct {
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

func (c ClaimRequest) Validate() error {
	if c.WaitSeconds < 0 || c.WaitSeconds > MaxClaimWaitSeconds {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

// AgentMessage is one agent-to-control message. ID increases by one per message within a
// session, starting at 1, and exactly one exchange is set.
type AgentMessage struct {
	ID        uint64        `json:"id"`
	Heartbeat *Handshake    `json:"heartbeat,omitempty"`
	Claim     *ClaimRequest `json:"claim,omitempty"`
}

func (m AgentMessage) Validate(previous uint64) error {
	if m.ID != previous+1 || (m.Heartbeat == nil) == (m.Claim == nil) {
		return errors.New("AGENT_SESSION_MESSAGE_INVALID")
	}
	if m.Claim != nil {
		return m.Claim.Validate()
	}
	return nil
}

// ControlMessage answers the agent message named by ReplyTo with exactly one of a
// disposition, a claim or a problem.
type ControlMessage struct {
	ReplyTo     uint64          `json:"reply_to"`
	Disposition *Disposition    `json:"disposition,omitempty"`
	Claim       *Claim          `json:"claim,omitempty"`
	Problem     *SessionProblem `json:"problem,omitempty"`
}

// SessionProblem carries the code and HTTP-equivalent status the HTTPS path would return,
// so both transports fail the same way.
type SessionProblem struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
}

// Validate checks that the message answers request and holds the expected kind of reply.
func (m ControlMessage) Validate(request AgentMessage) error {
	bad := errors.New("CONTROL_RESPONSE_INVALID")
	set := 0
	for _, present := range []bool{m.Disposition != nil, m.Claim != nil, m.Problem != nil} {
		if present {
			set++
		}
	}
	if m.ReplyTo != request.ID || set != 1 {
		return bad
	}
	if m.Problem != nil {
		if !text(m.Problem.Code, 128) || m.Problem.Status < 400 || m.Problem.Status > 599 {
			return bad
		}
		return nil
	}
	if request.Heartbeat != nil && m.Disposition == nil || request.Claim != nil && m.Claim == nil {
		return bad
	}
	return nil
}

// SessionCodec is the gRPC codec of the session: the same strict JSON as the HTTPS paths,
// with unknown fields and trailing data refused. Its name selects the content subtype
// "application/grpc+json".
type SessionCodec struct{}

func (SessionCodec) Name() string { return "json" }

func (SessionCodec) Marshal(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil || len(b) > MaxSessionMessageBytes {
		return nil, errors.New("AGENT_SESSION_MESSAGE_INVALID")
	}
	return b, nil
}

func (SessionCodec) Unmarshal(data []byte, v any) error {
	if len(data) > MaxSessionMessageBytes {
		return errors.New("AGENT_SESSION_MESSAGE_INVALID")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("AGENT_SESSION_MESSAGE_INVALID")
	}
	return nil
}
