package client

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Transport names reported by Session.Transport.
const (
	TransportStream = "stream"
	TransportHTTPS  = "https"
)

func init() { encoding.RegisterCodec(wire.SessionCodec{}) }

// Session carries the node's heartbeats and claims (ADR 0007). It uses the session stream
// and falls back to HTTPS with long-polled claims when the stream cannot be opened, then
// tries the stream again after SessionFallbackRetrySeconds. Calls are sequential.
type Session struct {
	client        *Client
	node          string
	mu            sync.Mutex
	conn          *grpc.ClientConn
	stream        *sessionStream
	fallbackUntil time.Time
	transport     string
	pollHTTP      *http.Client
	now           func() time.Time
}

type sessionStream struct {
	stream grpc.ClientStream
	cancel context.CancelFunc
	last   uint64
}

// errStreamRefused marks a stream that control answered but refused; it is not a reason
// to leave the stream for HTTPS.
var errStreamRefused = errors.New("AGENT_SESSION_REFUSED")

// Session returns the session of node. Close releases it.
func (c *Client) Session(node string) *Session {
	return &Session{client: c, node: node, now: time.Now}
}

// Transport reports the transport of the last successful exchange, or "".
func (s *Session) Transport() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport
}

// Close ends the stream and releases its connection.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropStream()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	if s.pollHTTP != nil {
		s.pollHTTP.CloseIdleConnections()
	}
}

func (s *Session) base() string { return "/agent/v1/nodes/" + url.PathEscape(s.node) }

// Heartbeat reports the node and returns control's disposition.
func (s *Session) Heartbeat(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stream == nil && !s.now().Before(s.fallbackUntil) {
		reply, e := s.openWithHeartbeat(ctx, h)
		if e == nil {
			s.transport = TransportStream
			return s.disposition(reply)
		}
		if errors.Is(e, errStreamRefused) || ctx.Err() != nil {
			return wire.Disposition{}, e
		}
		var out wire.Disposition
		if e := s.client.Post(ctx, s.base()+"/heartbeat", h, &out); e != nil {
			return out, e
		}
		// HTTPS works where the stream did not, so the path to control lacks HTTP/2.
		slog.Warn("agent_session_fallback", "code", e.Error(), "retry_seconds", wire.SessionFallbackRetrySeconds)
		s.fallbackUntil = s.now().Add(time.Duration(wire.SessionFallbackRetrySeconds) * time.Second)
		s.transport = TransportHTTPS
		return out, nil
	}
	if s.stream == nil {
		var out wire.Disposition
		if e := s.client.Post(ctx, s.base()+"/heartbeat", h, &out); e != nil {
			return out, e
		}
		s.transport = TransportHTTPS
		return out, nil
	}
	message := wire.AgentMessage{Heartbeat: &h}
	reply, e := s.exchange(ctx, message)
	if e != nil && !errors.Is(e, errStreamRefused) && ctx.Err() == nil {
		// A stream left idle while a task ran is closed by control. A heartbeat is safe to
		// repeat, so reopen once before reporting a failure.
		s.dropStream()
		reply, e = s.openWithHeartbeat(ctx, h)
	}
	if e != nil {
		s.dropStream()
		return wire.Disposition{}, e
	}
	return s.disposition(reply)
}

// Claim asks for the next lease, letting control hold the claim for up to wait seconds.
// acknowledged is the agent's cursor, the highest task sequence it acknowledged; control
// answers with a lease, a resync or neither (ADR 0011).
func (s *Session) Claim(ctx context.Context, wait int, acknowledged uint64) (wire.Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := wire.ClaimRequest{WaitSeconds: wait, AcknowledgedSequence: acknowledged}
	if e := request.Validate(); e != nil {
		return wire.Claim{}, e
	}
	if s.stream == nil {
		var out wire.Claim
		if e := s.client.post(ctx, s.longPollClient(), s.base()+"/tasks/claims", request, &out); e != nil {
			return wire.Claim{}, e
		}
		if out.Validate() != nil {
			return wire.Claim{}, errors.New("CONTROL_RESPONSE_INVALID")
		}
		return out, nil
	}
	// A claim is not repeated: control delivers a lease whose reply was lost again on the
	// next claim, because the agent never acknowledged it.
	reply, e := s.exchange(ctx, wire.AgentMessage{Claim: &request})
	if e != nil {
		s.dropStream()
		return wire.Claim{}, e
	}
	if reply.Problem != nil {
		return wire.Claim{}, remoteProblem(reply.Problem.Code, reply.Problem.Status)
	}
	return *reply.Claim, nil
}

// Acknowledge confirms that the agent holds lease and is about to execute it. The agent
// executes only after control confirms. An acknowledgement is idempotent, so when the
// stream fails it is sent once more over HTTPS.
func (s *Session) Acknowledge(ctx context.Context, lease wire.Lease) (wire.RenewedLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ack := wire.TaskAcknowledgement{TaskID: lease.TaskID, Sequence: lease.Sequence, LeaseToken: lease.LeaseToken}
	if e := ack.Validate(); e != nil {
		return wire.RenewedLease{}, e
	}
	if s.stream != nil {
		reply, e := s.exchange(ctx, wire.AgentMessage{Ack: &ack})
		if e == nil {
			if reply.Problem != nil {
				return wire.RenewedLease{}, remoteProblem(reply.Problem.Code, reply.Problem.Status)
			}
			return *reply.Acknowledged, nil
		}
		s.dropStream()
	}
	var out wire.RenewedLease
	e := s.client.post(ctx, s.client.HTTP, s.base()+"/tasks/"+url.PathEscape(lease.TaskID)+"/"+wire.TaskAcknowledgementsPath, wire.TaskAcknowledgementRequest{Sequence: ack.Sequence, LeaseToken: ack.LeaseToken}, &out)
	return out, e
}

