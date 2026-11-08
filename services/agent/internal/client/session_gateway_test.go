package client

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc/codes"
)

// cell is control and its agent gateways, with a session whose gateways are in node order.
type cell struct {
	control  *control
	gateways map[string]*control
	servers  map[string]*httptest.Server
	order    []string
	session  *Session
}

func gatewayCell(t *testing.T, gateways int) *cell {
	t.Helper()
	c := &cell{control: &control{}, gateways: map[string]*control{}, servers: map[string]*httptest.Server{}}
	connection := connect(t, start(t, c.control, true))
	for range gateways {
		g := &control{}
		server := start(t, g, true)
		address := strings.TrimPrefix(server.URL, "https://")
		c.gateways[address], c.servers[address] = g, server
		connection.SessionAddresses = append(connection.SessionAddresses, address)
	}
	c.order = wire.SessionAddressOrder(sessionNode, connection.SessionAddresses)
	c.session = connection.Session(sessionNode)
	t.Cleanup(c.session.Close)
	return c
}

func TestSessionUsesTheFirstGatewayOfItsOrder(t *testing.T) {
	c := gatewayCell(t, 3)
	if _, e := c.session.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.gateways[c.order[0]].log(); !equal(got, []string{"stream heartbeat"}) {
		t.Fatalf("first gateway saw %v", got)
	}
	if len(c.control.log()) != 0 || len(c.gateways[c.order[1]].log()) != 0 || len(c.gateways[c.order[2]].log()) != 0 {
		t.Fatal("the session went somewhere other than its first gateway")
	}
	if c.session.Gateway() != c.order[0] || c.session.Transport() != TransportStream {
		t.Fatalf("gateway %q transport %q", c.session.Gateway(), c.session.Transport())
	}
}

func TestSessionMovesToTheNextGatewayWhenOneIsDown(t *testing.T) {
	c := gatewayCell(t, 2)
	c.servers[c.order[0]].Close()
	ctx := t.Context()
	// One attempt makes one request, so the failed gateway costs this attempt only.
	if _, e := c.session.Heartbeat(ctx, wire.Handshake{}); e == nil {
		t.Fatal("a heartbeat to a stopped gateway succeeded")
	}
	if _, e := c.session.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.gateways[c.order[1]].log(); !equal(got, []string{"stream heartbeat"}) {
		t.Fatalf("second gateway saw %v", got)
	}
	// Control is not probed over HTTPS while a gateway is still untried.
	if len(c.control.log()) != 0 || c.session.Gateway() != c.order[1] {
		t.Fatalf("control saw %v, gateway %q", c.control.log(), c.session.Gateway())
	}
	// A session that works stays where it is.
	if _, e := c.session.Claim(ctx, 0, 0); e != nil {
		t.Fatal(e)
	}
	if got := c.gateways[c.order[1]].log(); !equal(got, []string{"stream heartbeat", "stream claim"}) {
		t.Fatalf("second gateway saw %v", got)
	}
}

func TestSessionMovesToTheNextGatewayWhenOneIsFull(t *testing.T) {
	c := gatewayCell(t, 2)
	c.gateways[c.order[0]].refuse = codes.ResourceExhausted
	ctx := t.Context()
	if _, e := c.session.Heartbeat(ctx, wire.Handshake{}); !errors.Is(e, errStreamRefused) {
		t.Fatalf("full gateway %v", e)
	}
	if _, e := c.session.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if c.session.Gateway() != c.order[1] || len(c.control.log()) != 0 {
		t.Fatalf("gateway %q, control saw %v", c.session.Gateway(), c.control.log())
	}
}

