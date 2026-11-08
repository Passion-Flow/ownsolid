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
	mu              sync.Mutex
	exchanges       []string
	waits           []int
	cursors         []uint64
	acks            []string
	dropAck         bool
	headers         metadata.MD
	refuse          codes.Code
	problem         *wire.SessionProblem
	heartbeatStatus string
	closeAt         int
	streams         int
	lease           *wire.Lease
	httpDown        bool
	pacing          *wire.Pacing
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
			c.mu.Lock()
			status := c.heartbeatStatus
			if status == "" {
				status = "active"
			}
			reply.Disposition = &wire.Disposition{Status: status, ServerTime: time.Now().UTC(), Pacing: c.pacing}
			c.mu.Unlock()
		case m.Ack != nil:
			c.mu.Lock()
			drop := c.dropAck
			c.mu.Unlock()
			if drop {
				return status.Error(codes.Unavailable, "CONNECTION_LOST")
			}
			c.record("stream ack")
			c.mu.Lock()
			c.acks = append(c.acks, m.Ack.TaskID)
			c.mu.Unlock()
			reply.Acknowledged = &wire.RenewedLease{LeaseExpiresAt: time.Now().UTC().Add(30 * time.Second), ValidForMilliseconds: 30000}
		default:
			c.record("stream claim")
			c.mu.Lock()
			c.waits = append(c.waits, m.Claim.WaitSeconds)
			c.cursors = append(c.cursors, m.Claim.AcknowledgedSequence)
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
		c.mu.Lock()
		pacing := c.pacing
		c.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": wire.Disposition{Status: "active", ServerTime: time.Now().UTC(), Pacing: pacing}})
	case base + "/tasks/claims":
		var request wire.ClaimRequest
		if json.Unmarshal(body, &request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.record("https claim")
		c.mu.Lock()
		c.waits = append(c.waits, request.WaitSeconds)
		c.cursors = append(c.cursors, request.AcknowledgedSequence)
		c.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": wire.Claim{Lease: c.lease}})
	case base + "/tasks/" + sessionNode + "/acknowledgements":
		var request wire.TaskAcknowledgementRequest
		if json.Unmarshal(body, &request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.record("https ack")
		c.mu.Lock()
		c.acks = append(c.acks, sessionNode)
		c.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": wire.RenewedLease{LeaseExpiresAt: time.Now().UTC().Add(30 * time.Second), ValidForMilliseconds: 30000}})
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
	server := start(t, c, http2)
	connection := connect(t, server)
	session := connection.Session(sessionNode)
	t.Cleanup(session.Close)
	return session
}

// start serves c's session stream and HTTPS paths. Every test server presents the same
// certificate, so an agent gateway verifies for the control origin's host as control does.
func start(t *testing.T, c *control, http2 bool) *httptest.Server {
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
	return server
}

func connect(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
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
	return connection
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
	claim, e := s.Claim(ctx, wire.MaxClaimWaitSeconds, 0)
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
	// One attempt makes one request: the stream fails, and the next attempt tries HTTPS.
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e == nil {
		t.Fatal("the attempt on the stream succeeded")
	}
	if got := c.log(); len(got) != 0 {
		t.Fatalf("a failed stream attempt also made %v", got)
	}
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if !s.fallbackUntil.IsZero() {
		t.Fatal("one stream failure left the stream: that is also what a control restart looks like")
	}
	if _, e := s.Claim(ctx, wire.MaxClaimWaitSeconds, 0); e != nil {
		t.Fatal(e)
	}
	// It fails again while HTTPS works, so the path lacks HTTP/2 and the agent stays on HTTPS.
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e == nil {
		t.Fatal("the second attempt on the stream succeeded")
	}
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if s.fallbackUntil.IsZero() {
		t.Fatal("the stream failed twice while HTTPS worked, and the agent stayed on it")
	}
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.log(); !equal(got, []string{"https heartbeat", "https claim", "https heartbeat", "https heartbeat"}) {
		t.Fatalf("exchanges %v", got)
	}
	if s.Transport() != TransportHTTPS || c.waits[0] != wire.MaxClaimWaitSeconds {
		t.Fatalf("transport %q waits %v", s.Transport(), c.waits)
	}
	// After the retry window the stream is tried again, and the same two failures start a
	// new window.
	current = current.Add(time.Duration(wire.SessionFallbackRetrySeconds) * time.Second)
	for range wire.SessionFallbackProbes {
		if _, e := s.Heartbeat(ctx, wire.Handshake{}); e == nil {
			t.Fatal("the stream worked after the window")
		}
		if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
			t.Fatal(e)
		}
	}
	if !s.fallbackUntil.After(current) {
		t.Fatal("a failed retry starts a new fallback window")
	}
}

