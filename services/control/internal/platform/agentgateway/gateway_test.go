package agentgateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testNode = "6f1c1d0e-6b1a-4d55-9a3c-2f8b5d1e7a10"

type fakeBackend struct {
	mu        sync.Mutex
	admitted  []string
	heartbeat error
	claim     error
	waits     []time.Duration
	cursors   []uint64
	acks      []wire.TaskAcknowledgement
	lease     *wire.Lease
}

func (b *fakeBackend) Admit(_ context.Context, address, node, credential string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.admitted = append(b.admitted, address+"|"+node+"|"+credential)
	return nil
}

func (b *fakeBackend) Heartbeat(context.Context, string, string, wire.Handshake) (wire.Disposition, error) {
	if b.heartbeat != nil {
		return wire.Disposition{}, b.heartbeat
	}
	return wire.Disposition{Status: "active", ServerTime: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)}, nil
}

func (b *fakeBackend) Claim(_ context.Context, _, _, request string, input wire.ClaimRequest) (wire.Claim, error) {
	b.mu.Lock()
	b.waits = append(b.waits, time.Duration(input.WaitSeconds)*time.Second)
	b.cursors = append(b.cursors, input.AcknowledgedSequence)
	b.mu.Unlock()
	if b.claim != nil {
		return wire.Claim{}, b.claim
	}
	if request == "" {
		return wire.Claim{}, errors.New("VALIDATION_FAILED")
	}
	return wire.Claim{Lease: b.lease}, nil
}

func (b *fakeBackend) Acknowledge(_ context.Context, _, _, request string, ack wire.TaskAcknowledgement) (wire.RenewedLease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if request == "" {
		return wire.RenewedLease{}, errors.New("VALIDATION_FAILED")
	}
	b.acks = append(b.acks, ack)
	return wire.RenewedLease{LeaseExpiresAt: time.Date(2026, 11, 4, 0, 0, 30, 0, time.UTC), ValidForMilliseconds: 30000}, nil
}

func problem(e error) (string, int) {
	if e.Error() == "AGENT_UNAUTHORIZED" {
		return e.Error(), 401
	}
	return "STORAGE_UNAVAILABLE", 503
}

func start(t *testing.T, backend Backend, sessions int) (*Gateway, *grpc.ClientConn) {
	t.Helper()
	g, e := New(backend, Policy{MaxSessions: sessions, ExchangeSeconds: 5}, problem)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Owns(r) {
			http.NotFound(w, r)
			return
		}
		g.ServeHTTP(w, r, "192.0.2.10")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	conn, e := grpc.NewClient("passthrough:///"+strings.TrimPrefix(server.URL, "https://"),
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12})))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	return g, conn
}

func open(t *testing.T, conn *grpc.ClientConn, pairs ...string) grpc.ClientStream {
	t.Helper()
	if pairs == nil {
		pairs = []string{"authorization", "Bearer credential", wire.SessionNodeHeader, testNode, wire.SessionSiteHeader, "default", wire.SessionCellHeader, "default"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, e := conn.NewStream(metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...)), &grpc.StreamDesc{StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true}, wire.SessionPath, grpc.CallContentSubtype(wire.SessionCodec{}.Name()))
	if e != nil {
		t.Fatal(e)
	}
	return stream
}

func exchange(t *testing.T, stream grpc.ClientStream, m wire.AgentMessage) (wire.ControlMessage, error) {
	t.Helper()
	if e := stream.SendMsg(&m); e != nil {
		return wire.ControlMessage{}, e
	}
	var reply wire.ControlMessage
	e := stream.RecvMsg(&reply)
	return reply, e
}

func heartbeat(id uint64) wire.AgentMessage {
	return wire.AgentMessage{ID: id, Heartbeat: &wire.Handshake{ProtocolVersion: wire.Version}}
}

func TestSessionCarriesHeartbeatsAndClaims(t *testing.T) {
	backend := &fakeBackend{lease: &wire.Lease{TaskID: testNode, Operation: wire.CollectFacts, LeaseToken: "token", Attempt: 1}}
	g, conn := start(t, backend, 4)
	stream := open(t, conn)
	reply, e := exchange(t, stream, heartbeat(1))
	if e != nil || reply.Validate(heartbeat(1)) != nil || reply.Disposition.Status != "active" {
		t.Fatalf("heartbeat reply %+v %v", reply, e)
	}
	claim := wire.AgentMessage{ID: 2, Claim: &wire.ClaimRequest{WaitSeconds: 3}}
	reply, e = exchange(t, stream, claim)
	if e != nil || reply.Validate(claim) != nil || reply.Claim.Lease == nil || reply.Claim.Lease.LeaseToken != "token" {
		t.Fatalf("claim reply %+v %v", reply, e)
	}
	if g.Sessions() != 1 {
		t.Fatalf("open sessions %d", g.Sessions())
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.waits) != 1 || backend.waits[0] != 3*time.Second {
		t.Fatalf("claim wait %v", backend.waits)
	}
	if len(backend.admitted) != 2 || backend.admitted[0] != "192.0.2.10|"+testNode+"|credential" {
		t.Fatalf("every exchange is admitted with the client address: %v", backend.admitted)
	}
}

