package httpserver

import (
	"context"
	"crypto/tls"
	"math/rand/v2"
	"net"
	"net/http"
	"time"
)

// ConnectionAge bounds when a dedicated listener drains a connection and how
// long an exchange already admitted on it may finish.
type ConnectionAge struct {
	Maximum time.Duration
	Grace   time.Duration
}

type agingListener struct {
	net.Listener
	age ConnectionAge
}

func (l *agingListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		drainAt := time.Now().Add(l.age.Maximum - rand.N(l.age.Maximum/10+1))
		closeAt := drainAt.Add(l.age.Grace)
		if err := conn.SetDeadline(closeAt); err != nil {
			_ = conn.Close()
			continue
		}
		return &agingConn{Conn: conn, drainAt: drainAt, closeAt: closeAt}, nil
	}
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

func retireAgedConnections(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if at := ConnectionDrainAt(r.Context()); !at.IsZero() && !time.Now().Before(at) {
			// The pinned Go HTTP/2 transport converts this before writing headers.
			w.Header().Set("Connection", "close")
		}
		next.ServeHTTP(w, r)
	})
}
