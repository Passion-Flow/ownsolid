package agent_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// sessionControl is the real control handler and database behind a TLS listener that
// offers HTTP/2, as the installed control plane does.
type sessionControl struct {
	handler *transport.Handler
	origin  string
	https   *http.Client
	roots   *x509.CertPool
	address string
}

func startSessionControl(t *testing.T) (*sessionControl, string) {
	t.Helper()
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	cert, ca, _, _ := smtpsink.Certificate(t, t.TempDir(), "localhost")
	handler := transport.New(s)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}
	server.EnableHTTP2 = true
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	s.Config.Server.AdminOrigin = "https://localhost:" + port
	server.StartTLS()
	t.Cleanup(server.Close)
	pem, e := os.ReadFile(ca)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	address := server.Listener.Addr().String()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	// An HTTP/1.1 client, like the agent's HTTPS transport.
	https := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}}}
	return &sessionControl{handler: handler, origin: s.Config.Server.AdminOrigin, https: https, roots: roots, address: address}, password
}

func sessionFacts() wire.Facts {
	return wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "session-node", LogicalCPUs: 4, MemoryTotalBytes: 1 << 30, DiskPath: "/", DiskTotalBytes: 1 << 34, DiskAvailableBytes: 1 << 33, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
}

func sessionHandshake() wire.Handshake {
	return wire.Handshake{ProtocolVersion: wire.ProfileVersion(wire.ReadOnlyProfile), Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: sessionFacts()}
}

func (c *sessionControl) post(t *testing.T, path, credential string, body, out any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, c.origin+path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response, e := c.https.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	b, _ := io.ReadAll(response.Body)
	if response.StatusCode < 300 && out != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(b, &envelope) != nil || json.Unmarshal(envelope.Data, out) != nil {
			t.Fatalf("response %s", b)
		}
	}
	return response.StatusCode, string(b)
}

func (c *sessionControl) stream(t *testing.T, node, credential string) grpc.ClientStream {
	t.Helper()
	conn, e := grpc.NewClient("passthrough:///"+c.address, grpc.WithAuthority(strings.TrimPrefix(c.origin, "https://")), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: c.roots, MinVersion: tls.VersionTLS12})))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	md := metadata.Pairs("authorization", "Bearer "+credential, wire.SessionNodeHeader, node, wire.SessionSiteHeader, "default", wire.SessionCellHeader, "default")
	ctx, cancel := context.WithCancel(metadata.NewOutgoingContext(context.Background(), md))
	t.Cleanup(cancel)
	stream, e := conn.NewStream(ctx, &grpc.StreamDesc{StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true}, wire.SessionPath, grpc.CallContentSubtype(wire.SessionCodec{}.Name()))
	if e != nil {
		t.Fatal(e)
	}
	return stream
}

func exchange(t *testing.T, stream grpc.ClientStream, m wire.AgentMessage) wire.ControlMessage {
	t.Helper()
	if e := stream.SendMsg(&m); e != nil {
		t.Fatal(e)
	}
	var reply wire.ControlMessage
	if e := stream.RecvMsg(&reply); e != nil {
		t.Fatal(e)
	}
	if e := reply.Validate(m); e != nil {
		t.Fatalf("reply %+v does not answer %d", reply, m.ID)
	}
	return reply
}

