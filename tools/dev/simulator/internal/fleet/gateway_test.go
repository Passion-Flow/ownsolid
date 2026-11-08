package fleet

import (
	"net/http/httptest"
	"testing"
	"time"
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