func TestSessionProbesHTTPSOnlyWhenNoGatewayAnswers(t *testing.T) {
	c := gatewayCell(t, 2)
	for _, server := range c.servers {
		server.Close()
	}
	ctx := t.Context()
	for range 2 {
		if _, e := c.session.Heartbeat(ctx, wire.Handshake{}); e == nil {
			t.Fatal("a heartbeat to a stopped gateway succeeded")
		}
	}
	if len(c.control.log()) != 0 {
		t.Fatalf("control was probed before every gateway was tried: %v", c.control.log())
	}
	// Every gateway failed, so the next attempt asks control whether HTTPS works.
	if _, e := c.session.Heartbeat(ctx, wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.control.log(); !equal(got, []string{"https heartbeat"}) {
		t.Fatalf("control saw %v", got)
	}
	// The round starts again where it began.
	if c.session.Gateway() != c.order[0] {
		t.Fatalf("gateway %q", c.session.Gateway())
	}
}

func TestSessionWithoutGatewaysUsesTheControlOrigin(t *testing.T) {
	c := gatewayCell(t, 0)
	if _, e := c.session.Heartbeat(t.Context(), wire.Handshake{}); e != nil {
		t.Fatal(e)
	}
	if got := c.control.log(); !equal(got, []string{"stream heartbeat"}) || c.session.Gateway() != "" {
		t.Fatalf("control saw %v, gateway %q", got, c.session.Gateway())
	}
}

func TestSessionKeepsFallbackUntilPreferredGatewayAcceptsHeartbeat(t *testing.T) {
	c := gatewayCell(t, 2)
	preferred, fallback := c.gateways[c.order[0]], c.gateways[c.order[1]]
	preferred.refuse = codes.Unavailable
	now := time.Now()
	c.session.now = func() time.Time { return now }
	c.session.client.GatewayRebalanceSeconds = wire.MinGatewayRebalanceSeconds
	ctx := t.Context()
	h := wire.Handshake{}
	if _, e := c.session.Heartbeat(ctx, h); e == nil {
		t.Fatal("the preferred gateway is down")
	}
	if _, e := c.session.Heartbeat(ctx, h); e != nil {
		t.Fatal(e)
	}
	if _, e := c.session.Heartbeat(ctx, h); e != nil {
		t.Fatal(e)
	}
	preferred.mu.Lock()
	preferred.refuse = codes.OK
	preferred.problem = &wire.SessionProblem{Status: 503, Code: "SERVICE_NOT_READY"}
	preferred.mu.Unlock()

	now = now.Add(2 * time.Duration(wire.MinGatewayRebalanceSeconds) * time.Second)
	if _, e := c.session.Heartbeat(ctx, h); e != nil {
		t.Fatalf("a refused preference check disrupted the heartbeat: %v", e)
	}
	if _, e := c.session.Claim(ctx, 0, 12); e != nil {
		t.Fatal(e)
	}
	if c.session.Gateway() != c.order[1] || !equal(fallback.log(), []string{"stream heartbeat", "stream heartbeat", "stream heartbeat", "stream claim"}) {
		t.Fatalf("lost fallback session after refusal: %q %v", c.session.Gateway(), fallback.log())
	}
	preferred.mu.Lock()
	preferred.problem = nil
	preferred.mu.Unlock()
	now = now.Add(2 * time.Duration(wire.MinGatewayRebalanceSeconds) * time.Second)
	if _, e := c.session.Heartbeat(ctx, h); e != nil {
		t.Fatal(e)
	}
	if _, e := c.session.Claim(ctx, 0, 12); e != nil {
		t.Fatal(e)
	}
	if c.session.Gateway() != c.order[0] || !equal(preferred.log(), []string{"stream heartbeat", "stream claim"}) || len(c.control.log()) != 0 {
		t.Fatalf("gateway %q preferred %v control %v", c.session.Gateway(), preferred.log(), c.control.log())
	}
	preferred.mu.Lock()
	defer preferred.mu.Unlock()
	if len(preferred.cursors) != 1 || preferred.cursors[0] != 12 {
		t.Fatalf("claim cursor after switching: %v", preferred.cursors)
	}
}
