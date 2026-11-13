package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/sessiontransport"
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
// The caller's run loop retries failed cycles with full jitter (ADR 0013). A healthy
// fallback session occasionally checks a higher-ranked gateway between claims. An
// acknowledgement can be repeated over HTTPS because the run loop never repeats it.
type Session struct {
	client        *Client
	node          string
	mu            sync.Mutex
	conn          *sessiontransport.Connection
	stream        *sessionStream
	fallbackUntil time.Time
	// probeHTTPS makes the next heartbeat try HTTPS because the stream could not be opened.
	probeHTTPS bool
	// probes counts the HTTPS heartbeats that worked since the stream last did.
	probes       int
	transport    string
	pollHTTP     *http.Client
	pacing       wire.Pacing
	lastExchange time.Time
	now          func() time.Time
	// gateways are the agent gateways in this node's order, or none when the session goes
	// to the control origin (ADR 0048). gateway is the one in use, and unanswered counts
	// the gateways that failed one after another since a stream last opened.
	gateways   []string
	gateway    int
	unanswered int
	rebalance  wire.SessionRebalance
	retiring   *sessiontransport.Retirement
}

type sessionStream struct {
	stream     grpc.ClientStream
	cancel     context.CancelFunc
	last       uint64
	connection *sessiontransport.Connection
	age        ageHint
}

// errStreamRefused marks a stream that control answered but refused; it is not a reason
// to leave the stream for HTTPS.
var errStreamRefused = errors.New("AGENT_SESSION_REFUSED")

// errStreamBusy marks a stream refused because its gateway holds all the sessions it may;
// another gateway can still take it.
var errStreamBusy = fmt.Errorf("%w: gateway full", errStreamRefused)

// errStreamUnavailable marks a client that cannot open a stream at all, so no request was
// made.
var errStreamUnavailable = errors.New("AGENT_SESSION_UNAVAILABLE")

// Session returns the session of node. Close releases it.
func (c *Client) Session(node string) *Session {
	// Until control says otherwise, a failed cycle backs off as this agent is configured.
	pacing := wire.DefaultPacing()
	pacing.ReconnectBaseSeconds, pacing.ReconnectMaxSeconds = c.Policy().BackoffInitialSeconds, c.Policy().BackoffMaxSeconds
	return &Session{client: c, node: node, pacing: pacing, now: time.Now, gateways: wire.SessionAddressOrder(node, c.SessionAddresses)}
}

// Gateway reports the gateway address the session uses, or "" for the control origin.
func (s *Session) Gateway() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.gateways) == 0 {
		return ""
	}
	return s.gateways[s.gateway]
}

// nextGateway moves to the next gateway in this node's order after the one in use failed. It
// reports whether that gateway is one this round has not tried yet; once every gateway has
// failed, the round starts again and the caller treats the failure as control being away.
func (s *Session) nextGateway() bool {
	if len(s.gateways) < 2 {
		return false
	}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	s.rebalance = wire.SessionRebalance{}
	s.gateway = (s.gateway + 1) % len(s.gateways)
	if s.unanswered++; s.unanswered < len(s.gateways) {
		return true
	}
	s.unanswered = 0
	return false
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
	if s.retiring != nil {
		s.retiring.Close()
		s.retiring = nil
	}
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
		if disposition := s.tryRebalance(ctx, h); disposition != nil {
			return *disposition, nil
		}
		if disposition := s.tryAgeHandoff(ctx, h); disposition != nil {
			return *disposition, nil
		}
		reply, e := s.exchange(ctx, wire.AgentMessage{Heartbeat: &h})
		if e != nil {
			s.dropStream()
			s.closeForCertificate(e)
			return wire.Disposition{}, e
		}
		return s.disposition(reply)
	}
	if s.now().Before(s.fallbackUntil) {
		return s.heartbeatHTTPS(ctx, h)
	}
	if s.probeHTTPS {
		// The stream failed on the last attempt. The next attempt starts with the stream
		// again whatever this one finds: HTTPS working once may only mean control just came
		// back. HTTPS working every time the stream fails means the path lacks HTTP/2.
		s.probeHTTPS = false
		out, e := s.heartbeatHTTPS(ctx, h)
		if e != nil {
			s.probes = 0
			return out, e
		}
		if s.probes++; s.probes >= wire.SessionFallbackProbes {
			s.probes = 0
			s.stayOnHTTPS()
		}
		return out, nil
	}
	reply, e := s.openWithHeartbeat(ctx, h)
	if errors.Is(e, errStreamUnavailable) {
		// Nothing was sent, so this attempt still has its one request.
		return s.fallBack(ctx, h)
	}
	if e != nil {
		s.closeForCertificate(e)
		var refused *Problem
		unanswered := !errors.Is(e, errStreamRefused) && !errors.As(e, &refused) && ctx.Err() == nil
		// Another gateway gets the next attempt while some have not been tried. Only when
		// none answers may the path to control lack HTTP/2, so only then is HTTPS probed.
		untried := (unanswered || errors.Is(e, errStreamBusy)) && s.nextGateway()
		s.probeHTTPS = unanswered && !untried
		return wire.Disposition{}, e
	}
	s.transport = TransportStream
	s.probes = 0
	s.unanswered = 0
	return s.disposition(reply)
}

