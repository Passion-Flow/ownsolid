// Package agentgateway serves the agent session: the long-lived gRPC stream that carries
// heartbeats and task claims (ADR 0007). The gateway is stateless. It keeps only the open
// streams and passes every exchange to the node service, which owns authentication, rate
// limits, persistence and task leases, so a gateway can run anywhere and restart freely.
package agentgateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Backend is the node service as the gateway sees it. Errors carry public codes.
type Backend interface {
	// Admit applies the per-address and per-node request limits of the HTTPS machine
	// paths and validates the credential form. It runs for every exchange.
	Admit(ctx context.Context, address, node, credential string) error
	Heartbeat(ctx context.Context, node, credential string, h wire.Handshake) (wire.Disposition, error)
	// Claim leases the next task, holding the claim open for up to input's wait.
	Claim(ctx context.Context, node, credential, request string, input wire.ClaimRequest) (wire.Claim, error)
	// Acknowledge confirms a lease before the agent executes it (ADR 0011).
	Acknowledge(ctx context.Context, node, credential, request string, ack wire.TaskAcknowledgement) (wire.RenewedLease, error)
}

// Policy bounds the gateway. All values come from the control configuration.
type Policy struct {
	// MaxSessions caps open streams, authenticated or not.
	MaxSessions int
	// ExchangeSeconds bounds one heartbeat or claim, excluding the claim wait.
	ExchangeSeconds int
}

// Problem converts an error to the public code and status the HTTPS paths would return.
type Problem func(error) (string, int)

type Gateway struct {
	backend  Backend
	policy   Policy
	problem  Problem
	server   *grpc.Server
	slots    chan struct{}
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct{ cancel context.CancelCauseFunc }

type addressKey struct{}

var errReplaced = errors.New("AGENT_SESSION_REPLACED")

// idleTimeout closes a session that sends nothing; tests shorten it.
var idleTimeout = time.Duration(wire.SessionIdleSeconds) * time.Second

// The session codec is selected by the "application/grpc+json" content type. A stream in
// any other encoding fails to decode its first message.
func init() { encoding.RegisterCodec(wire.SessionCodec{}) }

// New builds a gateway. The gRPC server is never bound to a socket; ServeHTTP hands it
// streams from the control HTTPS listener.
func New(backend Backend, policy Policy, problem Problem) (*Gateway, error) {
	if backend == nil || problem == nil || policy.MaxSessions < 1 || policy.ExchangeSeconds < 1 {
		return nil, errors.New("AGENT_GATEWAY_CONFIGURATION_INVALID")
	}
	g := &Gateway{backend: backend, policy: policy, problem: problem, slots: make(chan struct{}, policy.MaxSessions), sessions: map[string]*session{}}
	g.server = grpc.NewServer(
		grpc.MaxRecvMsgSize(wire.MaxSessionMessageBytes),
		grpc.MaxSendMsgSize(wire.MaxSessionMessageBytes),
	)
	g.server.RegisterService(&grpc.ServiceDesc{
		ServiceName: wire.SessionService,
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    wire.SessionMethod,
			Handler:       func(_ any, stream grpc.ServerStream) error { return g.connect(stream) },
			ServerStreams: true,
			ClientStreams: true,
		}},
	}, nil)
	return g, nil
}

// Owns reports whether a request is an agent session stream.
func Owns(r *http.Request) bool {
	return r != nil && r.URL != nil && r.Method == http.MethodPost && r.URL.Path == wire.SessionPath && r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc")
}

// ServeHTTP serves one stream. The caller has already checked transport, origin and
// audience. address is the client address used for rate limits.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request, address string) {
	// The listener's request deadlines are for short requests. A session ends through its
	// idle timeout, the agent closing it or the serving context being cancelled.
	controller := http.NewResponseController(w)
	if e := controller.SetReadDeadline(time.Time{}); e != nil && !errors.Is(e, http.ErrNotSupported) {
		http.Error(w, "", http.StatusServiceUnavailable)
		return
	}
	if e := controller.SetWriteDeadline(time.Time{}); e != nil && !errors.Is(e, http.ErrNotSupported) {
		http.Error(w, "", http.StatusServiceUnavailable)
		return
	}
	g.server.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), addressKey{}, address)))
}

// Sessions reports the number of open streams and held long-poll claims.
func (g *Gateway) Sessions() int { return len(g.slots) }

// Hold takes a session slot for a long-polled claim on the HTTPS fallback, which waits
// like a session instead of holding a shared request slot. It reports false when full.
func (g *Gateway) Hold() (release func(), ok bool) {
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, true
	default:
		return nil, false
	}
}

