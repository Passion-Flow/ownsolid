// Package sessiontransport owns the physical lifetime of outgoing Agent session
// connections. It neither authenticates a node nor retries an application exchange.
package sessiontransport

import (
	"context"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
)

// Connection is an Agent gRPC channel with transport lifetime observations.
// Its owner supplies normal TLS verification and closes or retires the channel.
type Connection struct {
	*grpc.ClientConn
	mu   sync.Mutex
	live map[connectionKey]*transport
}
type connectionKey struct{ localNetwork, local, remoteNetwork, remote string }
type transport struct {
	key   connectionKey
	ended context.Context
	end   context.CancelFunc
}
type transportKey struct{}
type ownerKey struct{}
type transportTag struct {
	owner    *Connection
	lifetime *transport
}

// New creates a lazy Agent channel and observes its actual transports. It does not
// connect or retry a heartbeat; all caller-provided TLS and message options apply.
func New(target string, options ...grpc.DialOption) (*Connection, error) {
	c := &Connection{live: make(map[connectionKey]*transport)}
	options = append(options, grpc.WithStatsHandler(c))
	channel, err := grpc.NewClient(target, options...)
	if err != nil {
		return nil, err
	}
	c.ClientConn = channel
	return c, nil
}
func keyOf(local, remote net.Addr) (connectionKey, bool) {
	if local == nil || remote == nil {
		return connectionKey{}, false
	}
	return connectionKey{local.Network(), local.String(), remote.Network(), remote.String()}, true
}

// TagConn associates gRPC's actual connection addresses with its lifetime.
func (c *Connection) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	if info == nil {
		return ctx
	}
	key, ok := keyOf(info.LocalAddr, info.RemoteAddr)
	if !ok {
		return ctx
	}
	ended, end := context.WithCancel(context.WithValue(context.Background(), ownerKey{}, c))
	life := &transport{key, ended, end}
	c.mu.Lock()
	c.live[key] = life
	c.mu.Unlock()
	return context.WithValue(ctx, transportKey{}, transportTag{c, life})
}

// HandleConn releases the exact transport when gRPC reports its physical end.
// Channel close must run outside this callback to avoid reentering gRPC's locks.
func (c *Connection) HandleConn(ctx context.Context, event stats.ConnStats) {
	if _, ok := event.(*stats.ConnEnd); !ok {
		return
	}
	tag, ok := ctx.Value(transportKey{}).(transportTag)
	if !ok || tag.owner != c {
		return
	}
	c.mu.Lock()
	if c.live[tag.lifetime.key] == tag.lifetime {
		delete(c.live, tag.lifetime.key)
	}
	c.mu.Unlock()
	tag.lifetime.end()
}

// TagRPC retains the caller's context without retaining any RPC metadata.
func (*Connection) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

// HandleRPC ignores payloads, credentials and request-level statistics.
func (*Connection) HandleRPC(context.Context, stats.RPCStats) {}

// Lifetime binds a committed stream to its physical, verified TLS transport.
// Call after the first successful reply. Nil means that no safe binding exists.
func (c *Connection) Lifetime(stream grpc.ClientStream) context.Context {
	actual, ok := peer.FromContext(stream.Context())
	if !ok {
		return nil
	}
	secured, ok := actual.AuthInfo.(credentials.TLSInfo)
	if !ok || len(secured.State.VerifiedChains) == 0 {
		return nil
	}
	key, ok := keyOf(actual.LocalAddr, actual.Addr)
	if !ok {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	life := c.live[key]
	if life == nil {
		return nil
	}
	return life.ended
}

// Retirement owns one unused channel until its original physical transport ends
// or its server's hard close deadline arrives. Explicit Close joins this worker.
type Retirement struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
	err  error
}

// Retire leaves a channel quiescent until its bound transport's natural end.
// The caller must have moved all application use to an authenticated replacement.
func (c *Connection) Retire(lifetime context.Context, latest time.Time) (*Retirement, error) {
	if lifetime == nil || lifetime.Value(ownerKey{}) != c || latest.IsZero() {
		return nil, errors.New("AGENT_SESSION_RETIREMENT_INVALID")
	}
	r := &Retirement{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		timer := time.NewTimer(max(time.Until(latest), 0))
		defer timer.Stop()
		select {
		case <-lifetime.Done():
		case <-timer.C:
		case <-r.stop:
		}
		r.err = c.ClientConn.Close()
		close(r.done)
	}()
	return r, nil
}

// Done closes only after the unused channel's close call returns.
func (r *Retirement) Done() <-chan struct{} { return r.done }

// Close immediately releases the owned channel and joins its retirement worker.
func (r *Retirement) Close() error { r.once.Do(func() { close(r.stop) }); <-r.done; return r.err }

// Age binds an optional server schedule to the actual verified physical transport.
// A caller may use it only after a successful first authenticated heartbeat reply.
type Age struct {
	wire.SessionConnectionAge
	Lifetime context.Context
}

// ReadAge reads the first reply's optional metadata and binds its committed transport.
// Missing, invalid or unbound hints keep the existing legacy reconnect behavior.
func (c *Connection) ReadAge(stream grpc.ClientStream, started time.Time) (Age, bool) {
	headers, err := stream.Header()
	if err != nil {
		return Age{}, false
	}
	schedule, ok := wire.ParseSessionConnectionAge(headers.Get(wire.SessionDrainHeader), headers.Get(wire.SessionCloseHeader), started)
	if !ok {
		return Age{}, false
	}
	lifetime := c.Lifetime(stream)
	if lifetime == nil {
		return Age{}, false
	}
	return Age{schedule, lifetime}, true
}
