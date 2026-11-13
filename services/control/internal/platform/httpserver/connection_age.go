package httpserver

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/tls"
	"math/rand/v2"
	"net"
	"time"
)

// ConnectionAge bounds when a dedicated listener drains a connection and how
// long an exchange already admitted on it may finish.
type ConnectionAge struct {
	Maximum time.Duration
	Grace   time.Duration
}

func ageConnection(conn net.Conn, age ConnectionAge) (*agingConn, error) {
	acceptedAt := time.Now()
	drainAt := acceptedAt.Add(age.Maximum - rand.N(age.Maximum/10+1))
	closeAt := drainAt.Add(age.Grace)
	if err := conn.SetDeadline(closeAt); err != nil {
		return nil, err
	}
	return &agingConn{Conn: conn, id: cryptorand.Text(), acceptedAt: acceptedAt, drainAt: drainAt, closeAt: closeAt}, nil
}

type agingConn struct {
	net.Conn
	id         string
	acceptedAt time.Time
	drainAt    time.Time
	closeAt    time.Time
}

func (c *agingConn) bound(deadline time.Time) time.Time {
	if deadline.IsZero() || deadline.After(c.closeAt) {
		return c.closeAt
	}
	return deadline
}

func (c *agingConn) SetDeadline(t time.Time) error      { return c.Conn.SetDeadline(c.bound(t)) }
func (c *agingConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(c.bound(t)) }
func (c *agingConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(c.bound(t)) }

type connectionDrainKey struct{}
type connectionCloseKey struct{}
type connectionIdentityKey struct{}

func withConnectionDrainAt(ctx context.Context, conn net.Conn) context.Context {
	if secured, ok := conn.(*tls.Conn); ok {
		if aged, ok := secured.NetConn().(*agingConn); ok {
			ctx = context.WithValue(ctx, connectionDrainKey{}, aged.drainAt)
			ctx = context.WithValue(ctx, connectionCloseKey{}, aged.closeAt)
			return context.WithValue(ctx, connectionIdentityKey{}, aged.id)
		}
	}
	return ctx
}

// ConnectionID identifies a dedicated transport in lifecycle and session logs.
// It comes from the socket owner, independently of request headers or node identity.
func ConnectionID(ctx context.Context) string {
	id, _ := ctx.Value(connectionIdentityKey{}).(string)
	return id
}

// ConnectionDrainAt reports when the dedicated transport stops taking new
// exchanges. A zero time indicates that the listener has no age policy.
func ConnectionDrainAt(ctx context.Context) time.Time {
	at, _ := ctx.Value(connectionDrainKey{}).(time.Time)
	return at
}

// ConnectionCloseAt reports the dedicated transport's unchanged hard deadline.
// A zero time indicates a listener without a connection-age policy.
func ConnectionCloseAt(ctx context.Context) time.Time {
	at, _ := ctx.Value(connectionCloseKey{}).(time.Time)
	return at
}
