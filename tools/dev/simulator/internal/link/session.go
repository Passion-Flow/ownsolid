package link

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func init() { encoding.RegisterCodec(wire.SessionCodec{}) }

// ErrRefused is a stream control answered and refused. It is no reason to leave the
// stream for HTTPS.
var ErrRefused = errors.New("AGENT_SESSION_REFUSED")

// ErrBusy is a stream refused because its gateway holds all the sessions it may; another
// gateway can still take it.
var ErrBusy = fmt.Errorf("%w: gateway full", ErrRefused)

// Connection is one HTTP/2 connection that one or more agents open their sessions on. It
// connects lazily, and Reset closes it, ending every stream on it.
type Connection struct {
	dialer *Dialer
	mu     sync.Mutex
	conn   *grpc.ClientConn
	// gateways are the agent gateways in the order of the connection's first node; gateway
	// is the one in use and unanswered counts those that failed in a row (ADR 0048).
	gateways   []string
	gateway    int
	unanswered int
}

// Connection returns a new, unconnected session connection to the dialer's address.
func (d *Dialer) Connection() *Connection { return &Connection{dialer: d} }

// GatewayConnection returns a new, unconnected session connection to the agent gateways,
// tried in node's order as a real agent tries them.
func (d *Dialer) GatewayConnection(node string, gateways []string) *Connection {
	return &Connection{dialer: d, gateways: wire.SessionAddressOrder(node, gateways)}
}

// Gateway is the gateway in use, or "" when there are none.
func (c *Connection) Gateway() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.gateways) == 0 {
		return ""
	}
	return c.gateways[c.gateway]
}

// Next moves to the next gateway after the one in use failed, closing the connection. It
// reports whether that gateway has not been tried since a stream last opened; once all have
// failed the round starts again and the failure counts as control being away.
func (c *Connection) Next() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.gateways) < 2 {
		return false
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.gateway = (c.gateway + 1) % len(c.gateways)
	if c.unanswered++; c.unanswered < len(c.gateways) {
		return true
	}
	c.unanswered = 0
	return false
}

// Opened records that a stream opened, which starts a new round of gateways.
func (c *Connection) Opened() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unanswered = 0
}

func (c *Connection) get() (*grpc.ClientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn, nil
	}
	d := c.dialer
	address := d.address
	if len(c.gateways) > 0 {
		address = c.gateways[c.gateway]
	}
	conn, e := grpc.NewClient("passthrough:///"+address,
		grpc.WithTransportCredentials(credentials.NewTLS(d.tls.Clone())),
		grpc.WithAuthority(d.origin.Host),
		grpc.WithNoProxy(),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype(wire.SessionCodec{}.Name()), grpc.MaxCallRecvMsgSize(wire.MaxSessionMessageBytes), grpc.MaxCallSendMsgSize(wire.MaxSessionMessageBytes)))
	if e != nil {
		return nil, ErrUnreachable
	}
	c.conn = conn
	return conn, nil
}

// Reset closes the connection; the next stream opens a new one.
func (c *Connection) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Stream is one open session. Its exchanges are sequential.
type Stream struct {
	stream grpc.ClientStream
	cancel context.CancelFunc
	last   uint64
	budget time.Duration
}

// Open opens a session for node on c within the request budget.
func (c *Connection) Open(ctx context.Context, node Node) (*Stream, error) {
	conn, e := c.get()
	if e != nil {
		return nil, e
	}
	md := metadata.Pairs("authorization", "Bearer "+node.Credential, wire.SessionNodeHeader, node.ID, wire.SessionSiteHeader, node.Scope.SiteID, wire.SessionCellHeader, node.Scope.CellID)
	streamContext, cancel := context.WithCancel(metadata.NewOutgoingContext(context.Background(), md))
	budget := c.dialer.request
	timer := time.AfterFunc(budget, cancel)
	stop := context.AfterFunc(ctx, cancel)
	stream, e := conn.NewStream(streamContext, &grpc.StreamDesc{StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true}, wire.SessionPath)
	timely, live := timer.Stop(), stop()
	if e != nil || !timely || !live {
		cancel()
		return nil, streamError(e)
	}
	return &Stream{stream: stream, cancel: cancel, budget: budget}, nil
}

// Close ends the stream.
func (s *Stream) Close() { s.cancel() }

// Exchange sends m and returns control's validated reply. A claim's budget grows by its
// wait. A failed exchange leaves the stream broken; the caller closes it.
func (s *Stream) Exchange(ctx context.Context, m wire.AgentMessage) (wire.ControlMessage, error) {
	m.ID = s.last + 1
	budget := s.budget
	if m.Claim != nil {
		budget += time.Duration(m.Claim.WaitSeconds) * time.Second
	}
	timer := time.AfterFunc(budget, s.cancel)
	defer timer.Stop()
	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()
	var reply wire.ControlMessage
	e := s.stream.SendMsg(&m)
	if e == nil {
		e = s.stream.RecvMsg(&reply)
	}
	if e != nil {
		return reply, streamError(e)
	}
	s.last = m.ID
	if reply.Validate(m) != nil {
		return reply, ErrInvalid
	}
	if reply.Problem != nil {
		return reply, &Problem{Code: reply.Problem.Code, Status: reply.Problem.Status, RetryAfter: time.Duration(reply.Problem.RetryAfterSeconds) * time.Second}
	}
	return reply, nil
}

// streamError separates a stream control refused from one that never worked. Control
// refuses a credential it does not accept with Unauthenticated, which is the stream's 401.
func streamError(e error) error {
	switch status.Code(e) {
	case codes.Unauthenticated:
		return &Problem{Code: "AGENT_UNAUTHORIZED", Status: http.StatusUnauthorized}
	case codes.ResourceExhausted:
		return ErrBusy
	case codes.NotFound, codes.InvalidArgument:
		return ErrRefused
	}
	return ErrUnreachable
}
