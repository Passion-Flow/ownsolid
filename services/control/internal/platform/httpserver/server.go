package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"sync"
)

type bounded struct {
	net.Listener
	slots chan struct{}
}
type connection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *connection) Close() error { e := c.Conn.Close(); c.once.Do(c.release); return e }
func (l *bounded) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		select {
		case l.slots <- struct{}{}:
			return &connection{Conn: c, release: func() { <-l.slots }}, nil
		default:
			c.Close()
		}
	}
}

// Serve retains the historical direct-TLS entry point. Plain listeners must use
// ServeConfigured so an omitted TLS configuration cannot silently downgrade it.
func Serve(ctx context.Context, address string, tlsConfig *tls.Config, handler http.Handler, policies ...config.HTTPPolicy) error {
	if tlsConfig == nil {
		return errors.New("CONFIG_TRANSPORT_INVALID")
	}
	return serve(ctx, address, tlsConfig, handler, policies...)
}

func ServeConfigured(ctx context.Context, server config.ServerConfiguration, tlsConfig *tls.Config, handler http.Handler, policy config.HTTPPolicy) error {
	if e := server.ValidateTransport(); e != nil {
		return e
	}
	if (server.EffectiveTransportMode() == config.TransportDirectHTTPS) != (tlsConfig != nil) {
		return errors.New("CONFIG_TRANSPORT_INVALID")
	}
	return serve(ctx, server.ListenAddress, tlsConfig, handler, policy)
}

func serve(ctx context.Context, address string, tlsConfig *tls.Config, handler http.Handler, policies ...config.HTTPPolicy) error {
	policy := config.DefaultRuntimePolicy().HTTP
	if len(policies) > 0 {
		policy = policies[0]
	}
	check := config.DefaultRuntimePolicy()
	check.HTTP = policy
	if e := check.Validate(); e != nil {
		return e
	}
	listener, e := net.Listen("tcp", address)
	if e != nil {
		return e
	}
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Handler: handler, ReadHeaderTimeout: config.Seconds(policy.ReadHeaderSeconds), ReadTimeout: config.Seconds(policy.RequestSeconds), WriteTimeout: config.Seconds(policy.RequestSeconds), IdleTimeout: config.Seconds(policy.IdleSeconds), MaxHeaderBytes: MaxJSONBytes, TLSConfig: tlsConfig}
	servingDone := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-servingDone:
		}
		shutdown, cancel := context.WithTimeout(context.Background(), config.Seconds(policy.ShutdownSeconds))
		defer cancel()
		err := server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		shutdownDone <- err
	}()
	var accepted net.Listener = &bounded{listener, make(chan struct{}, policy.MaxConnections)}
	if tlsConfig != nil {
		accepted = tls.NewListener(accepted, tlsConfig)
	}
	e = server.Serve(accepted)
	close(servingDone)
	if shutdownError := <-shutdownDone; shutdownError != nil {
		return shutdownError
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}

type listenerEndpoint struct {
	listener net.Listener
	handler  http.Handler
	tls      *tls.Config
}

// ListenerGroup owns every prebound socket. Its handlers share one connection
// budget; business request admission and authorization remain handler-owned.
type ListenerGroup struct {
	mu        sync.Mutex
	endpoints []listenerEndpoint
	policy    config.HTTPPolicy
	started   bool
	closed    bool
	cancel    context.CancelFunc
	done      chan struct{}
	result    error
}

// ListenConfigured binds all selected sockets before any request is accepted.
// TLS material must already have passed its owning configuration validation.
func ListenConfigured(server config.ServerConfiguration, browserTLS *tls.Config, browserHandler http.Handler, machine *config.MachineServerConfiguration, machineTLS *tls.Config, machineHandler http.Handler, policy config.HTTPPolicy) (*ListenerGroup, error) {
	bad := errors.New("CONFIG_TRANSPORT_INVALID")
	check := config.DefaultRuntimePolicy()
	check.HTTP = policy
	if server.ValidateTransport() != nil || check.Validate() != nil || browserHandler == nil || (server.EffectiveTransportMode() == config.TransportDirectHTTPS) != (browserTLS != nil) {
		return nil, bad
	}
	if machine == nil {
		if machineTLS != nil || machineHandler != nil {
			return nil, bad
		}
	} else if machine.Validate() != nil || machineTLS == nil || machineHandler == nil || machine.ListenAddress == server.ListenAddress || machine.Origin == server.PortalOrigin || machine.Origin == server.AdminOrigin {
		return nil, bad
	}
	group := &ListenerGroup{policy: policy, done: make(chan struct{})}
	slots := make(chan struct{}, policy.MaxConnections)
	bind := func(address string, tlsConfiguration *tls.Config, handler http.Handler) error {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			return e
		}
		var accepted net.Listener = &bounded{listener, slots}
		if tlsConfiguration != nil {
			tlsConfiguration = tlsConfiguration.Clone()
			accepted = tls.NewListener(accepted, tlsConfiguration)
		}
		group.endpoints = append(group.endpoints, listenerEndpoint{listener: accepted, handler: handler, tls: tlsConfiguration})
		return nil
	}
	if e := bind(server.ListenAddress, browserTLS, browserHandler); e != nil {
		return nil, errors.New("HTTP_LISTENER_BIND_FAILED")
	}
	if machine != nil {
		if e := bind(machine.ListenAddress, machineTLS, machineHandler); e != nil {
			return nil, errors.Join(errors.New("HTTP_LISTENER_BIND_FAILED"), group.Close())
		}
	}
	return group, nil
}

