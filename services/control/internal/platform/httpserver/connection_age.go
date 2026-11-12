package httpserver

import (
	"context"
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
	drainAt := time.Now().Add(age.Maximum - rand.N(age.Maximum/10+1))
	closeAt := drainAt.Add(age.Grace)
	if err := conn.SetDeadline(closeAt); err != nil {
		return nil, err
	}
	return &agingConn{Conn: conn, drainAt: drainAt, closeAt: closeAt}, nil
}

type agingConn struct {
	net.Conn
	drainAt time.Time
	closeAt time.Time
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

func withConnectionDrainAt(ctx context.Context, conn net.Conn) context.Context {
	if secured, ok := conn.(*tls.Conn); ok {
		if aged, ok := secured.NetConn().(*agingConn); ok {
			return context.WithValue(ctx, connectionDrainKey{}, aged.drainAt)
		}
	}
	return ctx
}

// ConnectionDrainAt reports when the dedicated transport stops taking new
// exchanges. A zero time indicates that the listener has no age policy.
func ConnectionDrainAt(ctx context.Context) time.Time {
	at, _ := ctx.Value(connectionDrainKey{}).(time.Time)
	return at
}
