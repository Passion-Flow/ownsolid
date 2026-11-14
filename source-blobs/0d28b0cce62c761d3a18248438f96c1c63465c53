package fleet

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/report"
)

// gateway serves f's agent paths on a listener of its own, standing in for an agent gateway
// in front of the same control. Test servers share one certificate, which names the origin.
func gateway(t *testing.T, f *fakeControl) *httptest.Server {
	t.Helper()
	g := httptest.NewUnstartedServer(f.server.Config.Handler)
	g.EnableHTTP2 = true
	g.StartTLS()
	t.Cleanup(g.Close)
	return g
}

func TestAgentsSpreadOverTheGateways(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	first, second := gateway(t, f), gateway(t, f)
	c := enrolled(t, f, 40, 4)
	c.SessionAddresses = []string{first.Listener.Addr().String(), second.Listener.Addr().String()}
	out := <-runAsync(t, c)
	if out.OnStream != 40 || out.Gateways[c.SessionAddresses[0]] == 0 || out.Gateways[c.SessionAddresses[1]] == 0 || out.Gateways[c.SessionAddresses[0]]+out.Gateways[c.SessionAddresses[1]] != 40 {
		t.Fatalf("report %+v", out)
	}
}

func TestAgentsLeaveAStoppedGatewayForTheNext(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	stopped, live := gateway(t, f), gateway(t, f)
	c := enrolled(t, f, 20, 6)
	c.SessionAddresses = []string{stopped.Listener.Addr().String(), live.Listener.Addr().String()}
	stopped.Close()
	done := runAsync(t, c)
	waitUntil(t, f, 5*time.Second, "every agent on a stream", everyNode(func(n *fakeNode) bool {
		return len(n.heartbeats) > 0 && n.heartbeats[len(n.heartbeats)-1].transport == "stream"
	}))
	out := <-done
	if out.OnStream != 20 || out.Gateways[c.SessionAddresses[1]] != 20 {
		t.Fatalf("report %+v", out)
	}
	// While a gateway was left to try, no agent asked control over HTTPS.
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.nodes {
		for _, h := range n.heartbeats {
			if h.transport != "stream" {
				t.Fatalf("node %s sent a heartbeat over %s", n.id, h.transport)
			}
		}
	}
}

// TestGatewayRollRebalancesFleet exercises the simulator over TLS/HTTP2. The first
// listener closes its connections and refuses new ones until the test brings it back.
func TestGatewayRollRebalancesFleet(t *testing.T) {
	t.Parallel()
	const size = 40
	f := newFakeControl(t)
	second := gateway(t, f)
	var gate sync.Mutex
	ready := true
	atFirst := map[string]bool{}
	first := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate.Lock()
		accepting := ready
		if accepting {
			atFirst[r.Header.Get(wire.SessionNodeHeader)] = true
		}
		gate.Unlock()
		if !accepting {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.server.Config.Handler.ServeHTTP(w, r)
	}))
	first.EnableHTTP2 = true
	first.StartTLS()
	t.Cleanup(first.Close)
	c := enrolled(t, f, size, 45)
	c.SessionAddresses = []string{first.Listener.Addr().String(), second.Listener.Addr().String()}
	c.GatewayRebalanceSeconds = wire.MinGatewayRebalanceSeconds
	preferred := map[string]bool{}
	f.mu.Lock()
	for id := range f.nodes {
		if wire.SessionAddressOrder(id, c.SessionAddresses)[0] == c.SessionAddresses[0] {
			preferred[id] = true
		}
	}
	f.mu.Unlock()
	if len(preferred) == 0 || len(preferred) == size {
		t.Fatalf("test fleet did not split: %d/%d", len(preferred), size-len(preferred))
	}

	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	var out report.Report
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, runErr = Run(ctx, c)
	}()
	t.Cleanup(func() { stop(); <-done })
	waitUntil(t, f, 8*time.Second, "every agent connected before the roll", everyNode(func(n *fakeNode) bool {
		return len(n.heartbeats) > 0
	}))
	gate.Lock()
	if !maps.Equal(atFirst, preferred) {
		gate.Unlock()
		t.Fatal("initial session membership differs from the rendezvous order")
	}
	ready = false
	clear(atFirst)
	gate.Unlock()
	first.CloseClientConnections()
	takenDown := time.Now()
	waitUntil(t, f, 15*time.Second, "every agent connected while only the second gateway is available", everyNode(func(n *fakeNode) bool {
		last := n.heartbeats[len(n.heartbeats)-1]
		return last.at.After(takenDown) && last.transport == "stream"
	}))
	gate.Lock()
	ready = true
	gate.Unlock()
	returned := time.Now()
	waitUntil(t, f, 25*time.Second, "every displaced agent back on its preferred gateway", func(_ *fakeControl) bool {
		gate.Lock()
		defer gate.Unlock()
		return maps.Equal(atFirst, preferred)
	})
	took := time.Since(returned)
	// Let the run finish so a stream's acceptance cannot race the final report.
	<-done
	moves := out.Calls[report.CallGatewayRebalance]
	if runErr != nil || out.OnStream != size || out.Gateways[c.SessionAddresses[0]] != len(preferred) || moves == nil || moves.Succeeded != int64(len(preferred)) {
		t.Fatalf("rebalance report: on_stream=%d gateways=%v moves=%+v error=%v", out.OnStream, out.Gateways, moves, runErr)
	}
	t.Logf("%d agents: %d/%d before the roll, 0/%d during it, %d/%d after %s", size,
		len(preferred), size-len(preferred), size, len(preferred), size-len(preferred), took.Round(time.Millisecond))
}