func TestSessionCarriesCursorsAndAcknowledgements(t *testing.T) {
	backend := &fakeBackend{lease: &wire.Lease{TaskID: testNode, Sequence: 8, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("t", 43), Attempt: 1}}
	_, conn := start(t, backend, 4)
	stream := open(t, conn)
	if reply, e := exchange(t, stream, heartbeat(1)); e != nil || reply.Disposition == nil {
		t.Fatalf("heartbeat reply %+v %v", reply, e)
	}
	claim := wire.AgentMessage{ID: 2, Claim: &wire.ClaimRequest{AcknowledgedSequence: 7}}
	reply, e := exchange(t, stream, claim)
	if e != nil || reply.Validate(claim) != nil || reply.Claim.Lease.Sequence != 8 {
		t.Fatalf("claim reply %+v %v", reply, e)
	}
	ack := wire.AgentMessage{ID: 3, Ack: &wire.TaskAcknowledgement{TaskID: testNode, Sequence: 8, LeaseToken: strings.Repeat("t", 43)}}
	reply, e = exchange(t, stream, ack)
	if e != nil || reply.Validate(ack) != nil || reply.Acknowledged.ValidForMilliseconds != 30000 {
		t.Fatalf("ack reply %+v %v", reply, e)
	}
	// An acknowledgement that fails validation closes nothing but is refused in band.
	bad := wire.AgentMessage{ID: 4, Ack: &wire.TaskAcknowledgement{TaskID: "task", Sequence: 8, LeaseToken: strings.Repeat("t", 43)}}
	if reply, e = exchange(t, stream, bad); e == nil && reply.Problem == nil {
		t.Fatalf("invalid ack accepted: %+v", reply)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.cursors) != 1 || backend.cursors[0] != 7 || len(backend.acks) != 1 || backend.acks[0] != *ack.Ack {
		t.Fatalf("cursor %v acks %v", backend.cursors, backend.acks)
	}
}

func TestSessionReportsBackendProblemsInBand(t *testing.T) {
	backend := &fakeBackend{heartbeat: errors.New("AGENT_UNAUTHORIZED"), claim: errors.New("private detail")}
	_, conn := start(t, backend, 4)
	stream := open(t, conn)
	reply, e := exchange(t, stream, heartbeat(1))
	if e != nil || reply.Problem == nil || reply.Problem.Code != "AGENT_UNAUTHORIZED" || reply.Problem.Status != 401 {
		t.Fatalf("heartbeat problem %+v %v", reply, e)
	}
	backend.heartbeat = nil
	if reply, e = exchange(t, stream, heartbeat(2)); e != nil || reply.Disposition == nil {
		t.Fatalf("a failed exchange keeps the session %+v %v", reply, e)
	}
	reply, e = exchange(t, stream, wire.AgentMessage{ID: 3, Claim: &wire.ClaimRequest{}})
	if e != nil || reply.Problem == nil || reply.Problem.Code != "STORAGE_UNAVAILABLE" || reply.Problem.Status != 503 {
		t.Fatalf("claim problem is reduced to its public code %+v %v", reply, e)
	}
}

