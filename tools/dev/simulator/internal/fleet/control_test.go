package fleet

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// fakeControl is a control plane small enough to read: enrollment, the session stream and
// the HTTPS agent paths of the contract, with knobs for the failures a test needs. It
// holds claims until a task arrives, as control does.
type fakeControl struct {
	t      *testing.T
	server *httptest.Server
	origin string
	caFile string
	pacing wire.Pacing

	mu     sync.Mutex
	tokens map[string]bool
	nodes  map[string]*fakeNode
	// noStream answers the session path as a proxy without HTTP/2 would.
	noStream bool
	// refuse answers this many heartbeats with RATE_LIMITED and a hint of retryAfter.
	refuse     int
	retryAfter int
	// revoked answers every call of these nodes as an unknown credential.
	revoked map[string]bool
	streams int
	// down answers every task call with 503, as control does while it restarts.
	down bool
	// renewal, when set, refuses lease renewals.
	renewal *wire.SessionProblem
}

type fakeNode struct {
	id, credential string
	sequence       uint64
	acknowledged   uint64
	queue          []wire.Lease
	wake           chan struct{}
	heartbeats     []heartbeatSeen
	acks           map[string]int
	results        map[string]int
	progress       map[string]int
}

type heartbeatSeen struct {
	at        time.Time
	transport string
}

func newFakeControl(t *testing.T) *fakeControl {
	t.Helper()
	f := &fakeControl{t: t, tokens: map[string]bool{}, nodes: map[string]*fakeNode{}, revoked: map[string]bool{},
		pacing: wire.Pacing{HeartbeatSeconds: wire.MinHeartbeatSeconds, JitterPercent: 0, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 2}}
	session := grpc.NewServer(grpc.UnknownServiceHandler(f.session))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/v1/enroll", f.enroll)
	base := "/agent/v1/sites/{site}/cells/{cell}/nodes/{node}"
	mux.HandleFunc("POST "+base+"/heartbeat", f.node(func(n *fakeNode, r *http.Request) (any, *wire.SessionProblem) {
		var h wire.Handshake
		decodeBody(f.t, r, &h)
		return f.heartbeat(n, "https")
	}))
	mux.HandleFunc("POST "+base+"/tasks/claims", f.node(func(n *fakeNode, r *http.Request) (any, *wire.SessionProblem) {
		var request wire.ClaimRequest
		decodeBody(f.t, r, &request)
		return f.claim(r.Context(), n, request), nil
	}))
	mux.HandleFunc("POST "+base+"/tasks/{task}/acknowledgements", f.node(func(n *fakeNode, r *http.Request) (any, *wire.SessionProblem) {
		var request wire.TaskAcknowledgementRequest
		decodeBody(f.t, r, &request)
		return f.acknowledge(n, wire.TaskAcknowledgement{TaskID: r.PathValue("task"), Sequence: request.Sequence, LeaseToken: request.LeaseToken})
	}))
	mux.HandleFunc("POST "+base+"/tasks/{task}/"+wire.TaskProgressPath, f.node(func(n *fakeNode, r *http.Request) (any, *wire.SessionProblem) {
		var report wire.TaskProgress
		decodeBody(f.t, r, &report)
		if report.Validate() != nil {
			return nil, &wire.SessionProblem{Code: "TASK_PROGRESS_INVALID", Status: 400}
		}
		f.mu.Lock()
		n.progress[r.PathValue("task")]++
		f.mu.Unlock()
		return wire.TaskProgressAccepted{Accepted: len(report.Entries)}, nil
	}))
	mux.HandleFunc("POST "+base+"/tasks/{task}/renewals", f.node(func(n *fakeNode, r *http.Request) (any, *wire.SessionProblem) {
		var renewal wire.LeaseRenewal
		decodeBody(f.t, r, &renewal)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.renewal != nil {
			return nil, f.renewal
		}
		return wire.RenewedLease{LeaseExpiresAt: time.Now().Add(30 * time.Second).UTC(), ValidForMilliseconds: 30000}, nil
	}))
	mux.HandleFunc("POST "+base+"/tasks/{task}/results", f.node(func(n *fakeNode, r *http.Request) (any, *wire.SessionProblem) {
		var result wire.Result
		decodeBody(f.t, r, &result)
		if result.Outcome != "success" || result.Facts == nil || result.Facts.Validate() != nil {
			return nil, &wire.SessionProblem{Code: "VALIDATION_FAILED", Status: 400}
		}
		f.mu.Lock()
		n.results[r.PathValue("task")]++
		f.mu.Unlock()
		return map[string]string{"state": "succeeded"}, nil
	}))
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			f.mu.Lock()
			blocked := f.noStream
			f.mu.Unlock()
			if blocked {
				http.Error(w, "not here", http.StatusNotFound)
				return
			}
			session.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	f.server.EnableHTTP2 = true
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	// The test certificate names example.com; connections dial the listener directly.
	f.origin = "https://example.com:" + strconv.Itoa(f.server.Listener.Addr().(*net.TCPAddr).Port)
	return f
}