func TestSessionReturnsToTheStreamAfterAControlRestart(t *testing.T) {
	c := &control{refuse: codes.Unavailable}
	s := serve(t, c, true)
	ctx := t.Context()
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e == nil {
		t.Fatal("the stream worked during the restart")
	}
	// Control is back: HTTPS answers the probe, and the next attempt is on the stream.
	c.refuse = codes.OK
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil || s.Transport() != TransportHTTPS {
		t.Fatalf("probe: %v %q", e, s.Transport())
	}
	if !s.fallbackUntil.IsZero() {
		t.Fatal("one restart put the agent on HTTPS for the whole fallback window")
	}
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil || s.Transport() != TransportStream {
		t.Fatalf("after the probe the stream is used: %v %q", e, s.Transport())
	}
	// A later restart counts from the start: the good stream cleared the first probe.
	c.refuse = codes.Unavailable
	s.dropStream()
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e == nil {
		t.Fatal("the stream worked during the second restart")
	}
	if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if !s.fallbackUntil.IsZero() {
		t.Fatal("two restarts apart were read as a path without HTTP/2")
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
	// The attempts alternate between the transports, one request each, and neither leaves
	// the stream: an unreachable control says nothing about the path to it.
	for attempt := range 4 {
		if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e == nil {
			t.Fatalf("attempt %d succeeded with control down", attempt)
		}
		if want := attempt%2 == 0; s.probeHTTPS != want {
			t.Fatalf("attempt %d: next attempt on HTTPS %v", attempt, s.probeHTTPS)
		}
		if !s.fallbackUntil.IsZero() {
			t.Fatal("an unreachable control is not a reason to leave the stream")
		}
	}
}

func TestSessionUsesHTTPSWhenItCannotOpenAStream(t *testing.T) {
	c := &control{}
	s := serve(t, c, true)
	s.client.tls = nil
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if s.Transport() != TransportHTTPS || s.fallbackUntil.IsZero() || c.streams != 0 {
		t.Fatalf("transport %q streams %d", s.Transport(), c.streams)
	}
}

func TestSessionDoesNotRetryABrokenStream(t *testing.T) {
	c := &control{closeAt: 1}
	s := serve(t, c, true)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	// The break is the run loop's to retry, after its backoff.
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e == nil {
		t.Fatal("the broken stream was retried inside the heartbeat")
	}
	if c.streams != 1 {
		t.Fatalf("streams %d", c.streams)
	}
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if c.streams != 2 || s.Transport() != TransportStream {
		t.Fatalf("streams %d transport %q", c.streams, s.Transport())
	}
	if got := c.log(); !equal(got, []string{"stream heartbeat", "stream heartbeat"}) {
		t.Fatalf("exchanges %v", got)
	}
}

func TestSessionReplacesAStreamControlHasClosedForIdleness(t *testing.T) {
	c := &control{closeAt: 1}
	s := serve(t, c, true)
	current := time.Now()
	s.now = func() time.Time { return current }
	for i := range 3 {
		if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
			t.Fatalf("heartbeat %d: %v", i, e)
		}
		// A task ran for longer than control keeps a silent session.
		current = current.Add(time.Duration(s.Pacing().IdleSeconds()) * time.Second)
	}
	if c.streams != 3 {
		t.Fatalf("streams %d", c.streams)
	}
}

func TestSessionAdoptsPacingFromControl(t *testing.T) {
	c := &control{}
	s := serve(t, c, true)
	policy := s.client.Policy()
	initial := s.Pacing()
	if initial.HeartbeatSeconds != wire.HeartbeatSeconds || initial.ReconnectBaseSeconds != policy.BackoffInitialSeconds || initial.ReconnectMaxSeconds != policy.BackoffMaxSeconds || initial.Validate() != nil {
		t.Fatalf("pacing before control spoke %+v", initial)
	}
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if s.Pacing() != initial {
		t.Fatal("a reply without pacing changed it")
	}
	slow := wire.Pacing{HeartbeatSeconds: 60, JitterPercent: 20, ReconnectBaseSeconds: 5, ReconnectMaxSeconds: 300}
	c.mu.Lock()
	c.pacing = &slow
	c.mu.Unlock()
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if s.Pacing() != slow {
		t.Fatalf("pacing %+v", s.Pacing())
	}
	for _, invalid := range []wire.Pacing{{}, {HeartbeatSeconds: 1, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 1}, {HeartbeatSeconds: 10, ReconnectBaseSeconds: 9, ReconnectMaxSeconds: 1}} {
		c.mu.Lock()
		c.pacing = &invalid
		c.mu.Unlock()
		if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
			t.Fatal(e)
		}
		if s.Pacing() != slow {
			t.Fatalf("invalid pacing %+v was adopted", invalid)
		}
	}
}