func TestSessionRefusesBadOpenings(t *testing.T) {
	_, conn := start(t, &fakeBackend{}, 4)
	cases := []struct {
		name  string
		pairs []string
		code  codes.Code
	}{
		{"no credential", []string{wire.SessionNodeHeader, testNode, wire.SessionSiteHeader, "default", wire.SessionCellHeader, "default"}, codes.Unauthenticated},
		{"basic credential", []string{"authorization", "Basic abc", wire.SessionNodeHeader, testNode, wire.SessionSiteHeader, "default", wire.SessionCellHeader, "default"}, codes.Unauthenticated},
		{"bad node", []string{"authorization", "Bearer credential", wire.SessionNodeHeader, "node", wire.SessionSiteHeader, "default", wire.SessionCellHeader, "default"}, codes.InvalidArgument},
		{"other cell", []string{"authorization", "Bearer credential", wire.SessionNodeHeader, testNode, wire.SessionSiteHeader, "default", wire.SessionCellHeader, "other"}, codes.NotFound},
		{"no scope", []string{"authorization", "Bearer credential", wire.SessionNodeHeader, testNode}, codes.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, e := exchange(t, open(t, conn, c.pairs...), heartbeat(1))
			if status.Code(e) != c.code {
				t.Fatalf("got %v, want %v", e, c.code)
			}
		})
	}
	t.Run("claim before heartbeat", func(t *testing.T) {
		_, e := exchange(t, open(t, conn), wire.AgentMessage{ID: 1, Claim: &wire.ClaimRequest{}})
		if status.Code(e) != codes.InvalidArgument {
			t.Fatalf("got %v", e)
		}
	})
	t.Run("message id out of order", func(t *testing.T) {
		stream := open(t, conn)
		if _, e := exchange(t, stream, heartbeat(1)); e != nil {
			t.Fatal(e)
		}
		if _, e := exchange(t, stream, heartbeat(3)); status.Code(e) != codes.InvalidArgument {
			t.Fatalf("got %v", e)
		}
	})
	t.Run("claim wait above the interval", func(t *testing.T) {
		stream := open(t, conn)
		exchange(t, stream, heartbeat(1))
		if _, e := exchange(t, stream, wire.AgentMessage{ID: 2, Claim: &wire.ClaimRequest{WaitSeconds: wire.MaxClaimWaitSeconds + 1}}); status.Code(e) != codes.InvalidArgument {
			t.Fatalf("got %v", e)
		}
	})
}

func TestNewerSessionReplacesOlder(t *testing.T) {
	g, conn := start(t, &fakeBackend{}, 4)
	first := open(t, conn)
	if _, e := exchange(t, first, heartbeat(1)); e != nil {
		t.Fatal(e)
	}
	second := open(t, conn)
	if _, e := exchange(t, second, heartbeat(1)); e != nil {
		t.Fatal(e)
	}
	var reply wire.ControlMessage
	if e := first.RecvMsg(&reply); status.Code(e) != codes.Aborted || !strings.Contains(e.Error(), "AGENT_SESSION_REPLACED") {
		t.Fatalf("older session %v", e)
	}
	if _, e := exchange(t, second, heartbeat(2)); e != nil {
		t.Fatalf("newer session continues: %v", e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for g.Sessions() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.Sessions() != 1 {
		t.Fatalf("open sessions %d", g.Sessions())
	}
}

func TestSessionCapacityIsBounded(t *testing.T) {
	g, conn := start(t, &fakeBackend{}, 1)
	first := open(t, conn)
	if _, e := exchange(t, first, heartbeat(1)); e != nil {
		t.Fatal(e)
	}
	if _, e := exchange(t, open(t, conn), heartbeat(1)); status.Code(e) != codes.ResourceExhausted {
		t.Fatalf("second session %v", e)
	}
	if _, ok := g.Hold(); ok {
		t.Fatal("a long-poll claim cannot take a full gateway's slot")
	}
	first.CloseSend()
	var reply wire.ControlMessage
	first.RecvMsg(&reply)
	deadline := time.Now().Add(5 * time.Second)
	for g.Sessions() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	release, ok := g.Hold()
	if !ok {
		t.Fatal("a closed session frees its slot")
	}
	release()
}

func TestOwnsOnlyHTTP2GRPCSessions(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, wire.SessionPath, nil)
	r.Header.Set("Content-Type", "application/grpc+json")
	if Owns(r) {
		t.Fatal("HTTP/1.1 request is not a session")
	}
	r.ProtoMajor = 2
	if !Owns(r) {
		t.Fatal("HTTP/2 gRPC request is a session")
	}
	r.Header.Set("Content-Type", "application/json")
	if Owns(r) {
		t.Fatal("JSON request is not a session")
	}
}

func TestNewRefusesIncompletePolicy(t *testing.T) {
	for _, p := range []Policy{{MaxSessions: 0, ExchangeSeconds: 1}, {MaxSessions: 1}} {
		if _, e := New(&fakeBackend{}, p, problem); e == nil {
			t.Fatalf("policy %+v accepted", p)
		}
	}
	if _, e := New(nil, Policy{MaxSessions: 1, ExchangeSeconds: 1}, problem); e == nil {
		t.Fatal("missing backend accepted")
	}
}

func TestSilentSessionIsClosed(t *testing.T) {
	saved := idleTimeout
	idleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { idleTimeout = saved })
	g, conn := start(t, &fakeBackend{}, 4)
	stream := open(t, conn)
	if _, e := exchange(t, stream, heartbeat(1)); e != nil {
		t.Fatal(e)
	}
	var reply wire.ControlMessage
	if e := stream.RecvMsg(&reply); status.Code(e) != codes.DeadlineExceeded || !strings.Contains(e.Error(), "AGENT_SESSION_IDLE") {
		t.Fatalf("silent session %v", e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for g.Sessions() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.Sessions() != 0 {
		t.Fatalf("open sessions %d", g.Sessions())
	}
}