// files writes the CA and a join token per agent, and returns a configuration for them.
func (f *fakeControl) files(agents int) config.Config {
	f.t.Helper()
	directory := f.t.TempDir()
	f.caFile = filepath.Join(directory, "ca.pem")
	if e := os.WriteFile(f.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0o600); e != nil {
		f.t.Fatal(e)
	}
	var tokens []string
	f.mu.Lock()
	for range agents {
		token := randomToken()
		f.tokens[token] = true
		tokens = append(tokens, token)
	}
	f.mu.Unlock()
	tokensFile := filepath.Join(directory, "tokens")
	if e := os.WriteFile(tokensFile, []byte(strings.Join(tokens, "\n")+"\n"), 0o600); e != nil {
		f.t.Fatal(e)
	}
	start := 0
	return config.Config{ControlOrigin: f.origin, CAFile: f.caFile, DialAddress: f.server.Listener.Addr().String(),
		IdentitiesFile: filepath.Join(directory, "identities.json"), JoinTokensFile: tokensFile, Agents: agents,
		AgentsPerConnection: 1, HTTPSConnections: 8, EnrollConcurrency: 4, RequestSeconds: 5, StartSeconds: &start,
		LogSeconds: 1, WindowSeconds: 60, LongTaskSeconds: 5, ReportFile: filepath.Join(directory, "report.json")}
}

func randomToken() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func randomID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func decodeBody(t *testing.T, r *http.Request, out any) {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(out); e != nil {
		t.Errorf("%s: %v", r.URL.Path, e)
	}
}

func answer(w http.ResponseWriter, status int, value any, problem *wire.SessionProblem) {
	w.Header().Set("Content-Type", "application/json")
	if problem != nil {
		if problem.RetryAfterSeconds > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(problem.RetryAfterSeconds))
		}
		w.WriteHeader(problem.Status)
		json.NewEncoder(w).Encode(map[string]string{"code": problem.Code})
		return
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": value})
}

func (f *fakeControl) enroll(w http.ResponseWriter, r *http.Request) {
	var in wire.Enrollment
	decodeBody(f.t, r, &in)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tokens[in.JoinToken] || in.Handshake.Validate() != nil {
		answer(w, 0, nil, &wire.SessionProblem{Code: "JOIN_TOKEN_INVALID", Status: 401})
		return
	}
	delete(f.tokens, in.JoinToken)
	n := &fakeNode{id: randomID(), credential: randomToken(), wake: make(chan struct{}), acks: map[string]int{}, results: map[string]int{}, progress: map[string]int{}}
	f.nodes[n.id] = n
	answer(w, http.StatusCreated, wire.Enrolled{NodeID: n.id, SiteID: "default", CellID: "default", Credential: n.credential, ProtocolVersion: wire.ProfileVersion(wire.ReadOnlyProfile), HeartbeatSeconds: wire.HeartbeatSeconds}, nil)
}

// authorized finds the node a credential belongs to.
func (f *fakeControl) authorized(id, header string) *fakeNode {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.nodes[id]
	if n == nil || f.revoked[id] || header != "Bearer "+n.credential {
		return nil
	}
	return n
}

func (f *fakeControl) node(handle func(*fakeNode, *http.Request) (any, *wire.SessionProblem)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := f.authorized(r.PathValue("node"), r.Header.Get("Authorization"))
		if n == nil || r.PathValue("site") != "default" || r.PathValue("cell") != "default" {
			answer(w, 0, nil, &wire.SessionProblem{Code: "AGENT_UNAUTHORIZED", Status: 401})
			return
		}
		f.mu.Lock()
		down := f.down
		f.mu.Unlock()
		if down {
			answer(w, 0, nil, &wire.SessionProblem{Code: "SERVICE_UNAVAILABLE", Status: 503})
			return
		}
		value, problem := handle(n, r)
		answer(w, http.StatusOK, value, problem)
	}
}