func (s *Session) disposition(reply wire.ControlMessage) (wire.Disposition, error) {
	if reply.Problem != nil {
		return wire.Disposition{}, remoteProblem(reply.Problem.Code, reply.Problem.Status)
	}
	return *reply.Disposition, nil
}

func (s *Session) dropStream() {
	if s.stream != nil {
		s.stream.cancel()
		s.stream = nil
	}
}

func (s *Session) policy() settings.Policy { return s.client.Policy() }

// longPollClient extends the HTTPS budgets by the claim wait.
func (s *Session) longPollClient() *http.Client {
	if s.pollHTTP != nil {
		return s.pollHTTP
	}
	extra := time.Duration(wire.MaxClaimWaitSeconds) * time.Second
	transport, ok := s.client.HTTP.Transport.(*http.Transport)
	if !ok {
		client := *s.client.HTTP
		client.Timeout += extra
		s.pollHTTP = &client
		return s.pollHTTP
	}
	poll := transport.Clone()
	poll.ResponseHeaderTimeout += extra
	client := *s.client.HTTP
	client.Transport = poll
	client.Timeout += extra
	s.pollHTTP = &client
	return s.pollHTTP
}

func (s *Session) connection() (*grpc.ClientConn, error) {
	if s.conn != nil {
		return s.conn, nil
	}
	origin, e := url.Parse(s.client.Origin)
	if e != nil || s.client.tls == nil {
		return nil, errors.New("CONTROL_ORIGIN_INVALID")
	}
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	// The certificate is verified for the authority's host name.
	verify := s.client.tls.Clone()
	// passthrough dials the origin host exactly as the HTTPS client does, without a proxy.
	// The authority is the origin's, which control checks as it checks the Host header.
	s.conn, e = grpc.NewClient("passthrough:///"+net.JoinHostPort(origin.Hostname(), port),
		grpc.WithTransportCredentials(credentials.NewTLS(verify)),
		grpc.WithAuthority(origin.Host),
		grpc.WithNoProxy(),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype(wire.SessionCodec{}.Name()), grpc.MaxCallRecvMsgSize(wire.MaxSessionMessageBytes), grpc.MaxCallSendMsgSize(wire.MaxSessionMessageBytes)))
	if e != nil {
		return nil, errors.New("CONTROL_ORIGIN_INVALID")
	}
	return s.conn, nil
}

// openWithHeartbeat opens a stream whose first message is the heartbeat h.
func (s *Session) openWithHeartbeat(ctx context.Context, h wire.Handshake) (wire.ControlMessage, error) {
	conn, e := s.connection()
	if e != nil {
		return wire.ControlMessage{}, e
	}
	scope := s.client.Scope
	md := metadata.Pairs("authorization", "Bearer "+s.client.Credential, wire.SessionNodeHeader, s.node, wire.SessionSiteHeader, scope.SiteID, wire.SessionCellHeader, scope.CellID)
	streamContext, cancel := context.WithCancel(metadata.NewOutgoingContext(context.Background(), md))
	opened := make(chan struct{})
	var stream grpc.ClientStream
	go func() {
		defer close(opened)
		stream, e = conn.NewStream(streamContext, &grpc.StreamDesc{StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true}, wire.SessionPath)
	}()
	timer := time.NewTimer(settings.Seconds(s.policy().ControlRequestSeconds))
	select {
	case <-opened:
		timer.Stop()
	case <-timer.C:
		cancel()
		<-opened
	case <-ctx.Done():
		cancel()
		<-opened
	}
	if e != nil || stream == nil {
		cancel()
		return wire.ControlMessage{}, errors.New("CONTROL_CONNECTION_FAILED")
	}
	s.stream = &sessionStream{stream: stream, cancel: cancel}
	reply, e := s.exchange(ctx, wire.AgentMessage{Heartbeat: &h})
	if e != nil {
		s.dropStream()
		return reply, e
	}
	return reply, nil
}

// exchange sends one message and reads its reply within the request budget plus any
// claim wait. A broken or late stream is closed; the caller drops it.
func (s *Session) exchange(ctx context.Context, m wire.AgentMessage) (wire.ControlMessage, error) {
	st := s.stream
	m.ID = st.last + 1
	budget := settings.Seconds(s.policy().ControlRequestSeconds)
	if m.Claim != nil {
		budget += time.Duration(m.Claim.WaitSeconds) * time.Second
	}
	timer := time.AfterFunc(budget, st.cancel)
	defer timer.Stop()
	stop := context.AfterFunc(ctx, st.cancel)
	defer stop()
	var reply wire.ControlMessage
	e := st.stream.SendMsg(&m)
	if e == nil {
		e = st.stream.RecvMsg(&reply)
	}
	if e != nil {
		return reply, streamError(e)
	}
	st.last = m.ID
	if reply.Validate(m) != nil {
		return reply, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return reply, nil
}

// streamError separates a stream control refused from one that never worked.
func streamError(e error) error {
	switch status.Code(e) {
	case codes.ResourceExhausted, codes.NotFound, codes.InvalidArgument, codes.Unauthenticated:
		return errStreamRefused
	}
	return errors.New("CONTROL_CONNECTION_FAILED")
}
