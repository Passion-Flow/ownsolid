package client

import (
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc/codes"
)

func TestPreviouslyUsableStreamRetriesWithinReconnectBound(t *testing.T) {
	p := wire.DefaultPacing()
	p.ReconnectMaxSeconds = 7
	c := &control{pacing: &p}
	s := serve(t, c, true)
	current := time.Now()
	s.now = func() time.Time { return current }
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil || s.Transport() != TransportStream {
		t.Fatalf("initial stream: %v", e)
	}
	c.mu.Lock()
	c.refuse = codes.Unavailable
	c.mu.Unlock()
	s.dropStream()
	for range wire.SessionFallbackProbes {
		if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e == nil {
			t.Fatal("transient stream refusal succeeded")
		}
		if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil || s.Transport() != TransportHTTPS {
			t.Fatalf("HTTPS probe: %v", e)
		}
	}
	if delay := s.fallbackUntil.Sub(current); delay <= 0 || delay > time.Duration(p.ReconnectMaxSeconds)*time.Second {
		t.Fatalf("previously usable stream retained fallback %s beyond advertised %d seconds", delay, p.ReconnectMaxSeconds)
	}
	c.mu.Lock()
	c.refuse = codes.OK
	c.mu.Unlock()
	current = s.fallbackUntil.Add(-time.Nanosecond)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil || s.Transport() != TransportHTTPS {
		t.Fatalf("before due: %v", e)
	}
	current = current.Add(time.Nanosecond)
	if _, e := s.Heartbeat(t.Context(), wire.Handshake{}); e != nil || s.Transport() != TransportStream {
		t.Fatalf("stream recovery: %v", e)
	}
	if _, e := s.Claim(t.Context(), 0, 7); e != nil {
		t.Fatal(e)
	}
	c.mu.Lock()
	cursors, acknowledgements := append([]uint64(nil), c.cursors...), len(c.acks)
	c.refuse = codes.Unavailable
	c.mu.Unlock()
	if len(cursors) != 1 || cursors[0] != 7 || acknowledgements != 0 {
		t.Fatalf("fallback replayed a claim/ACK or changed cursor: %v/%d", cursors, acknowledgements)
	}
	s.Close()
	fresh := s.client.Session(sessionNode)
	t.Cleanup(fresh.Close)
	fresh.now = s.now
	for range wire.SessionFallbackProbes {
		if _, err := fresh.Heartbeat(t.Context(), wire.Handshake{}); err == nil {
			t.Fatal("new session opened a refused stream")
		}
		if _, err := fresh.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
			t.Fatal(err)
		}
	}
	if delay := fresh.fallbackUntil.Sub(current); delay != time.Duration(wire.SessionFallbackRetrySeconds)*time.Second {
		t.Fatalf("new session inherited prior stream knowledge: %s", delay)
	}
}

func TestPreviouslyUsableStreamRequiresSuccessfulHTTPSProbes(t *testing.T) {
	p := wire.DefaultPacing()
	p.ReconnectMaxSeconds = 7
	c := &control{pacing: &p}
	s := serve(t, c, true)
	current := time.Now()
	s.now = func() time.Time { return current }
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.refuse, c.httpDown = codes.Unavailable, true
	c.mu.Unlock()
	s.dropStream()
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err == nil {
		t.Fatal("refused stream succeeded")
	}
	if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err == nil || !s.fallbackUntil.IsZero() {
		t.Fatal("failed HTTPS armed fallback")
	}
	c.mu.Lock()
	c.httpDown = false
	c.mu.Unlock()
	for attempt := range wire.SessionFallbackProbes {
		if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err == nil {
			t.Fatal("refused stream succeeded")
		}
		if _, err := s.Heartbeat(t.Context(), wire.Handshake{}); err != nil {
			t.Fatal(err)
		}
		if attempt+1 < wire.SessionFallbackProbes && !s.fallbackUntil.IsZero() {
			t.Fatal("failed HTTPS was counted as a successful probe")
		}
	}
	if s.fallbackUntil.Sub(current) != time.Duration(p.ReconnectMaxSeconds)*time.Second {
		t.Fatal("usable stream did not retain bounded fallback")
	}
}