func (f *fakeControl) session(_ any, stream grpc.ServerStream) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	first := func(key string) string {
		if values := md.Get(key); len(values) == 1 {
			return values[0]
		}
		return ""
	}
	n := f.authorized(first(wire.SessionNodeHeader), first("authorization"))
	if n == nil {
		return status.Error(codes.Unauthenticated, "AGENT_UNAUTHORIZED")
	}
	f.mu.Lock()
	f.streams++
	f.mu.Unlock()
	var last uint64
	for {
		var m wire.AgentMessage
		if stream.RecvMsg(&m) != nil {
			return nil
		}
		if m.Validate(last) != nil {
			return status.Error(codes.InvalidArgument, "AGENT_SESSION_MESSAGE_INVALID")
		}
		last = m.ID
		reply := wire.ControlMessage{ReplyTo: m.ID}
		switch {
		case m.Heartbeat != nil:
			disposition, problem := f.heartbeat(n, "stream")
			reply.Problem = problem
			if problem == nil {
				reply.Disposition = &disposition
			}
		case m.Claim != nil:
			claim := f.claim(stream.Context(), n, *m.Claim)
			reply.Claim = &claim
		case m.Ack != nil:
			renewed, problem := f.acknowledge(n, *m.Ack)
			reply.Problem = problem
			if problem == nil {
				reply.Acknowledged = &renewed
			}
		}
		if stream.SendMsg(&reply) != nil {
			return nil
		}
	}
}

func (f *fakeControl) heartbeat(n *fakeNode, transport string) (wire.Disposition, *wire.SessionProblem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n.heartbeats = append(n.heartbeats, heartbeatSeen{at: time.Now(), transport: transport})
	if f.refuse > 0 {
		f.refuse--
		return wire.Disposition{}, &wire.SessionProblem{Code: "RATE_LIMITED", Status: 429, RetryAfterSeconds: f.retryAfter}
	}
	pacing := f.pacing
	return wire.Disposition{Status: "active", ServerTime: time.Now().UTC(), Pacing: &pacing}, nil
}

// claim answers with a resync when the agent's cursor is behind control's record, with the
// first unacknowledged lease, or, after holding the claim for its wait, with neither.
func (f *fakeControl) claim(ctx context.Context, n *fakeNode, request wire.ClaimRequest) wire.Claim {
	deadline := time.NewTimer(time.Duration(request.WaitSeconds) * time.Second)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		if request.AcknowledgedSequence < n.acknowledged {
			f.mu.Unlock()
			return wire.Claim{Resync: &wire.Resync{AcknowledgedSequence: n.acknowledged}}
		}
		if len(n.queue) > 0 {
			lease := n.queue[0]
			f.mu.Unlock()
			return wire.Claim{Lease: &lease}
		}
		wake := n.wake
		f.mu.Unlock()
		select {
		case <-wake:
		case <-deadline.C:
			return wire.Claim{}
		case <-ctx.Done():
			return wire.Claim{}
		}
	}
}

func (f *fakeControl) acknowledge(n *fakeNode, ack wire.TaskAcknowledgement) (wire.RenewedLease, *wire.SessionProblem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(n.queue) > 0 && n.queue[0].TaskID == ack.TaskID {
		n.acknowledged = ack.Sequence
		n.queue = n.queue[1:]
	} else if n.acks[ack.TaskID] == 0 {
		return wire.RenewedLease{}, &wire.SessionProblem{Code: "AGENT_LEASE_STALE", Status: 409}
	}
	n.acks[ack.TaskID]++
	return wire.RenewedLease{LeaseExpiresAt: time.Now().Add(30 * time.Second).UTC(), ValidForMilliseconds: 30000}, nil
}

// enqueue adds a collect_facts task for every node and wakes their claims.
func (f *fakeControl) enqueue() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.nodes {
		n.sequence++
		n.queue = append(n.queue, wire.Lease{TaskID: randomID(), Sequence: n.sequence, Operation: wire.CollectFacts, LeaseToken: randomToken(), LeaseExpiresAt: time.Now().Add(30 * time.Second).UTC(), Attempt: 1})
		close(n.wake)
		n.wake = make(chan struct{})
	}
}

// each runs check on every node under the lock.
func (f *fakeControl) each(check func(*fakeNode)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.nodes {
		check(n)
	}
}

func (f *fakeControl) set(change func(*fakeControl)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}
