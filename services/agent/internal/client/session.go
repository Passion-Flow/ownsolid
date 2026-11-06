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

// sessionIdleMarginSeconds replaces a stream this long before control would close it.
const sessionIdleMarginSeconds = 5

func init() { encoding.RegisterCodec(wire.SessionCodec{}) }

// Session carries the node's heartbeats and claims (ADR 0007). It uses the session stream
// and falls back to HTTPS with long-polled claims when the stream cannot be opened, then
// tries the stream again after SessionFallbackRetrySeconds. Calls are sequential.
//
// A heartbeat or claim makes exactly one request and does not retry: the caller's run
// loop is the one retry layer, and it spaces attempts with full jitter (ADR 0013). The
// acknowledgement is the exception, because the run loop never repeats it.
type Session struct {
	client        *Client
	node          string
	mu            sync.Mutex
	conn          *grpc.ClientConn
	stream        *sessionStream
	fallbackUntil time.Time
	// probeHTTPS makes the next heartbeat try HTTPS because the stream could not be opened.
	probeHTTPS   bool
	transport    string
	pollHTTP     *http.Client
	pacing       wire.Pacing
	lastExchange time.Time
	now          func() time.Time
}

type sessionStream struct {
	stream grpc.ClientStream
	cancel context.CancelFunc
	last   uint64
}

// errStreamRefused marks a stream that control answered but refused; it is not a reason
// to leave the stream for HTTPS.
var errStreamRefused = errors.New("AGENT_SESSION_REFUSED")

// errStreamUnavailable marks a client that cannot open a stream at all, so no request was
// made.
var errStreamUnavailable = errors.New("AGENT_SESSION_UNAVAILABLE")

// Session returns the session of node. Close releases it.
func (c *Client) Session(node string) *Session {
	// Until control says otherwise, a failed cycle backs off as this agent is configured.
	pacing := wire.DefaultPacing()
	pacing.ReconnectBaseSeconds, pacing.ReconnectMaxSeconds = c.Policy().BackoffInitialSeconds, c.Policy().BackoffMaxSeconds
	return &Session{client: c, node: node, pacing: pacing, now: time.Now}
}

// Pacing reports the pacing control last asked for.
func (s *Session) Pacing() wire.Pacing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pacing
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

// Heartbeat reports the node and returns control's disposition, adopting any new pacing.
func (s *Session) Heartbeat(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, e := s.heartbeat(ctx, h)
	if e != nil {
		return wire.Disposition{}, e
	}
	s.adopt(out.Pacing)
	return out, nil
}

func (s *Session) heartbeat(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	// A stream left idle while a task ran is closed by control, so it is replaced rather
	// than tried. The margin covers a heartbeat that reaches control as the session closes.
	if s.stream != nil && s.now().Sub(s.lastExchange) >= time.Duration(s.pacing.IdleSeconds()-sessionIdleMarginSeconds)*time.Second {
		s.dropStream()
	}
	if s.stream != nil {
		reply, e := s.exchange(ctx, wire.AgentMessage{Heartbeat: &h})
		if e != nil {
			s.dropStream()
			return wire.Disposition{}, e
		}
		return s.disposition(reply)
	}
	if s.now().Before(s.fallbackUntil) {
		return s.heartbeatHTTPS(ctx, h)
	}
	if s.probeHTTPS {
		// The stream failed on the last attempt. HTTPS working where the stream did not
		// means the path to control lacks HTTP/2; if it fails too, control is unreachable
		// and the next attempt starts with the stream again.
		s.probeHTTPS = false
		return s.fallBack(ctx, h)
	}
	reply, e := s.openWithHeartbeat(ctx, h)
	if errors.Is(e, errStreamUnavailable) {
		// Nothing was sent, so this attempt still has its one request.
		return s.fallBack(ctx, h)
	}
	if e != nil {
		var refused *Problem
		s.probeHTTPS = !errors.Is(e, errStreamRefused) && !errors.As(e, &refused) && ctx.Err() == nil
		return wire.Disposition{}, e
	}
	s.transport = TransportStream
	return s.disposition(reply)
}

