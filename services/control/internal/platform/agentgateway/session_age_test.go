package agentgateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type ageDeadlines struct{ drain, close time.Time }

type ageBackend struct {
	fakeBackend
	deadlines chan ageDeadlines
}

func (b *ageBackend) Heartbeat(ctx context.Context, node, credential string, h wire.Handshake) (wire.Disposition, error) {
	b.deadlines <- ageDeadlines{httpserver.ConnectionDrainAt(ctx), httpserver.ConnectionCloseAt(ctx)}
	return b.fakeBackend.Heartbeat(ctx, node, credential, h)
}

func startAgeGateway(t *testing.T, backend Backend) (*Gateway, *grpc.ClientConn) {
	t.Helper()
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	serverTLS := fixture.TLS.Clone()
	serverTLS.NextProtos = []string{"h2", "http/1.1"}
	clientTLS := &tls.Config{RootCAs: fixture.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs,
		ServerName: "example.com", MinVersion: tls.VersionTLS12}
	fixture.Close()
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
		done <- httpserver.ServeListener(ctx, listener, serverTLS, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gateway.ServeHTTP(w, r, "127.0.0.1")
		}), config.DefaultHTTPPolicy(), httpserver.ConnectionAge{Maximum: time.Minute, Grace: 3 * time.Second})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("owned age listener did not join")
		}
	})
	conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithNoProxy(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return gateway, conn
}

func TestAgeHintMatchesOwnedDedicatedTransport(t *testing.T) {
	backend := &ageBackend{deadlines: make(chan ageDeadlines, 1)}
	gateway, conn := startAgeGateway(t, backend)
	stream := open(t, conn)
	started := time.Now()
	if reply, err := exchange(t, stream, heartbeat(1)); err != nil || reply.Disposition == nil {
		t.Fatalf("first authenticated heartbeat: %+v %v", reply, err)
	}
	headers, err := stream.Header()
	if err != nil {
		t.Fatal(err)
	}
	age, ok := wire.ParseSessionConnectionAge(headers.Get(wire.SessionDrainHeader), headers.Get(wire.SessionCloseHeader), started)
	if !ok {
		t.Fatal("first successful heartbeat omitted its valid age metadata")
	}
	deadlines := <-backend.deadlines
	if age.DrainAt.After(deadlines.drain) || age.CloseAt.After(deadlines.close) || age.CloseAt.Sub(age.DrainAt) != 3*time.Second {
		t.Fatal("relative hint extended or changed the owned transport's deadlines")
	}
	if gateway.connectionHints.Load() != 1 || gateway.connectionHintFailures.Load() != 0 {
		t.Fatal("the successful hint was not measured exactly once")
	}
	outcomes := make(map[string]float64)
	for _, sample := range gateway.Samples() {
		if sample.Name == "ownsolid_agent_gateway_connection_hints_total" {
			if len(sample.Labels) != 1 || sample.Labels[0].Name != "outcome" {
				t.Fatal("hint metrics acquired unbounded or unknown labels")
			}
			outcomes[sample.Labels[0].Value] = sample.Value
		}
	}
	if len(outcomes) != 2 || outcomes["queued"] != 1 || outcomes["failed"] != 0 {
		t.Fatalf("hint outcome metrics: %+v", outcomes)
	}
}

func TestAgeHintFollowsAuthenticationAndPreservesSharedListener(t *testing.T) {
	t.Run("credential_refused", func(t *testing.T) {
		gateway, conn := startAgeGateway(t, &fakeBackend{refusal: errors.New("AGENT_UNAUTHORIZED")})
		stream := open(t, conn)
		if reply, err := exchange(t, stream, heartbeat(1)); err != nil || reply.Problem == nil || reply.Problem.Code != "AGENT_UNAUTHORIZED" {
			t.Fatalf("credential refusal: %+v %v", reply, err)
		}
		headers, _ := stream.Header()
		if len(headers.Get(wire.SessionDrainHeader)) != 0 || len(headers.Get(wire.SessionCloseHeader)) != 0 || gateway.connectionHints.Load() != 0 {
			t.Fatal("unauthenticated reply acquired an age hint")
		}
	})
	t.Run("certificate_refused", func(t *testing.T) {
		gateway, conn := startAgeGateway(t, &fakeBackend{peerRefusal: errors.New("AGENT_CERTIFICATE_REQUIRED")})
		stream := open(t, conn)
		if _, err := exchange(t, stream, heartbeat(1)); err == nil {
			t.Fatal("missing certificate was accepted")
		}
		headers, _ := stream.Header()
		if len(headers.Get(wire.SessionDrainHeader)) != 0 || len(headers.Get(wire.SessionCloseHeader)) != 0 || gateway.connectionHints.Load() != 0 {
			t.Fatal("certificate refusal acquired an age hint")
		}
	})
	t.Run("shared_listener", func(t *testing.T) {
		gateway, conn := start(t, &fakeBackend{}, 4)
		stream := open(t, conn)
		if _, err := exchange(t, stream, heartbeat(1)); err != nil {
			t.Fatal(err)
		}
		headers, _ := stream.Header()
		if len(headers.Get(wire.SessionDrainHeader)) != 0 || len(headers.Get(wire.SessionCloseHeader)) != 0 || gateway.connectionHints.Load() != 0 {
			t.Fatal("shared listener manufactured a dedicated transport age")
		}
	})
}
