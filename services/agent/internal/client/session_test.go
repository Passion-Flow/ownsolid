package client

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const sessionNode = "3b0c8f1e-2d4a-4c6b-8e9f-1a2b3c4d5e6f"

// control is a stand-in control plane: the session stream on HTTP/2 and the HTTPS
// heartbeat and claims paths, recording which transport each exchange used.
type control struct {
	mu        sync.Mutex
	exchanges []string
	waits     []int
	headers   metadata.MD
	refuse    codes.Code
	problem   *wire.SessionProblem
	closeAt   int
	streams   int
	lease     *wire.Lease
	httpDown  bool
}

func (c *control) record(kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exchanges = append(c.exchanges, kind)
}

func (c *control) connect(stream grpc.ServerStream) error {
	c.mu.Lock()
	c.streams++
	refuse, closeAt := c.refuse, c.closeAt
	c.headers, _ = metadata.FromIncomingContext(stream.Context())
	c.mu.Unlock()
	if refuse != codes.OK {
		return status.Error(refuse, "AUTH_CAPACITY_EXCEEDED")
	}
	for n := 1; ; n++ {
		var m wire.AgentMessage
		if e := stream.RecvMsg(&m); e != nil {
			return nil
		}
		if closeAt > 0 && n > closeAt {
			return status.Error(codes.DeadlineExceeded, "AGENT_SESSION_IDLE")
		}
		reply := wire.ControlMessage{ReplyTo: m.ID}
		c.mu.Lock()
		problem := c.problem
		c.mu.Unlock()
		switch {
		case problem != nil:
			reply.Problem = problem
		case m.Heartbeat != nil:
			c.record("stream heartbeat")
			reply.Disposition = &wire.Disposition{Status: "active", ServerTime: time.Now().UTC()}
		default:
			c.record("stream claim")
			c.mu.Lock()
			c.waits = append(c.waits, m.Claim.WaitSeconds)
			reply.Claim = &wire.Claim{Lease: c.lease}
			c.mu.Unlock()
		}
		if e := stream.SendMsg(&reply); e != nil {
			return nil
		}
	}
}

func (c *control) http(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	down := c.httpDown
	c.mu.Unlock()
	base := "/agent/v1/sites/default/cells/default/nodes/" + sessionNode
	if down || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("C", 43) {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{"code": "STORAGE_UNAVAILABLE"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case base + "/heartbeat":
		c.record("https heartbeat")
		json.NewEncoder(w).Encode(map[string]any{"data": wire.Disposition{Status: "active", ServerTime: time.Now().UTC()}})
	case base + "/tasks/claims":
		var request wire.ClaimRequest
		if json.Unmarshal(body, &request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.record("https claim")
		c.mu.Lock()
		c.waits = append(c.waits, request.WaitSeconds)
		c.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": wire.Claim{Lease: c.lease}})
	default:
		http.NotFound(w, r)
	}
}

func (c *control) log() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.exchanges...)
}

// serve starts control; with http2 false it speaks only HTTP/1.1, like many proxies.
func serve(t *testing.T, c *control, http2 bool) *Session {
	t.Helper()
	g := grpc.NewServer()
	g.RegisterService(&grpc.ServiceDesc{ServiceName: wire.SessionService, HandlerType: (*any)(nil), Streams: []grpc.StreamDesc{{
		StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true,
		Handler: func(_ any, stream grpc.ServerStream) error { return c.connect(stream) },
	}}}, nil)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && r.URL.Path == wire.SessionPath {
			g.ServeHTTP(w, r)
			return
		}
		c.http(w, r)
	}))
	server.EnableHTTP2 = http2
	server.StartTLS()
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	connection, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	connection.Credential = strings.Repeat("C", 43)
	t.Cleanup(connection.Close)
	session := connection.Session(sessionNode)
	t.Cleanup(session.Close)
	return session
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSessionUsesTheStream(t *testing.T) {
	c := &control{lease: &wire.Lease{TaskID: sessionNode, Operation: wire.CollectFacts, LeaseToken: "lease", Attempt: 1}}
	s := serve(t, c, true)
	ctx := t.Context()
	if d, e := s.Heartbeat(ctx, wire.Handshake{ProtocolVersion: wire.Version}); e != nil || d.Status != "active" {
		t.Fatalf("heartbeat %+v %v", d, e)
	}
	claim, e := s.Claim(ctx, wire.MaxClaimWaitSeconds)
	if e != nil || claim.Lease == nil || claim.Lease.LeaseToken != "lease" {
		t.Fatalf("claim %+v %v", claim, e)
	}
	if _, e = s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.log(); !equal(got, []string{"stream heartbeat", "stream claim", "stream heartbeat"}) {
		t.Fatalf("exchanges %v", got)
	}
	if s.Transport() != TransportStream || c.streams != 1 || c.waits[0] != wire.MaxClaimWaitSeconds {
		t.Fatalf("transport %q streams %d waits %v", s.Transport(), c.streams, c.waits)
	}
	if c.headers.Get("authorization")[0] != "Bearer "+strings.Repeat("C", 43) || c.headers.Get(wire.SessionNodeHeader)[0] != sessionNode || c.headers.Get(wire.SessionSiteHeader)[0] != "default" || c.headers.Get(wire.SessionCellHeader)[0] != "default" {
		t.Fatalf("session metadata %v", c.headers)
	}
}

