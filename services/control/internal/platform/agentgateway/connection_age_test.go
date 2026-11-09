package agentgateway

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type heldAgeClaim struct {
	fakeBackend
	started chan time.Time
	finish  chan struct{}
}

func (b *heldAgeClaim) Claim(ctx context.Context, node, credential, request string, input wire.ClaimRequest) (wire.Claim, error) {
	if input.AcknowledgedSequence == 0 {
		return b.fakeBackend.Claim(ctx, node, credential, request, input)
	}
	b.started <- httpserver.ConnectionDrainAt(ctx)
	select {
	case <-ctx.Done():
		return wire.Claim{}, ctx.Err()
	case <-b.finish:
		return wire.Claim{}, nil
	}
}

func TestAgedGatewayFinishesHeldClaimAndPreservesCursor(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	serverTLS := fixture.TLS.Clone()
	serverTLS.NextProtos = []string{"h2", "http/1.1"}
	clientTLS := &tls.Config{RootCAs: fixture.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs,
		ServerName: "example.com", MinVersion: tls.VersionTLS12}
	fixture.Close()
	start := func(backend Backend, maximum time.Duration) func() *grpc.ClientConn {
		t.Helper()
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
			done <- httpserver.ServeListener(ctx, listener, serverTLS.Clone(),
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !Owns(r) {
						http.NotFound(w, r)
						return
					}
					gateway.ServeHTTP(w, r, "127.0.0.1")
				}), config.DefaultHTTPPolicy(), httpserver.ConnectionAge{Maximum: maximum, Grace: 3 * time.Second})
		}()
		t.Cleanup(func() {
			stop()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("dedicated listener did not stop")
			}
		})
		return func() *grpc.ClientConn {
			t.Helper()
			conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS.Clone())))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
	}

	lease := wire.Lease{TaskID: testNode, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("A", 43), Attempt: 1, Sequence: 7}
	first := &heldAgeClaim{fakeBackend: fakeBackend{lease: &lease}, started: make(chan time.Time, 1), finish: make(chan struct{})}
	second := &fakeBackend{}
	older := start(first, 2*time.Second)
	newer := start(second, time.Minute)
	stream := open(t, older())
	if reply, err := exchange(t, stream, heartbeat(1)); err != nil || reply.Disposition == nil {
		t.Fatalf("initial heartbeat: %+v %v", reply, err)
	}
	if reply, err := exchange(t, stream, wire.AgentMessage{ID: 2, Claim: &wire.ClaimRequest{}}); err != nil ||
		reply.Claim == nil || reply.Claim.Lease == nil || reply.Claim.Lease.Sequence != lease.Sequence {
		t.Fatalf("initial claim: %+v %v", reply, err)
	}
	ack := wire.TaskAcknowledgement{TaskID: lease.TaskID, LeaseToken: lease.LeaseToken, Sequence: lease.Sequence}
	if reply, err := exchange(t, stream, wire.AgentMessage{ID: 3, Ack: &ack}); err != nil || reply.Acknowledged == nil {
		t.Fatalf("acknowledge: %+v %v", reply, err)
	}
	claim := wire.AgentMessage{ID: 4, Claim: &wire.ClaimRequest{AcknowledgedSequence: lease.Sequence}}
	if err := stream.SendMsg(&claim); err != nil {
		t.Fatal(err)
	}
	var drain time.Time
	select {
	case drain = <-first.started:
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not start")
	}
	if !time.Now().Before(drain) {
		t.Fatal("claim did not start before connection age")
	}
	timer := time.NewTimer(time.Until(drain))
	defer timer.Stop()
	<-timer.C
	// A queued frame and the age timer are both ready after the held reply.
	queued := heartbeat(5)
	if err := stream.SendMsg(&queued); err != nil {
		t.Fatal(err)
	}
	close(first.finish)
	var reply wire.ControlMessage
	if err := stream.RecvMsg(&reply); err != nil || reply.Validate(claim) != nil {
		t.Fatalf("held claim did not finish: %+v %v", reply, err)
	}
	if err := stream.RecvMsg(&reply); status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "SERVICE_NOT_READY" {
		t.Fatalf("legacy drain status: %v", err)
	}
	first.mu.Lock()
	admitted, acknowledgements := len(first.admitted), len(first.acks)
	first.mu.Unlock()
	if admitted != 4 || acknowledgements != 1 {
		t.Fatalf("aged stream admitted extra work: %d exchanges, %d acknowledgements", admitted, acknowledgements)
	}

	if reply, err := exchange(t, open(t, older()), heartbeat(1)); err != nil || reply.Disposition == nil {
		t.Fatalf("age drained the whole gateway: %+v %v", reply, err)
	}
	reconnected := open(t, newer())
	if reply, err := exchange(t, reconnected, heartbeat(1)); err != nil || reply.Disposition == nil {
		t.Fatalf("next gateway heartbeat: %+v %v", reply, err)
	}
	claim.ID = 2
	if reply, err := exchange(t, reconnected, claim); err != nil || reply.Validate(claim) != nil {
		t.Fatalf("next gateway claim: %+v %v", reply, err)
	}
	second.mu.Lock()
	defer second.mu.Unlock()
	if len(second.cursors) != 1 || second.cursors[0] != lease.Sequence {
		t.Fatalf("next gateway lost acknowledged sequence: %v", second.cursors)
	}
}