// TestAgentSessionAgainstControl runs the session stream and the HTTPS long-poll against
// the real control handler and PostgreSQL: enrollment, heartbeat, a claim woken by a new
// task, the HTTPS result, credential failure and the fallback claim.
func TestAgentSessionAgainstControl(t *testing.T) {
	c, password := startSessionControl(t)
	s := c.handler.Service
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	nodes := c.handler.Nodes.Service
	ctx := t.Context()
	token, e := nodes.Mint(ctx, admin, admin.Me.Workspace.ID, "Session agent", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var enrolled wire.Enrolled
	if status, body := c.post(t, "/agent/v1/enroll", "", wire.Enrollment{JoinToken: token.Token, Handshake: sessionHandshake()}, &enrolled); status != http.StatusCreated {
		t.Fatalf("enroll %d %s", status, body)
	}
	base := "/agent/v1/sites/default/cells/default/nodes/" + enrolled.NodeID

	stream := c.stream(t, enrolled.NodeID, enrolled.Credential)
	heartbeat := sessionHandshake()
	reply := exchange(t, stream, wire.AgentMessage{ID: 1, Heartbeat: &heartbeat})
	if reply.Disposition == nil || reply.Disposition.Status != "active" {
		t.Fatalf("heartbeat %+v", reply)
	}

	// The claim waits; a task written meanwhile is leased at once.
	created := make(chan string, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		task, e := nodes.CreateTask(context.Background(), admin, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
		if e != nil {
			created <- "error " + e.Error()
			return
		}
		created <- task.ID
	}()
	started := time.Now()
	reply = exchange(t, stream, wire.AgentMessage{ID: 2, Claim: &wire.ClaimRequest{WaitSeconds: wire.MaxClaimWaitSeconds}})
	elapsed := time.Since(started)
	taskID := <-created
	if reply.Claim == nil || reply.Claim.Lease == nil || reply.Claim.Lease.TaskID != taskID {
		t.Fatalf("claim %+v, created %s", reply, taskID)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("a woken claim took %s", elapsed)
	}
	t.Logf("lease arrived %s after the claim, %s after the task was written", elapsed.Round(time.Millisecond), (elapsed - 500*time.Millisecond).Round(time.Millisecond))

	facts := sessionFacts()
	var completed struct {
		State string `json:"state"`
	}
	if status, body := c.post(t, base+"/tasks/"+taskID+"/results", enrolled.Credential, wire.Result{LeaseToken: reply.Claim.Lease.LeaseToken, Outcome: "success", Facts: &facts}, &completed); status != http.StatusOK || completed.State != "succeeded" {
		t.Fatalf("result %d %s", status, body)
	}

	// With no work the claim ends empty when its wait ends.
	started = time.Now()
	reply = exchange(t, stream, wire.AgentMessage{ID: 3, Claim: &wire.ClaimRequest{WaitSeconds: 1}})
	if reply.Claim == nil || reply.Claim.Lease != nil || time.Since(started) < time.Second {
		t.Fatalf("empty claim %+v after %s", reply, time.Since(started))
	}

	// A wrong credential fails in band with the HTTPS status, and the stream stays open.
	wrong := c.stream(t, enrolled.NodeID, strings.Repeat("A", 43))
	reply = exchange(t, wrong, wire.AgentMessage{ID: 1, Heartbeat: &heartbeat})
	if reply.Problem == nil || reply.Problem.Status != http.StatusUnauthorized {
		t.Fatalf("wrong credential %+v", reply)
	}
	reply = exchange(t, stream, wire.AgentMessage{ID: 4, Heartbeat: &heartbeat})
	if reply.Disposition == nil || reply.Disposition.Status != "active" {
		t.Fatalf("heartbeat after another session's failure %+v", reply)
	}

	// The HTTPS fallback long-polls the claims path the same way.
	go func() {
		time.Sleep(500 * time.Millisecond)
		task, e := nodes.CreateTask(context.Background(), admin, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
		if e != nil {
			created <- "error " + e.Error()
			return
		}
		created <- task.ID
	}()
	var claim wire.Claim
	started = time.Now()
	if status, body := c.post(t, base+"/tasks/claims", enrolled.Credential, wire.ClaimRequest{WaitSeconds: wire.MaxClaimWaitSeconds}, &claim); status != http.StatusOK {
		t.Fatalf("long-poll claim %d %s", status, body)
	}
	taskID = <-created
	if claim.Lease == nil || claim.Lease.TaskID != taskID || time.Since(started) > 3*time.Second {
		t.Fatalf("long-poll claim %+v after %s, created %s", claim, time.Since(started), taskID)
	}
	if status, body := c.post(t, base+"/tasks/claims", enrolled.Credential, map[string]int{"wait_seconds": wire.MaxClaimWaitSeconds + 1}, nil); status != http.StatusBadRequest {
		t.Fatalf("over-long wait %d %s", status, body)
	}
	if status, body := c.post(t, base+"/tasks/claims", enrolled.Credential, struct{}{}, &claim); status != http.StatusOK {
		t.Fatalf("an empty claim body still claims at once: %d %s", status, body)
	}
}