func TestSessionFallsBackToLongPolling(t *testing.T) {
	c := &control{}
	s := serve(t, c, false)
	current := time.Now()
	s.now = func() time.Time { return current }
	ctx := t.Context()
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Claim(ctx, wire.MaxClaimWaitSeconds); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.log(); !equal(got, []string{"https heartbeat", "https claim", "https heartbeat"}) {
		t.Fatalf("exchanges %v", got)
	}
	if s.Transport() != TransportHTTPS || c.waits[0] != wire.MaxClaimWaitSeconds {
		t.Fatalf("transport %q waits %v", s.Transport(), c.waits)
	}
	// After the retry window the stream is tried again; it still fails, so HTTPS continues.
	current = current.Add(time.Duration(wire.SessionFallbackRetrySeconds) * time.Second)
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if !s.fallbackUntil.After(current) {
		t.Fatal("a failed retry starts a new fallback window")
	}
}

func TestSessionReturnsToTheStreamAfterTheWindow(t *testing.T) {
	c := &control{}
	s := serve(t, c, true)
	current := time.Now()
	s.now = func() time.Time { return current }
	s.fallbackUntil = current.Add(time.Minute)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil || s.Transport() != TransportHTTPS {
		t.Fatalf("inside the window HTTPS is used: %v %q", e, s.Transport())
	}
	current = current.Add(time.Minute)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil || s.Transport() != TransportStream {
		t.Fatalf("after the window the stream is used: %v %q", e, s.Transport())
	}
	if got := c.log(); !equal(got, []string{"https heartbeat", "stream heartbeat"}) {
		t.Fatalf("exchanges %v", got)
	}
}

func TestSessionDoesNotFallBackWhenControlIsDown(t *testing.T) {
	c := &control{httpDown: true}
	s := serve(t, c, false)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e == nil {
		t.Fatal("heartbeat succeeded with control down")
	}
	if !s.fallbackUntil.IsZero() {
		t.Fatal("an unreachable control is not a reason to leave the stream")
	}
}

func TestSessionReopensAStreamClosedWhileIdle(t *testing.T) {
	c := &control{closeAt: 1}
	s := serve(t, c, true)
	for i := 0; i < 3; i++ {
		if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
			t.Fatalf("heartbeat %d: %v", i, e)
		}
	}
	if c.streams != 3 || s.Transport() != TransportStream {
		t.Fatalf("streams %d transport %q", c.streams, s.Transport())
	}
	if got := c.log(); !equal(got, []string{"stream heartbeat", "stream heartbeat", "stream heartbeat"}) {
		t.Fatalf("exchanges %v", got)
	}
}

func TestSessionReportsControlProblems(t *testing.T) {
	c := &control{problem: &wire.SessionProblem{Code: "AGENT_UNAUTHORIZED", Status: 401}}
	s := serve(t, c, true)
	_, e := s.Heartbeat(t.Context(), wire.Handshake{})
	var p *Problem
	if !errors.As(e, &p) || p.Status != 401 || p.Code != "AGENT_UNAUTHORIZED" {
		t.Fatalf("problem %v", e)
	}
	c.problem = &wire.SessionProblem{Code: "PRIVATE_DETAIL", Status: 503}
	if _, e = s.Claim(t.Context(), 0); e == nil || e.Error() != "CONTROL_RESPONSE_REJECTED" {
		t.Fatalf("unknown code %v", e)
	}
}

func TestRefusedSessionDoesNotFallBack(t *testing.T) {
	c := &control{refuse: codes.ResourceExhausted}
	s := serve(t, c, true)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); !errors.Is(e, errStreamRefused) {
		t.Fatalf("refused session %v", e)
	}
	if !s.fallbackUntil.IsZero() || len(c.log()) != 0 {
		t.Fatal("a refused stream backs off instead of moving to HTTPS")
	}
}

func TestClaimWaitIsBounded(t *testing.T) {
	s := serve(t, &control{}, true)
	if _, e := s.Claim(t.Context(), wire.MaxClaimWaitSeconds+1); e == nil {
		t.Fatal("wait above the interval accepted")
	}
}
