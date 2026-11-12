package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// oneConnectionListener lets Shutdown retire one transport. Closing acceptance
// must leave the accepted socket owned by http.Server until requests finish.
type oneConnectionListener struct {
	conn      net.Conn
	mu        sync.Mutex
	delivered bool
	closed    chan struct{}
	once      sync.Once
}

func (l *oneConnectionListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	select {
	case <-l.closed:
		l.mu.Unlock()
		return nil, net.ErrClosed
	default:
	}
	if !l.delivered {
		l.delivered = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *oneConnectionListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConnectionListener) Addr() net.Addr { return l.conn.LocalAddr() }

func serveAgedListener(ctx context.Context, listener net.Listener, tlsConfig *tls.Config, handler http.Handler, policy config.HTTPPolicy, age ConnectionAge) (result error) {
	ctx, cancel := context.WithCancel(ctx)
	accepted := &bounded{Listener: listener, slots: make(chan struct{}, policy.MaxConnections)}
	var workers sync.WaitGroup
	var firstFailure sync.Once
	var shutdownError error
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		<-ctx.Done()
		_ = accepted.Close()
	}()
	defer func() {
		cancel()
		_ = accepted.Close()
		workers.Wait()
		<-watchDone
		result = errors.Join(result, shutdownError)
	}()
	// Retain net/http's bounded backoff for temporary kernel accept failures.
	const initialAcceptBackoff = 5 * time.Millisecond
	const maximumAcceptBackoff = time.Second
	var backoff time.Duration
	for {
		conn, err := accepted.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if temporary, ok := err.(net.Error); ok && temporary.Temporary() {
				if backoff == 0 {
					backoff = initialAcceptBackoff
				} else {
					backoff = min(2*backoff, maximumAcceptBackoff)
				}
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
				continue
			}
			return err
		}
		backoff = 0
		aged, err := ageConnection(conn, age)
		if err != nil {
			_ = conn.Close()
			slog.Warn("http_connection_age_deadline_failed")
			continue
		}
		workers.Go(func() {
			if err := serveAgedConnection(ctx, aged, tlsConfig, handler, policy, age.Grace); err != nil {
				firstFailure.Do(func() { shutdownError = err })
				cancel()
			}
		})
	}
}

func serveAgedConnection(ctx context.Context, conn *agingConn, tlsConfig *tls.Config, handler http.Handler, policy config.HTTPPolicy, grace time.Duration) error {
	// net/http configures ALPN on its TLS config. Each connection owner needs
	// its own clone rather than concurrent mutations of the shared snapshot.
	server := newHTTPServer(ctx, tlsConfig.Clone(), handler, policy)
	secured := tls.Server(conn, server.TLSConfig)
	defer secured.Close()
	listener := &oneConnectionListener{conn: secured, closed: make(chan struct{})}
	defer listener.Close()
	closed := make(chan struct{})
	var markClosed sync.Once
	server.ConnContext = withConnectionDrainAt
	server.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			markClosed.Do(func() { close(closed) })
			_ = listener.Close()
		}
	}
	retire := time.NewTimer(time.Until(conn.drainAt))
	defer retire.Stop()
	shutdownDone := make(chan error, 1)
	go func() {
		reason := "connection_age"
		budget := grace
		select {
		case <-closed:
			shutdownDone <- nil
			return
		case <-ctx.Done():
			reason = "process_shutdown"
			budget = config.Seconds(policy.ShutdownSeconds)
		case <-retire.C:
		}
		shutdown, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		err := server.Shutdown(shutdown)
		forced := err != nil
		if err != nil {
			_ = server.Close()
			if reason == "connection_age" {
				slog.Warn("http_connection_age_grace_expired")
				// An uncooperative peer consumes only its own bounded grace.
				err = nil
			}
		}
		// Notify the transport before logging; a slow sink must not postpone GOAWAY.
		slog.Info("http_connection_retired", "reason", reason,
			"connection_id", conn.id,
			"accepted_at", conn.acceptedAt.UTC(), "drain_at", conn.drainAt.UTC(),
			"age_seconds", conn.drainAt.Sub(conn.acceptedAt).Seconds(), "forced", forced)
		shutdownDone <- err
	}()
	err := server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		_ = server.Close()
		_ = secured.Close()
		markClosed.Do(func() { close(closed) })
		<-shutdownDone
		return err
	}
	return <-shutdownDone
}
