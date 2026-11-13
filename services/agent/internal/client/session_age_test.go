package client

import (
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func ageSession(t *testing.T, headers metadata.MD) (*control, *Session) {
	t.Helper()
	pacing := wire.DefaultPacing()
	pacing.HeartbeatSeconds = 30
	c := &control{replyHeaders: headers, pacing: &pacing}
	s := serve(t, c, true)
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	return c, s
}

func ageHeaders() metadata.MD {
	return metadata.Pairs(wire.SessionDrainHeader, "60000000000", wire.SessionCloseHeader, "75000000000")
}

func advanceToHandoff(s *Session) {
	advanced := s.stream.age.DrainAt.Add(-20 * time.Second)
	s.now = func() time.Time { return advanced }
}

func TestConnectionHandoffPreservesCursorAndDoesNotReplayAcknowledgement(t *testing.T) {
	c, s := ageSession(t, ageHeaders())
	lease := wire.Lease{TaskID: sessionNode, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("L", 43), Attempt: 1, Sequence: 7}
	c.mu.Lock()
	c.lease = &lease
	c.mu.Unlock()
	if _, err := s.Claim(t.Context(), 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acknowledge(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	oldConnection, oldStream := s.conn, s.stream
	advanceToHandoff(s)
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	if s.conn == oldConnection || s.stream == oldStream || s.retiring == nil {
		t.Fatal("authenticated replacement did not own the next exchanges")
	}
	if _, err := s.Claim(t.Context(), 0, lease.Sequence); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams != 2 || len(c.cursors) != 2 || c.cursors[0] != 0 || c.cursors[1] != lease.Sequence || len(c.acks) != 1 {
		t.Fatalf("handoff changed delivery: streams=%d cursors=%v acknowledgements=%v", c.streams, c.cursors, c.acks)
	}
}

func TestConnectionHandoffKeepsHealthyStreamAfterCapacityRefusal(t *testing.T) {
	c, s := ageSession(t, ageHeaders())
	oldConnection, oldStream := s.conn, s.stream
	c.mu.Lock()
	c.refuse = codes.ResourceExhausted
	c.mu.Unlock()
	advanceToHandoff(s)
	for range 2 {
		if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
			t.Fatal(err)
		}
	}
	if s.conn != oldConnection || s.stream != oldStream || s.retiring != nil {
		t.Fatal("refused candidate displaced the original session")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams != 2 {
		t.Fatalf("one hint initiated %d candidates", c.streams-1)
	}
}

func TestConnectionHandoffKeepsOneRetiringChannel(t *testing.T) {
	c, s := ageSession(t, ageHeaders())
	advanceToHandoff(s)
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	retirement, connection := s.retiring, s.conn
	if retirement == nil {
		t.Fatal("old channel was not retained for natural retirement")
	}
	advanceToHandoff(s)
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	if s.conn != connection || s.retiring != retirement {
		t.Fatal("another candidate grew the retirement set")
	}
	s.Close()
	select {
	case <-retirement.Done():
	default:
		t.Fatal("session close did not join its retirement worker")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams != 2 {
		t.Fatalf("retirement opened %d streams", c.streams)
	}
}

func TestConnectionHandoffRetainsLegacyWithoutValidHint(t *testing.T) {
	for name, headers := range map[string]metadata.MD{
		"absent":    nil,
		"duplicate": metadata.Pairs(wire.SessionDrainHeader, "1", wire.SessionDrainHeader, "1", wire.SessionCloseHeader, "2"),
		"invalid":   metadata.Pairs(wire.SessionDrainHeader, "invalid", wire.SessionCloseHeader, "2"),
	} {
		t.Run(name, func(t *testing.T) {
			c, s := ageSession(t, headers)
			connection, stream := s.conn, s.stream
			if s.stream.age.Lifetime != nil {
				t.Fatal("legacy stream acquired an unvalidated schedule")
			}
			if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
				t.Fatal(err)
			}
			if s.conn != connection || s.stream != stream || s.retiring != nil {
				t.Fatal("invalid or missing metadata changed the legacy path")
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.streams != 1 {
				t.Fatalf("legacy stream opened %d candidates", c.streams-1)
			}
		})
	}
}

func TestConnectionHandoffDoesNotStarveRecoveredPreferredGateway(t *testing.T) {
	c := gatewayCell(t, 2)
	pacing := wire.DefaultPacing()
	pacing.HeartbeatSeconds = 30
	for _, gateway := range c.gateways {
		gateway.mu.Lock()
		gateway.replyHeaders, gateway.pacing = ageHeaders(), &pacing
		gateway.mu.Unlock()
	}
	preferred := c.gateways[c.order[0]]
	preferred.mu.Lock()
	preferred.refuse = codes.Unavailable
	preferred.mu.Unlock()
	if _, err := c.session.Heartbeat(t.Context(), wire.Handshake{}); err == nil {
		t.Fatal("unavailable preferred gateway answered")
	}
	if _, err := c.session.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	if c.session.Gateway() != c.order[1] {
		t.Fatal("fixture did not enter the fallback")
	}
	preferred.mu.Lock()
	preferred.refuse = codes.OK
	preferred.mu.Unlock()
	c.session.client.GatewayRebalanceSeconds = 5
	advanceToHandoff(c.session)
	c.session.rebalance.Candidate(c.session.now().Add(-time.Hour), 5*time.Second, c.session.gateway)
	if _, err := c.session.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	if c.session.Gateway() != c.order[0] {
		t.Fatal("simultaneously due age handoff starved the recovered preferred gateway")
	}
}