// Close before Serve releases all prebound sockets. During Serve it cancels the
// common request context and waits for both bounded shutdowns. It is idempotent.
func (g *ListenerGroup) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.done == nil {
		g.mu.Unlock()
		return nil
	}
	if !g.started && !g.closed {
		g.closed = true
		for _, endpoint := range g.endpoints {
			if e := endpoint.listener.Close(); e != nil && !errors.Is(e, net.ErrClosed) {
				g.result = errors.Join(g.result, errors.New("HTTP_LISTENER_CLOSE_FAILED"))
			}
		}
		close(g.done)
	}
	if g.cancel != nil {
		g.cancel()
	}
	done := g.done
	g.mu.Unlock()
	<-done
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.result
}

// Serve starts acceptance exactly once. A sibling failure or caller cancellation
// cancels every request, then concurrently drains both servers within one budget.
// A handler that ignores cancellation can exceed that drain budget; forced socket
// closure is reported as failure, never as confirmation of handler cleanup.
func (g *ListenerGroup) Serve(ctx context.Context) error {
	if g == nil || ctx == nil {
		return errors.New("HTTP_LISTENER_CONTEXT_REQUIRED")
	}
	g.mu.Lock()
	if g.done == nil || len(g.endpoints) < 1 || len(g.endpoints) > 2 {
		g.mu.Unlock()
		return errors.New("HTTP_LISTENER_GROUP_INVALID")
	}
	if g.started {
		g.mu.Unlock()
		return errors.New("HTTP_LISTENER_GROUP_ALREADY_STARTED")
	}
	if g.closed {
		g.mu.Unlock()
		return errors.New("HTTP_LISTENER_GROUP_CLOSED")
	}
	g.started = true
	serving, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	g.mu.Unlock()
	defer cancel()
	if serving.Err() != nil {
		var result error
		for _, endpoint := range g.endpoints {
			if e := endpoint.listener.Close(); e != nil && !errors.Is(e, net.ErrClosed) {
				result = errors.Join(result, errors.New("HTTP_LISTENER_CLOSE_FAILED"))
			}
		}
		g.mu.Lock()
		g.closed, g.result = true, result
		close(g.done)
		g.mu.Unlock()
		return result
	}

	servers := make([]*http.Server, 0, len(g.endpoints))
	results := make(chan error, len(g.endpoints))
	for _, endpoint := range g.endpoints {
		server := &http.Server{BaseContext: func(net.Listener) context.Context { return serving }, Handler: endpoint.handler,
			ReadHeaderTimeout: config.Seconds(g.policy.ReadHeaderSeconds), ReadTimeout: config.Seconds(g.policy.RequestSeconds),
			WriteTimeout: config.Seconds(g.policy.RequestSeconds), IdleTimeout: config.Seconds(g.policy.IdleSeconds),
			MaxHeaderBytes: MaxJSONBytes, TLSConfig: endpoint.tls}
		servers = append(servers, server)
		go func(server *http.Server, listener net.Listener) { results <- server.Serve(listener) }(server, endpoint.listener)
	}
	var result error
	remaining := len(servers)
	select {
	case e := <-results:
		remaining--
		if serving.Err() == nil || !errors.Is(e, http.ErrServerClosed) {
			result = errors.New("HTTP_LISTENER_SERVE_FAILED")
		}
	case <-serving.Done():
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), config.Seconds(g.policy.ShutdownSeconds))
	defer stop()
	shutdownResults := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) {
			e := server.Shutdown(shutdown)
			if e != nil {
				// Close does not claim that a cancellation-ignoring handler exited.
				_ = server.Close()
			}
			shutdownResults <- e
		}(server)
	}
	for remaining > 0 {
		if e := <-results; e != nil && !errors.Is(e, http.ErrServerClosed) {
			result = errors.Join(result, errors.New("HTTP_LISTENER_SERVE_FAILED"))
		}
		remaining--
	}
	for range servers {
		if e := <-shutdownResults; e != nil {
			result = errors.Join(result, errors.New("HTTP_LISTENER_SHUTDOWN_FAILED"))
		}
	}
	g.mu.Lock()
	g.closed, g.result = true, result
	close(g.done)
	g.mu.Unlock()
	return result
}
