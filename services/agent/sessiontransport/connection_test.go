package sessiontransport

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/stats"
)

func unconnectedChannel(t *testing.T) *Connection {
	t.Helper()
	c, err := New("passthrough:///127.0.0.1:0", grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func tagOwned(c *Connection, port int) (context.Context, *transport) {
	tagged := c.TagConn(context.Background(), &stats.ConnTagInfo{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}, RemoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443}})
	return tagged, tagged.Value(transportKey{}).(transportTag).lifetime
}
func awaitRetirement(t *testing.T, r *Retirement) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("owned retirement worker did not join")
	}
}

func TestRetirementWaitsForPhysicalTransportEnd(t *testing.T) {
	c := unconnectedChannel(t)
	tagged, life := tagOwned(c, 1)
	r, err := c.Retire(life.ended, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	select {
	case <-r.Done():
		t.Fatal("unused channel closed before transport end")
	default:
	}
	if c.GetState() != connectivity.Idle {
		t.Fatal("new channel connected without an exchange")
	}
	c.HandleConn(tagged, &stats.ConnEnd{})
	awaitRetirement(t, r)
	if c.GetState() != connectivity.Shutdown {
		t.Fatal("transport end did not release channel")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.live) != 0 {
		t.Fatal("ended transport retained")
	}
}
func TestExplicitRetirementCloseJoinsConcurrentCallers(t *testing.T) {
	c := unconnectedChannel(t)
	tagged, life := tagOwned(c, 2)
	r, err := c.Retire(life.ended, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	awaitRetirement(t, r)
	c.HandleConn(tagged, &stats.ConnEnd{})
}
func TestRetirementHardDeadlineBoundsMissingEnd(t *testing.T) {
	c := unconnectedChannel(t)
	tagged, life := tagOwned(c, 3)
	r, err := c.Retire(life.ended, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	awaitRetirement(t, r)
	if c.GetState() != connectivity.Shutdown {
		t.Fatal("hard deadline retained channel")
	}
	c.HandleConn(tagged, &stats.ConnEnd{})
}
func TestRetirementRejectsForeignOrMissingLifetime(t *testing.T) {
	c, other := unconnectedChannel(t), unconnectedChannel(t)
	tagged, life := tagOwned(other, 4)
	defer other.HandleConn(tagged, &stats.ConnEnd{})
	for _, ctx := range []context.Context{nil, context.Background(), life.ended} {
		if r, err := c.Retire(ctx, time.Now().Add(time.Minute)); err == nil || r != nil {
			t.Fatal("foreign or missing transport lifetime was accepted")
		}
	}
	if r, err := other.Retire(life.ended, time.Time{}); err == nil || r != nil {
		t.Fatal("missing close deadline was accepted")
	}
}
func TestLatePhysicalEndPreservesReusedAddressIdentity(t *testing.T) {
	c := unconnectedChannel(t)
	older, oldLife := tagOwned(c, 5)
	newer, newLife := tagOwned(c, 5)
	c.HandleConn(older, &stats.ConnEnd{})
	c.HandleConn(older, &stats.ConnEnd{})
	if oldLife.ended.Err() == nil || newLife.ended.Err() != nil {
		t.Fatal("late end changed the current transport")
	}
	c.mu.Lock()
	got := c.live[newLife.key]
	c.mu.Unlock()
	if got != newLife {
		t.Fatal("late end deleted replacement identity")
	}
	c.HandleConn(newer, &stats.ConnEnd{})
}
func TestTransportEventsReleaseConcurrentRecords(t *testing.T) {
	c := unconnectedChannel(t)
	var workers sync.WaitGroup
	for i := range 300 {
		workers.Go(func() {
			tagged, life := tagOwned(c, i+100)
			c.HandleConn(tagged, &stats.ConnBegin{})
			if life.ended.Err() != nil {
				t.Error("begin ended transport")
			}
			c.HandleConn(tagged, &stats.ConnEnd{})
			if life.ended.Err() == nil {
				t.Error("end did not end transport")
			}
		})
	}
	workers.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.live) != 0 {
		t.Fatalf("retained %d physical records", len(c.live))
	}
}