func TestSessionAdoptsPacingOverHTTPS(t *testing.T) {
	c := &control{pacing: &wire.Pacing{HeartbeatSeconds: 30, ReconnectBaseSeconds: 2, ReconnectMaxSeconds: 60}}
	s := serve(t, c, true)
	s.fallbackUntil = time.Now().Add(time.Minute)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if s.Transport() != TransportHTTPS || s.Pacing() != *c.pacing {
		t.Fatalf("transport %q pacing %+v", s.Transport(), s.Pacing())
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
	if _, e = s.Claim(t.Context(), 0, 0); e == nil || e.Error() != "CONTROL_RESPONSE_REJECTED" {
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

func TestRevokedCredentialOnTheStreamEndsTheAgent(t *testing.T) {
	c := &control{refuse: codes.Unauthenticated}
	s := serve(t, c, true)
	_, e := s.Heartbeat(t.Context(), wire.Handshake{})
	var p *Problem
	if !errors.As(e, &p) || p.Status != http.StatusUnauthorized || p.Code != "AGENT_UNAUTHORIZED" {
		t.Fatalf("revoked session %v", e)
	}
	if _, e = s.Heartbeat(t.Context(), wire.Handshake{}); !errors.As(e, &p) || len(c.log()) != 0 {
		t.Fatalf("a revoked stream moved to HTTPS: %v", e)
	}
}

func TestClaimWaitIsBounded(t *testing.T) {
	s := serve(t, &control{}, true)
	if _, e := s.Claim(t.Context(), wire.MaxClaimWaitSeconds+1, 0); e == nil {
		t.Fatal("wait above the interval accepted")
	}
}

func TestSessionCarriesTheCursorAndAcknowledges(t *testing.T) {
	lease := wire.Lease{TaskID: sessionNode, Sequence: 4, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("l", 43), Attempt: 1}
	for _, http2 := range []bool{true, false} {
		c := &control{lease: &lease}
		s := serve(t, c, http2)
		ctx := t.Context()
		if !http2 {
			// The first attempt finds that the stream does not work.
			if _, e := s.Heartbeat(ctx, wire.Handshake{}); e == nil {
				t.Fatal("stream worked without HTTP/2")
			}
		}
		if _, e := s.Heartbeat(ctx, wire.Handshake{}); e != nil {
			t.Fatal(e)
		}
		claim, e := s.Claim(ctx, 0, 3)
		if e != nil || claim.Lease == nil || claim.Lease.Sequence != 4 {
			t.Fatalf("claim %+v %v", claim, e)
		}
		renewed, e := s.Acknowledge(ctx, *claim.Lease)
		if e != nil || renewed.ValidForMilliseconds != 30000 {
			t.Fatalf("ack %+v %v", renewed, e)
		}
		want := []string{"stream heartbeat", "stream claim", "stream ack"}
		if !http2 {
			want = []string{"https heartbeat", "https claim", "https ack"}
		}
		if got := c.log(); !equal(got, want) || c.cursors[0] != 3 {
			t.Fatalf("exchanges %v cursors %v", got, c.cursors)
		}
	}
}

func TestAcknowledgementRetriesOverHTTPSWhenTheStreamBreaks(t *testing.T) {
	lease := wire.Lease{TaskID: sessionNode, Sequence: 1, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("l", 43), Attempt: 1}
	c := &control{lease: &lease, dropAck: true}
	s := serve(t, c, true)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Acknowledge(t.Context(), lease); e != nil {
		t.Fatal(e)
	}
	if got := c.log(); !equal(got, []string{"stream heartbeat", "https ack"}) {
		t.Fatalf("exchanges %v", got)
	}
	if _, e := s.Acknowledge(t.Context(), wire.Lease{TaskID: "task", LeaseToken: lease.LeaseToken}); e == nil {
		t.Fatal("an invalid acknowledgement was sent")
	}
}