// fallBack reports over HTTPS and, when that works, stays there for the fallback window.
func (s *Session) fallBack(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	out, e := s.heartbeatHTTPS(ctx, h)
	if e != nil {
		return out, e
	}
	slog.Warn("agent_session_fallback", "retry_seconds", wire.SessionFallbackRetrySeconds)
	s.fallbackUntil = s.now().Add(time.Duration(wire.SessionFallbackRetrySeconds) * time.Second)
	return out, nil
}

func (s *Session) heartbeatHTTPS(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	var out wire.Disposition
	if e := s.client.Post(ctx, s.base()+"/heartbeat", h, &out); e != nil {
		return out, e
	}
	s.transport = TransportHTTPS
	return out, nil
}

// adopt takes the pacing of a disposition. An invalid pacing keeps the last valid one, so
// a bad value at control cannot stop an agent.
func (s *Session) adopt(next *wire.Pacing) {
	if next == nil || *next == s.pacing {
		return
	}
	if next.Validate() != nil {
		slog.Warn("agent_pacing_refused", "code", "AGENT_PACING_INVALID")
		return
	}
	s.pacing = *next
	slog.Info("agent_pacing_changed", "heartbeat_seconds", next.HeartbeatSeconds, "jitter_percent", next.JitterPercent, "reconnect_base_seconds", next.ReconnectBaseSeconds, "reconnect_max_seconds", next.ReconnectMaxSeconds)
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
		// The client allows the longest wait; this request is held for its own.
		bounded, cancel := context.WithTimeout(ctx, settings.Seconds(s.policy().ControlRequestSeconds)+time.Duration(wait)*time.Second)
		defer cancel()
		var out wire.Claim
		if e := s.client.post(bounded, s.longPollClient(), s.base()+"/tasks/claims", request, &out); e != nil {
			return wire.Claim{}, e
		}
		if out.Validate() != nil {
			return wire.Claim{}, errors.New("CONTROL_RESPONSE_INVALID")
		}
		return out, nil
	}
	// Control delivers a lease whose reply was lost again on the next claim, because the
	// agent never acknowledged it.
	reply, e := s.exchange(ctx, wire.AgentMessage{Claim: &request})
	if e != nil {
		s.dropStream()
		return wire.Claim{}, e
	}
	if reply.Problem != nil {
		return wire.Claim{}, sessionProblem(reply.Problem)
	}
	return *reply.Claim, nil
}

// Acknowledge confirms that the agent holds lease and is about to execute it. The agent
// executes only after control confirms. An acknowledgement is idempotent and the run loop
// never repeats it, so when the stream fails it is sent once more over HTTPS: a reply lost
// after control recorded it would otherwise strand an acknowledged task.
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
				return wire.RenewedLease{}, sessionProblem(reply.Problem)
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
		return wire.Disposition{}, sessionProblem(reply.Problem)
	}
	return *reply.Disposition, nil
}

// sessionProblem is the error of a problem control answered on the stream.
func sessionProblem(p *wire.SessionProblem) error {
	return remoteProblem(p.Code, p.Status, time.Duration(p.RetryAfterSeconds)*time.Second)
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
		return nil, errStreamUnavailable
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
		return nil, errStreamUnavailable
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
	s.lastExchange = s.now()
	if reply.Validate(m) != nil {
		return reply, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return reply, nil
}

// streamError separates a stream control refused from one that never worked. Control
// answers a credential it does not accept with Unauthenticated, the stream's 401, which
// ends the agent as the same answer over HTTPS does.
func streamError(e error) error {
	switch status.Code(e) {
	case codes.Unauthenticated:
		return &Problem{Code: "AGENT_UNAUTHORIZED", Status: http.StatusUnauthorized}
	case codes.ResourceExhausted, codes.NotFound, codes.InvalidArgument:
		return errStreamRefused
	}
	return errors.New("CONTROL_CONNECTION_FAILED")
}