func header(md metadata.MD, name string) (string, bool) {
	values := md.Get(name)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

func (g *Gateway) connect(stream grpc.ServerStream) error {
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		return status.Error(codes.ResourceExhausted, "AUTH_CAPACITY_EXCEEDED")
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	node, okNode := header(md, wire.SessionNodeHeader)
	site, okSite := header(md, wire.SessionSiteHeader)
	cell, okCell := header(md, wire.SessionCellHeader)
	authorization, okAuth := header(md, "authorization")
	credential, bearer := strings.CutPrefix(authorization, "Bearer ")
	if !okAuth || !bearer || strings.Contains(credential, " ") || credential == "" {
		return status.Error(codes.Unauthenticated, "AGENT_UNAUTHORIZED")
	}
	if _, e := uuid.Parse(node); !okNode || e != nil || len(node) != 36 {
		return status.Error(codes.InvalidArgument, "VALIDATION_FAILED")
	}
	// The scope check matches the HTTPS paths: another cell's node is not found here.
	if !okSite || !okCell || (wire.Scope{SiteID: site, CellID: cell}) != wire.LocalScope() {
		return status.Error(codes.NotFound, "RESOURCE_NOT_FOUND")
	}
	address, _ := stream.Context().Value(addressKey{}).(string)
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(nil)
	current := &session{cancel: cancel}
	registered := false
	defer func() {
		if registered {
			g.mu.Lock()
			if g.sessions[node] == current {
				delete(g.sessions, node)
			}
			g.mu.Unlock()
			slog.Info("agent_session_closed", "node_id", node)
		}
	}()

	messages := make(chan wire.AgentMessage)
	received := make(chan error, 1)
	go func() {
		for {
			var m wire.AgentMessage
			if e := stream.RecvMsg(&m); e != nil {
				received <- e
				return
			}
			select {
			case messages <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	var last uint64
	for {
		var m wire.AgentMessage
		select {
		case m = <-messages:
		case <-received:
			// The agent closed the stream or the connection broke.
			return nil
		case <-idle.C:
			return status.Error(codes.DeadlineExceeded, "AGENT_SESSION_IDLE")
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errReplaced) {
				return status.Error(codes.Aborted, errReplaced.Error())
			}
			return status.Error(codes.Unavailable, "SERVICE_NOT_READY")
		}
		idle.Reset(idleTimeout)
		if e := m.Validate(last); e != nil || last == 0 && m.Heartbeat == nil {
			return status.Error(codes.InvalidArgument, "AGENT_SESSION_MESSAGE_INVALID")
		}
		last = m.ID
		reply := g.exchange(ctx, address, node, credential, m)
		if reply.Problem == nil && !registered {
			// A node has one session; a newer one replaces an older one that was not closed.
			g.mu.Lock()
			if previous := g.sessions[node]; previous != nil {
				previous.cancel(errReplaced)
			}
			g.sessions[node] = current
			g.mu.Unlock()
			registered = true
			slog.Info("agent_session_opened", "node_id", node)
		}
		if e := stream.SendMsg(&reply); e != nil {
			return nil
		}
	}
}

func (g *Gateway) exchange(ctx context.Context, address, node, credential string, m wire.AgentMessage) wire.ControlMessage {
	reply := wire.ControlMessage{ReplyTo: m.ID}
	budget := time.Duration(g.policy.ExchangeSeconds) * time.Second
	var wait time.Duration
	if m.Claim != nil {
		wait = time.Duration(m.Claim.WaitSeconds) * time.Second
	}
	bounded, cancel := context.WithTimeout(ctx, budget+wait)
	defer cancel()
	fail := func(e error) wire.ControlMessage {
		code, status := g.problem(e)
		reply.Problem = &wire.SessionProblem{Code: code, Status: status}
		return reply
	}
	if e := g.backend.Admit(bounded, address, node, credential); e != nil {
		return fail(e)
	}
	if m.Heartbeat != nil {
		disposition, e := g.backend.Heartbeat(bounded, node, credential, *m.Heartbeat)
		if e != nil {
			return fail(e)
		}
		reply.Disposition = &disposition
		return reply
	}
	if m.Ack != nil {
		acknowledged, e := g.backend.Acknowledge(bounded, node, credential, uuid.NewString(), *m.Ack)
		if e != nil {
			return fail(e)
		}
		reply.Acknowledged = &acknowledged
		return reply
	}
	claim, e := g.backend.Claim(bounded, node, credential, uuid.NewString(), *m.Claim)
	if e != nil {
		return fail(e)
	}
	reply.Claim = &claim
	return reply
}