// fallBack reports over HTTPS and, when that works, stays there for the fallback window.
func (s *Session) fallBack(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	out, e := s.heartbeatHTTPS(ctx, h)
	if e != nil {
		return out, e
	}
	s.stayOnHTTPS()
	return out, nil
}

func (s *Session) stayOnHTTPS() {
	slog.Warn("agent_session_fallback", "retry_seconds", wire.SessionFallbackRetrySeconds)
	s.fallbackUntil = s.now().Add(time.Duration(wire.SessionFallbackRetrySeconds) * time.Second)
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

// closeForCertificate closes the connection after a refusal of its certificate: the stream
// shared it, and the next dial presents the certificate the agent has now.
func (s *Session) closeForCertificate(e error) {
	if CertificateRefused(e) && s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
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

func (s *Session) connection() (*sessiontransport.Connection, error) {
	if s.conn != nil {
		return s.conn, nil
	}
	address := ""
	if len(s.gateways) > 0 {
		address = s.gateways[s.gateway]
	}
	conn, e := s.dial(address)
	s.conn = conn
	return conn, e
}

func (s *Session) dial(address string) (*sessiontransport.Connection, error) {
	origin, e := url.Parse(s.client.Origin)
	if e != nil || s.client.tls == nil {
		return nil, errStreamUnavailable
	}
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	if address == "" {
		address = net.JoinHostPort(origin.Hostname(), port)
	}
	// The certificate is verified for the authority's host name, which is the origin's on a
	// gateway as on control, so every gateway presents the agent entrance's certificate.
	verify := s.client.tls.Clone()
	// passthrough dials the address exactly as the HTTPS client does, without a proxy.
	// The authority is the origin's, which control checks as it checks the Host header.
	conn, e := sessiontransport.New("passthrough:///"+address,
		grpc.WithTransportCredentials(credentials.NewTLS(verify)),
		grpc.WithAuthority(origin.Host),
		grpc.WithNoProxy(),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype(wire.SessionCodec{}.Name()), grpc.MaxCallRecvMsgSize(wire.MaxSessionMessageBytes), grpc.MaxCallSendMsgSize(wire.MaxSessionMessageBytes)))
	if e != nil {
		return nil, errStreamUnavailable
	}
	return conn, nil
}

// openWithHeartbeat opens a stream whose first message is the heartbeat h.
func (s *Session) openWithHeartbeat(ctx context.Context, h wire.Handshake) (wire.ControlMessage, error) {
	conn, e := s.connection()
	if e != nil {
		return wire.ControlMessage{}, e
	}
	s.stream, e = s.openStream(ctx, conn)
	if e != nil {
		return wire.ControlMessage{}, e
	}
	reply, e := s.exchange(ctx, wire.AgentMessage{Heartbeat: &h})
	if e != nil {
		s.dropStream()
	}
	return reply, e
}

func (s *Session) openStream(ctx context.Context, conn *sessiontransport.Connection) (*sessionStream, error) {
	scope := s.client.Scope
	md := metadata.Pairs("authorization", "Bearer "+s.client.Credential, wire.SessionNodeHeader, s.node, wire.SessionSiteHeader, scope.SiteID, wire.SessionCellHeader, scope.CellID)
	streamContext, cancel := context.WithCancel(metadata.NewOutgoingContext(context.Background(), md))
	timer := time.AfterFunc(settings.Seconds(s.policy().ControlRequestSeconds), cancel)
	stop := context.AfterFunc(ctx, cancel)
	stream, e := conn.NewStream(streamContext, &grpc.StreamDesc{StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true}, wire.SessionPath)
	timely, live := timer.Stop(), stop()
	if e != nil || !timely || !live {
		cancel()
		return nil, errors.New("CONTROL_CONNECTION_FAILED")
	}
	return &sessionStream{stream: stream, cancel: cancel, connection: conn}, nil
}

// tryRebalance leaves the fallback in place until the candidate authenticates and
// answers. Neither stream has an outstanding claim during the check.
func (s *Session) tryRebalance(ctx context.Context, h wire.Handshake) *wire.Disposition {
	if s.retirementPending() {
		return nil
	}
	now := s.now()
	interval := s.client.GatewayRebalanceSeconds
	if interval == 0 {
		interval = wire.DefaultGatewayRebalanceSeconds
	}
	rank := s.rebalance.Candidate(now, settings.Seconds(interval), s.gateway)
	if rank < 0 {
		return nil
	}
	idleRemaining := s.lastExchange.Add(settings.Seconds(s.pacing.IdleSeconds() - sessionIdleMarginSeconds)).Sub(now)
	budget := min(idleRemaining, settings.Seconds(s.policy().ControlRequestSeconds))
	if s.stream.age.Lifetime != nil {
		budget = s.stream.age.ProbeBudget(now, budget)
	}
	probe, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	conn, e := s.dial(s.gateways[rank])
	if e != nil {
		return nil
	}
	st, e := s.openStream(probe, conn)
	if e == nil {
		reply, exchangeErr := s.exchangeOn(probe, st, wire.AgentMessage{Heartbeat: &h})
		if exchangeErr == nil && reply.Disposition.AcceptsSessionRebalance() && probe.Err() == nil {
			from := s.gateways[s.gateway]
			var retirement *sessiontransport.Retirement
			if s.stream.age.Lifetime != nil {
				retirement, e = s.conn.Retire(s.stream.age.Lifetime, s.stream.age.CloseAt)
				if e != nil {
					st.cancel()
					_ = conn.Close()
					return nil
				}
			}
			s.dropStream()
			if retirement == nil {
				s.conn.Close()
			}
			s.retiring = retirement
			s.conn, s.stream, s.gateway = conn, st, rank
			s.lastExchange = s.now()
			s.rebalance = wire.SessionRebalance{}
			slog.Info("agent_gateway_rebalanced", "from", from, "to", s.gateways[rank])
			return reply.Disposition
		}
		st.cancel()
	}
	conn.Close()
	return nil
}

// exchange sends one message and reads its reply within the request budget plus any
// claim wait. A broken or late stream is closed; the caller drops it.
func (s *Session) exchange(ctx context.Context, m wire.AgentMessage) (wire.ControlMessage, error) {
	reply, e := s.exchangeOn(ctx, s.stream, m)
	if e == nil {
		s.lastExchange = s.now()
	}
	return reply, e
}

func (s *Session) exchangeOn(ctx context.Context, st *sessionStream, m wire.AgentMessage) (wire.ControlMessage, error) {
	started := s.now()
	m.ID = st.last + 1
	budget := settings.Seconds(s.policy().ControlRequestSeconds)
	if m.Claim != nil {
		budget += time.Duration(m.Claim.WaitSeconds) * time.Second
	}
	timer := time.AfterFunc(budget, st.cancel)
	stop := context.AfterFunc(ctx, st.cancel)
	var reply wire.ControlMessage
	e := st.stream.SendMsg(&m)
	if e == nil {
		e = st.stream.RecvMsg(&reply)
	}
	timely, live := timer.Stop(), stop()
	if e != nil {
		return reply, streamError(e)
	}
	if !timely || !live {
		return reply, errors.New("CONTROL_CONNECTION_FAILED")
	}
	st.last = m.ID
	if reply.Validate(m) != nil {
		return reply, errors.New("CONTROL_RESPONSE_INVALID")
	}
	if m.ID == 1 && reply.Disposition.AcceptsSessionRebalance() {
		st.readAge(started)
	}
	return reply, nil
}

// streamError separates a stream control refused from one that never worked. Control
// answers a credential it does not accept with Unauthenticated, the stream's 401, which
// ends the agent as the same answer over HTTPS does.
func streamError(e error) error {
	switch status.Code(e) {
	case codes.Unauthenticated:
		if code := status.Convert(e).Message(); certificateCodes[code] {
			return &Problem{Code: code, Status: http.StatusUnauthorized}
		}
		return &Problem{Code: "AGENT_UNAUTHORIZED", Status: http.StatusUnauthorized}
	case codes.ResourceExhausted:
		return errStreamBusy
	case codes.NotFound, codes.InvalidArgument:
		return errStreamRefused
	}
	return errors.New("CONTROL_CONNECTION_FAILED")
}
