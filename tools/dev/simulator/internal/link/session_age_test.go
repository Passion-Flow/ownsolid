package link

import (
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type sessionAgeFixture struct {
	dialer   *Dialer
	sessions atomic.Int64
	refuse   atomic.Bool
	cursor   atomic.Uint64
}

// The stream stand-in tests client ownership; actual node-certificate authority
// and natural server aging are exercised by the installed gateway fixture.
func newSessionAgeFixture(t *testing.T, exclusive bool) *sessionAgeFixture {
	t.Helper()
	f := new(sessionAgeFixture)
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: wire.SessionService, HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{StreamName: wire.SessionMethod, ServerStreams: true, ClientStreams: true,
			Handler: func(_ any, stream grpc.ServerStream) error {
				f.sessions.Add(1)
				if f.refuse.Load() {
					return status.Error(codes.ResourceExhausted, "AUTH_CAPACITY_EXCEEDED")
				}
				if err := stream.SetHeader(metadata.Pairs(wire.SessionDrainHeader, "60000000000", wire.SessionCloseHeader, "75000000000")); err != nil {
					return err
				}
				var last uint64
				for {
					var message wire.AgentMessage
					if err := stream.RecvMsg(&message); err != nil {
						return nil
					}
					if message.Validate(last) != nil || last == 0 && message.Heartbeat == nil {
						return status.Error(codes.InvalidArgument, "AGENT_SESSION_MESSAGE_INVALID")
					}
					last = message.ID
					reply := wire.ControlMessage{ReplyTo: message.ID}
					if message.Heartbeat != nil {
						reply.Disposition = &wire.Disposition{Status: "active", ServerTime: time.Now().UTC()}
					} else if message.Claim != nil {
						f.cursor.Store(message.Claim.AcknowledgedSequence)
						reply.Claim = &wire.Claim{}
					} else {
						reply.Acknowledged = &wire.RenewedLease{LeaseExpiresAt: time.Now().Add(time.Minute), ValidForMilliseconds: 60000}
					}
					if err := stream.SendMsg(&reply); err != nil {
						return nil
					}
				}
			}}},
	}, nil)
	endpoint := httptest.NewUnstartedServer(http.HandlerFunc(server.ServeHTTP))
	endpoint.EnableHTTP2 = true
	endpoint.StartTLS()
	t.Cleanup(endpoint.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: endpoint.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	dialer, err := NewDialer(endpoint.URL, ca, "", 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dialer.Close)
	f.dialer = dialer
	if exclusive {
		f.dialer = dialer.ForAgent(endpoint.TLS.Certificates[0])
		t.Cleanup(f.dialer.Close)
	}
	return f
}

func initialAgeStream(t *testing.T, conn *Connection, node Node) *Stream {
	t.Helper()
	stream, err := conn.Open(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	if _, err := stream.Exchange(t.Context(), wire.AgentMessage{Heartbeat: &wire.Handshake{ProtocolVersion: wire.Version}}); err != nil {
		t.Fatal(err)
	}
	schedule, ok := wire.ParseSessionConnectionAge([]string{"60000000000"}, []string{"75000000000"}, time.Now().Add(-40*time.Second))
	if !ok || stream.age.Lifetime == nil {
		t.Fatal("fixture did not bind a verified stream lifetime")
	}
	stream.age.SessionConnectionAge = schedule
	return stream
}

func ageNode() Node {
	return Node{ID: "3b0c8f1e-2d4a-4c6b-8e9f-1a2b3c4d5e6f", Scope: wire.LocalScope(), Credential: strings.Repeat("C", 43)}
}

func TestHandoffKeepsCursorAndJoinsRetiringChannel(t *testing.T) {
	f := newSessionAgeFixture(t, true)
	conn := f.dialer.Connection()
	t.Cleanup(conn.Reset)
	node := ageNode()
	old := initialAgeStream(t, conn, node)
	stream, disposition, err := conn.Handoff(t.Context(), old, node, wire.Handshake{}, wire.DefaultPacing(), time.Now().Add(time.Minute))
	if err != nil || stream == nil || disposition == nil || disposition.Status != "active" {
		t.Fatalf("authenticated handoff: disposition=%+v error=%v", disposition, err)
	}
	t.Cleanup(stream.Close)
	if conn.conn == old.connection || conn.retiring == nil {
		t.Fatal("replacement did not separate the active and retiring channels")
	}
	old.Close()
	if _, err := stream.Exchange(t.Context(), wire.AgentMessage{Claim: &wire.ClaimRequest{AcknowledgedSequence: 7}}); err != nil || f.cursor.Load() != 7 {
		t.Fatalf("handoff lost the cursor: %d %v", f.cursor.Load(), err)
	}
	retirement := conn.retiring
	conn.Reset()
	select {
	case <-retirement.Done():
	default:
		t.Fatal("reset did not join the retiring channel")
	}
}

func TestHandoffCapacityRefusalKeepsOriginalAndDoesNotRetryHint(t *testing.T) {
	f := newSessionAgeFixture(t, true)
	conn := f.dialer.Connection()
	t.Cleanup(conn.Reset)
	node := ageNode()
	old := initialAgeStream(t, conn, node)
	f.refuse.Store(true)
	stream, _, err := conn.Handoff(t.Context(), old, node, wire.Handshake{}, wire.DefaultPacing(), time.Now().Add(time.Minute))
	if !errors.Is(err, ErrBusy) || stream != nil || conn.conn != old.connection || conn.retiring != nil {
		t.Fatalf("capacity refusal displaced the original: %v", err)
	}
	if _, err := old.Exchange(t.Context(), wire.AgentMessage{Heartbeat: &wire.Handshake{}}); err != nil {
		t.Fatal(err)
	}
	if next, _, err := conn.Handoff(t.Context(), old, node, wire.Handshake{}, wire.DefaultPacing(), time.Now().Add(time.Minute)); err != nil || next != nil || f.sessions.Load() != 2 {
		t.Fatal("one hint triggered repeated candidates")
	}
}

func TestHandoffPreservesLegacyMultiplexedStreams(t *testing.T) {
	f := newSessionAgeFixture(t, false)
	conn := f.dialer.Connection()
	t.Cleanup(conn.Reset)
	firstNode, secondNode := ageNode(), ageNode()
	secondNode.ID = "9334a6cf-5382-4150-99f7-ac4d142a1731"
	first, second := initialAgeStream(t, conn, firstNode), initialAgeStream(t, conn, secondNode)
	if next, _, err := conn.Handoff(t.Context(), first, firstNode, wire.Handshake{}, wire.DefaultPacing(), time.Now().Add(time.Minute)); err != nil || next != nil {
		t.Fatal("one node replaced the legacy shared connection")
	}
	for _, stream := range []*Stream{first, second} {
		if _, err := stream.Exchange(t.Context(), wire.AgentMessage{Heartbeat: &wire.Handshake{}}); err != nil {
			t.Fatal(err)
		}
	}
	if f.sessions.Load() != 2 || first.connection != second.connection {
		t.Fatal("legacy streams no longer share their channel")
	}
}
