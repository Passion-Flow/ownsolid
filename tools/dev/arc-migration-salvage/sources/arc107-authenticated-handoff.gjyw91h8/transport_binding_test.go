package agentgateway

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
)

type observedTransport struct {
	done context.Context
	end  context.CancelFunc
}
type transportBindingKey struct{}
type transportBindingProbe struct {
	mu           sync.Mutex
	live         map[string]*observedTransport
	begins, ends int
}

func addressPair(local, remote net.Addr) string {
	return local.Network() + "|" + local.String() + "|" + remote.Network() + "|" + remote.String()
}
func (p *transportBindingProbe) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	done, end := context.WithCancel(context.Background())
	life := &observedTransport{done: done, end: end}
	p.mu.Lock()
	if p.live == nil {
		p.live = make(map[string]*observedTransport)
	}
	p.live[addressPair(info.LocalAddr, info.RemoteAddr)] = life
	p.begins++
	p.mu.Unlock()
	return context.WithValue(ctx, transportBindingKey{}, life)
}
func (p *transportBindingProbe) HandleConn(ctx context.Context, event stats.ConnStats) {
	if _, ok := event.(*stats.ConnEnd); !ok {
		return
	}
	life := ctx.Value(transportBindingKey{}).(*observedTransport)
	p.mu.Lock()
	for key, value := range p.live {
		if value == life {
			delete(p.live, key)
		}
	}
	p.ends++
	p.mu.Unlock()
	life.end()
}
func (*transportBindingProbe) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (*transportBindingProbe) HandleRPC(context.Context, stats.RPCStats) {}
func (p *transportBindingProbe) bind(t *testing.T, stream grpc.ClientStream) *observedTransport {
	t.Helper()
	actual, ok := peer.FromContext(stream.Context())
	if !ok || actual.LocalAddr == nil || actual.Addr == nil {
		t.Fatal("authenticated stream has no transport peer")
	}
	secured, ok := actual.AuthInfo.(credentials.TLSInfo)
	if !ok || len(secured.State.VerifiedChains) == 0 {
		t.Fatal("transport did not verify its TLS peer")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	life := p.live[addressPair(actual.LocalAddr, actual.Addr)]
	if life == nil {
		t.Fatal("RPC peer does not bind to the transport's TagConn identity")
	}
	return life
}

type connectionReport struct {
	id    string
	drain time.Time
}
type connectionReportingBackend struct {
	fakeBackend
	reports chan connectionReport
}

func (b *connectionReportingBackend) Heartbeat(ctx context.Context, node, credential string, h wire.Handshake) (wire.Disposition, error) {
	b.reports <- connectionReport{httpserver.ConnectionID(ctx), httpserver.ConnectionDrainAt(ctx)}
	return b.fakeBackend.Heartbeat(ctx, node, credential, h)
}

func TestPrototypeAuthenticatedStreamBindsNaturalTransportEnd(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	serverTLS := fixture.TLS.Clone()
	serverTLS.NextProtos = []string{"h2", "http/1.1"}
	clientTLS := &tls.Config{RootCAs: fixture.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, ServerName: "example.com", MinVersion: tls.VersionTLS12}
	fixture.Close()
	backend := &connectionReportingBackend{reports: make(chan connectionReport, 10)}
	gateway, err := New(backend, Policy{MaxSessions: 4, ExchangeSeconds: 3}, problem)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- httpserver.ServeListener(ctx, listener, serverTLS, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r, "127.0.0.1") }), config.DefaultHTTPPolicy(), httpserver.ConnectionAge{Maximum: 2 * time.Second, Grace: time.Second})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("owned listener did not join")
		}
	})
	dial := func() (*grpc.ClientConn, *transportBindingProbe, grpc.ClientStream, connectionReport, *observedTransport) {
		t.Helper()
		tracker := new(transportBindingProbe)
		conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithNoProxy(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS.Clone())), grpc.WithStatsHandler(tracker))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		stream := open(t, conn)
		reply, err := exchange(t, stream, heartbeat(1))
		if err != nil || reply.Disposition == nil || !reply.Disposition.AcceptsSessionRebalance() {
			t.Fatalf("candidate did not authenticate: %+v %v", reply, err)
		}
		report := <-backend.reports
		if report.id == "" || report.drain.IsZero() {
			t.Fatal("listener omitted owned connection timing")
		}
		return conn, tracker, stream, report, tracker.bind(t, stream)
	}
	old, tracker, _, report, life := dial()
	for cycle := 0; cycle < 3; cycle++ {
		timer := time.NewTimer(max(time.Until(report.drain)-700*time.Millisecond, 0))
		<-timer.C
		next, nextTracker, nextStream, nextReport, nextLife := dial()
		if nextReport.id == report.id {
			t.Fatal("replacement reused retiring physical transport")
		}
		closed := make(chan struct{})
		// ConnEnd must never synchronously call ClientConn.Close inside gRPC's callback.
		cleanup := context.AfterFunc(life.done, func() { _ = old.Close(); close(closed) })
		select {
		case <-closed:
			if time.Now().Before(report.drain) {
				t.Fatal("replacement retired old transport before its scheduled age")
			}
		case <-time.After(time.Until(report.drain) + 2*time.Second):
			cleanup()
			t.Fatal("natural transport end did not release the retired ClientConn")
		}
		tracker.mu.Lock()
		begins, ends, live := tracker.begins, tracker.ends, len(tracker.live)
		tracker.mu.Unlock()
		if begins != ends || live != 0 {
			t.Fatalf("retired connection retained transports: begins=%d ends=%d live=%d", begins, ends, live)
		}
		if reply, err := exchange(t, nextStream, heartbeat(2)); err != nil || reply.Disposition == nil {
			t.Fatalf("new session lost ownership when old stream ended: %+v %v", reply, err)
		}
		<-backend.reports
		t.Logf("cycle=%d transport_binding=verified_tls old_natural_end=true old_transport_begins=%d old_transport_ends=%d gateway_sessions=%d", cycle+1, begins, ends, gateway.Sessions())
		old, tracker, report, life = next, nextTracker, nextReport, nextLife
	}
	_ = old.Close()
}
