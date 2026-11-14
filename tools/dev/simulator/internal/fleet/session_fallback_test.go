package fleet

import (
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/report"
)

func TestPreviouslyUsableStreamRetriesWithinReconnectBound(t *testing.T) {
	f := newFakeControl(t)
	c := enrolled(t, f, 1, 10)
	identities, e := loadIdentities(c.IdentitiesFile, c.ControlOrigin)
	if e != nil {
		t.Fatal(e)
	}
	d, e := link.NewDialer(c.ControlOrigin, c.CAFile, c.DialAddress, c.HTTPSConnections, time.Duration(c.RequestSeconds)*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	conn := d.Connection()
	defer conn.Reset()
	recorder := report.New(1, time.Now())
	a := newAgent(identities[0], d, conn, recorder, time.Second)
	defer a.dropStream()
	a.cursor = 7
	if e := a.cycle(t.Context()); e != nil || a.stream == nil {
		t.Fatalf("initial stream: %v", e)
	}
	f.set(func(f *fakeControl) { f.noStream = true })
	a.dropStream()
	conn.Reset()
	for range wire.SessionFallbackProbes {
		if _, e := a.heartbeat(t.Context(), handshake(a.host)); e == nil {
			t.Fatal("blocked stream succeeded")
		}
		if _, e := a.heartbeat(t.Context(), handshake(a.host)); e != nil || a.stream != nil {
			t.Fatalf("HTTPS probe: %v", e)
		}
	}
	if remaining := time.Until(a.fallbackUntil); remaining <= 0 || remaining > time.Duration(a.pacing.ReconnectMaxSeconds)*time.Second {
		t.Fatalf("previously usable stream retained fallback %s beyond advertised %d seconds", remaining, a.pacing.ReconnectMaxSeconds)
	}
	f.set(func(f *fakeControl) { f.noStream = false })
	timer := time.NewTimer(time.Until(a.fallbackUntil))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if _, e := a.heartbeat(t.Context(), handshake(a.host)); e != nil || a.stream == nil {
		t.Fatalf("stream recovery: %v", e)
	}
	if a.cursor != 7 {
		t.Fatal("recovery changed acknowledged cursor")
	}
	if _, e := a.claim(t.Context(), 0, a.cursor); e != nil {
		t.Fatal(e)
	}
	out := recorder.Finish(time.Now(), 1, 1, 0, nil)
	if out.Tasks.Acknowledged != 0 || out.Tasks.Leases != 0 || out.Calls[report.CallClaim].Succeeded != 2 {
		t.Fatalf("recovery replayed application work: %+v", out)
	}
	a.mu.Lock()
	a.restart = true
	a.mu.Unlock()
	a.applyFaults()
	if a.cursor != 7 {
		t.Fatal("new process changed its retained cursor")
	}
	f.set(func(f *fakeControl) { f.noStream = true })
	var lastProbeStarted time.Time
	for range wire.SessionFallbackProbes {
		if _, err := a.heartbeat(t.Context(), handshake(a.host)); err == nil {
			t.Fatal("new process opened an unsupported stream")
		}
		lastProbeStarted = time.Now()
		if _, err := a.heartbeat(t.Context(), handshake(a.host)); err != nil {
			t.Fatal(err)
		}
	}
	legacy := time.Duration(wire.SessionFallbackRetrySeconds) * time.Second
	if a.fallbackUntil.Before(lastProbeStarted.Add(legacy)) || time.Until(a.fallbackUntil) > legacy {
		t.Fatal("new process inherited prior stream knowledge")
	}

}
