package nodes

import (
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const (
	viewNode   = "node"
	viewMaxAge = time.Minute
	viewLimit  = 2
)

func openCache() *viewCache {
	c := &viewCache{}
	c.setOpen(true)
	return c
}

func TestAViewIsServedUntilItExpires(t *testing.T) {
	c := openCache()
	now := time.Now()
	c.keep(viewNode, nodeView{status: "active"}, c.begin(), now, viewMaxAge, viewLimit)
	if v, ok := c.lookup(viewNode, now); !ok || v.status != "active" {
		t.Fatal("a fresh view was not served", v, ok)
	}
	if _, ok := c.lookup(viewNode, now.Add(viewMaxAge)); ok {
		t.Fatal("a view was served at its maximum age")
	}
}

func TestExpiryIsSpreadOverHalfTheAge(t *testing.T) {
	c := openCache()
	now := time.Now()
	earliest, latest := now.Add(viewMaxAge), now
	for range 1000 {
		c.keep(viewNode, nodeView{}, c.begin(), now, viewMaxAge, viewLimit)
		expires := c.views[viewNode].expires
		if expires.Before(earliest) {
			earliest = expires
		}
		if expires.After(latest) {
			latest = expires
		}
	}
	if earliest.Before(now.Add(viewMaxAge/2)) || latest.After(now.Add(viewMaxAge)) {
		t.Fatalf("expiry outside half the age: %s to %s", earliest.Sub(now), latest.Sub(now))
	}
	if latest.Sub(earliest) < viewMaxAge/4 {
		t.Fatalf("expiry barely spread: %s", latest.Sub(earliest))
	}
}

func TestALoadOverlappedByAChangeIsNotKept(t *testing.T) {
	for name, change := range map[string]func(*viewCache){
		"one node":      func(c *viewCache) { c.drop("another node") },
		"every node":    func(c *viewCache) { c.dropAll() },
		"reader failed": func(c *viewCache) { c.setOpen(false); c.setOpen(true) },
	} {
		t.Run(name, func(t *testing.T) {
			c := openCache()
			begun := c.begin()
			change(c)
			c.keep(viewNode, nodeView{}, begun, time.Now(), viewMaxAge, viewLimit)
			if _, ok := c.lookup(viewNode, time.Now()); ok {
				t.Fatal("a load older than a change was kept")
			}
		})
	}
}

func TestAClosedCacheServesAndKeepsNothing(t *testing.T) {
	c := openCache()
	now := time.Now()
	c.keep(viewNode, nodeView{}, c.begin(), now, viewMaxAge, viewLimit)
	c.setOpen(false)
	if _, ok := c.lookup(viewNode, now); ok {
		t.Fatal("a closed cache served a view")
	}
	c.keep(viewNode, nodeView{}, c.begin(), now, viewMaxAge, viewLimit)
	c.setOpen(true)
	if _, ok := c.lookup(viewNode, now); ok {
		t.Fatal("reopening brought back a view read while closed")
	}
	if c.fallbacks.Load() != 1 {
		t.Fatalf("%d fallbacks", c.fallbacks.Load())
	}
	var zero viewCache
	if _, ok := zero.lookup(viewNode, now); ok {
		t.Fatal("the zero value served a view")
	}
}

func TestTheCacheNeverHoldsMoreThanItsLimit(t *testing.T) {
	c := openCache()
	now := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		c.keep(id, nodeView{}, c.begin(), now, viewMaxAge, viewLimit)
	}
	if len(c.views) != viewLimit {
		t.Fatalf("%d views", len(c.views))
	}
	c.keep("a", nodeView{status: "renewed"}, c.begin(), now, viewMaxAge, viewLimit)
	if v, ok := c.lookup("a", now); !ok || v.status != "renewed" {
		t.Fatal("a full cache refused to renew a view it holds")
	}
}

func TestAViewAnswersOnlyTheHeartbeatItHolds(t *testing.T) {
	h := wire.Handshake{ProtocolVersion: "1", Capabilities: []string{"a", "b"}}
	v := nodeView{credential: []byte("secret"), protocol: h.ProtocolVersion, profile: h.Profile(), capabilities: h.Capabilities}
	if !v.answers([]byte("secret"), h) {
		t.Fatal("the view refused its own heartbeat")
	}
	other := h
	other.ProtocolVersion = "2"
	fewer := h
	fewer.Capabilities = []string{"a"}
	if v.answers([]byte("wrong!"), h) || v.answers(nil, h) || v.answers([]byte("secret"), other) || v.answers([]byte("secret"), fewer) {
		t.Fatal("the view answered something it does not hold")
	}
}

func TestCachedTimeStaysOnTheDatabaseClock(t *testing.T) {
	var c viewCache
	local := time.Now()
	c.noteClock(local.Add(3*time.Second), local)
	if got := c.databaseTime(local.Add(time.Second)); !got.Equal(local.Add(4 * time.Second)) {
		t.Fatalf("%s after local", got.Sub(local))
	}
}
