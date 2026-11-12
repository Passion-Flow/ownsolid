package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"github.com/google/uuid"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
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
	if server.PortalListenAddress != "" {
		group, err := ListenConfigured(server, tlsConfig, handler, nil, nil, nil, policy)
		if err != nil {
			return err
		}
		defer group.Close()
		return group.Serve(ctx)
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
	return serveListener(ctx, listener, tlsConfig, handler, policy, ConnectionAge{})
}

// ServeListener serves and ages connections on a dedicated prebound TLS listener.
// It closes the listener and returns after the bounded shutdown that ctx starts.
func ServeListener(ctx context.Context, listener net.Listener, tlsConfig *tls.Config, handler http.Handler, policy config.HTTPPolicy, age ConnectionAge) error {
	check := config.DefaultRuntimePolicy()
	check.HTTP = policy
	if listener == nil || tlsConfig == nil || check.Validate() != nil || age.Maximum <= 0 || age.Grace <= 0 {
		if listener != nil {
			listener.Close()
		}
		return errors.New("CONFIG_TRANSPORT_INVALID")
	}
	return serveListener(ctx, listener, tlsConfig, handler, policy, age)
}

func serveListener(ctx context.Context, listener net.Listener, tlsConfig *tls.Config, handler http.Handler, policy config.HTTPPolicy, age ConnectionAge) error {
	if age.Maximum > 0 {
		return serveAgedListener(ctx, listener, tlsConfig, handler, policy, age)
	}
	server := newHTTPServer(ctx, tlsConfig, handler, policy)
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
	e := server.Serve(accepted)
	close(servingDone)
	if shutdownError := <-shutdownDone; shutdownError != nil {
		return shutdownError
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}

func newHTTPServer(ctx context.Context, tlsConfig *tls.Config, handler http.Handler, policy config.HTTPPolicy) *http.Server {
	return &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler:           handler,
		ReadHeaderTimeout: config.Seconds(policy.ReadHeaderSeconds),
		ReadTimeout:       config.Seconds(policy.RequestSeconds),
		WriteTimeout:      config.Seconds(policy.RequestSeconds),
		IdleTimeout:       config.Seconds(policy.IdleSeconds),
		MaxHeaderBytes:    MaxJSONBytes,
		TLSConfig:         tlsConfig,
	}
}

type listenerEndpoint struct {
	listener net.Listener
	handler  http.Handler
	tls      *tls.Config
}

// BSD may allow overlapping wildcard binds. Inspect the owned actual addresses
// before serving rather than relying on kernel refusal or resolving DNS twice.
func listenersOverlap(left, right net.Addr) bool {
	a, okA := left.(*net.TCPAddr)
	b, okB := right.(*net.TCPAddr)
	return okA && okB && a.Port == b.Port && (a.IP.IsUnspecified() || b.IP.IsUnspecified() || a.IP.Equal(b.IP))
}

// A split socket must not expose the sibling origin even with valid Host/SNI.
// The unchanged dispatcher still owns sessions, CSRF and business authorization.
func browserEntrance(server config.ServerConfiguration, audience string, next http.Handler, policy config.HTTPPolicy, rejected *atomic.Uint64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := ResolveBrowserRequest(r, server)
		if err == nil && connection.Audience != audience {
			err = errors.New("MISDIRECTED_REQUEST")
		}
		if err == nil {
			next.ServeHTTP(w, r)
			return
		}
		requestID := uuid.NewString()
		rejected.Add(1)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		AuditTransport(r.Context(), requestID, connection, false)
		ProblemWithPolicy(w, requestID, err, policy)
	})
}

// ListenerGroup owns every prebound socket. Its handlers share one connection
// budget; business request admission and authorization remain handler-owned.
type ListenerGroup struct {
	mu           sync.Mutex
	endpoints    []listenerEndpoint
	policy       config.HTTPPolicy
	started      bool
	closed       bool
	cancel       context.CancelFunc
	done         chan struct{}
	result       error
	splitBrowser bool
	rejected     [2]atomic.Uint64
}

// Samples exposes fixed per-browser rejection counters for the serving metrics owner.
func (g *ListenerGroup) Samples() []telemetrystore.Sample {
	if g == nil || !g.splitBrowser {
		return nil
	}
	return []telemetrystore.Sample{
		{Name: "ownsolid_http_entrance_rejections_total", Value: float64(g.rejected[0].Load()), Labels: []telemetrystore.Label{{Name: "audience", Value: "admin"}}},
		{Name: "ownsolid_http_entrance_rejections_total", Value: float64(g.rejected[1].Load()), Labels: []telemetrystore.Label{{Name: "audience", Value: "portal"}}},
	}
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
	} else if machine.Validate() != nil || machineTLS == nil || machineHandler == nil || machine.ListenAddress == server.ListenAddress || machine.ListenAddress == server.PortalListenAddress || machine.Origin == server.PortalOrigin || machine.Origin == server.AdminOrigin {
		return nil, bad
	}
	group := &ListenerGroup{policy: policy, done: make(chan struct{}), splitBrowser: server.PortalListenAddress != ""}
	slots := make(chan struct{}, policy.MaxConnections)
	bind := func(address string, tlsConfiguration *tls.Config, handler http.Handler) error {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			return e
		}
		for _, endpoint := range group.endpoints {
			if listenersOverlap(listener.Addr(), endpoint.listener.Addr()) {
				_ = listener.Close()
				return errors.New("HTTP_LISTENER_BIND_FAILED")
			}
		}
		var accepted net.Listener = &bounded{listener, slots}
		if tlsConfiguration != nil {
			tlsConfiguration = tlsConfiguration.Clone()
			accepted = tls.NewListener(accepted, tlsConfiguration)
		}
		group.endpoints = append(group.endpoints, listenerEndpoint{listener: accepted, handler: handler, tls: tlsConfiguration})
		return nil
	}
	adminHandler := browserHandler
	if server.PortalListenAddress != "" {
		adminHandler = browserEntrance(server, "admin", browserHandler, policy, &group.rejected[0])
	}
	if e := bind(server.ListenAddress, browserTLS, adminHandler); e != nil {
		return nil, errors.New("HTTP_LISTENER_BIND_FAILED")
	}
	if server.PortalListenAddress != "" {
		if e := bind(server.PortalListenAddress, browserTLS, browserEntrance(server, "portal", browserHandler, policy, &group.rejected[1])); e != nil {
			return nil, errors.Join(errors.New("HTTP_LISTENER_BIND_FAILED"), group.Close())
		}
	}
	if machine != nil {
		if e := bind(machine.ListenAddress, machineTLS, machineHandler); e != nil {
			return nil, errors.Join(errors.New("HTTP_LISTENER_BIND_FAILED"), group.Close())
		}
	}
	return group, nil
}

// Close before Serve releases all prebound sockets. During Serve it cancels the
// common request context and waits for every bounded shutdown. It is idempotent.
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
// cancels every request, then concurrently drains every server within one budget.
// A handler that ignores cancellation can exceed that drain budget; forced socket
// closure is reported as failure, never as confirmation of handler cleanup.
func (g *ListenerGroup) Serve(ctx context.Context) error {
	if g == nil || ctx == nil {
		return errors.New("HTTP_LISTENER_CONTEXT_REQUIRED")
	}
	g.mu.Lock()
	// Two browser entrances and the optional machine entrance share this owner.
	if g.done == nil || len(g.endpoints) < 1 || len(g.endpoints) > 3 {
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
