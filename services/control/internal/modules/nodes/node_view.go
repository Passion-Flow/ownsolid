package nodes

import (
	"crypto/subtle"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// Node views (ARC-101, ADR 0047). A repeated heartbeat is answered from a view of the node in
// memory. Triggers announce every change to a view's columns through the outbox. Events are hints
// (ADR 0028), so a view also expires, and the cache serves nothing while the reader is not reading.
const (
	// topicNodeViewChanged says the node named by the key changed.
	topicNodeViewChanged = "node.view_changed"
	// topicNodeViewsChanged says a workspace or tenant changed, which reaches many nodes.
	topicNodeViewsChanged = "node.views_changed"
)

// nodeView is what a heartbeat is checked against.
type nodeView struct {
	credential       []byte
	status, protocol string
	profile          string
	capabilities     []string
	expires          time.Time
	// pins are the keys of the node's certificates (ADR 0050).
	pins certificatePins
}

// answers reports that a heartbeat with this credential repeats what the view holds.
func (v nodeView) answers(credential []byte, h wire.Handshake) bool {
	return subtle.ConstantTimeCompare(v.credential, credential) == 1 && v.protocol == h.ProtocolVersion &&
		v.profile == h.Profile() && slices.Equal(v.capabilities, h.Capabilities)
}

// viewCache holds node views. The zero value is closed, and serves and keeps nothing.
type viewCache struct {
	mu    sync.RWMutex
	views map[string]nodeView
	// open says the outbox reader is reading, so changes reach the cache.
	open bool
	// invalidations counts every drop, so a load can tell one overlapped it.
	invalidations uint64
	// clockOffset is the database clock less this process's, in nanoseconds.
	clockOffset atomic.Int64

	hits, misses, drops, fallbacks atomic.Uint64
}

// begin marks the start of a load, for keep.
func (c *viewCache) begin() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.invalidations
}

// lookup returns a view that has not expired.
func (c *viewCache) lookup(id string, now time.Time) (nodeView, bool) {
	c.mu.RLock()
	open := c.open
	v, ok := c.views[id]
	c.mu.RUnlock()
	switch {
	case !open:
		c.fallbacks.Add(1)
		return nodeView{}, false
	case !ok || !now.Before(v.expires):
		c.misses.Add(1)
		return nodeView{}, false
	}
	c.hits.Add(1)
	return v, true
}

// keep stores a view read since begun returned, unless something was dropped meanwhile: the load
// may then hold what the change replaced. Up to half of maxAge comes off at random, so a fleet
// that started together does not expire together.
func (c *viewCache) keep(id string, v nodeView, begun uint64, now time.Time, maxAge time.Duration, limit int) {
	v.expires = now.Add(maxAge - rand.N(maxAge/2+1))
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open || c.invalidations != begun {
		return
	}
	if _, present := c.views[id]; !present && len(c.views) >= limit {
		return
	}
	if c.views == nil {
		c.views = map[string]nodeView{}
	}
	c.views[id] = v
}

// drop forgets one node.
func (c *viewCache) drop(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidations++
	delete(c.views, id)
	c.drops.Add(1)
}

// dropAll forgets every node.
func (c *viewCache) dropAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clear()
}

// clear drops everything; the caller holds the lock.
func (c *viewCache) clear() {
	c.invalidations++
	c.drops.Add(uint64(len(c.views)))
	c.views = nil
}

// setOpen starts or stops serving. Stopping forgets every view: a reader that failed may have
// missed events, and it is cheaper to read again than to reason about which.
func (c *viewCache) setOpen(open bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !open {
		c.clear()
	}
	c.open = open
}

// noteClock records the database's time against this process's.
func (c *viewCache) noteClock(database, local time.Time) {
	c.clockOffset.Store(int64(database.Sub(local)))
}

// databaseTime is the database's time now, by the offset last measured.
func (c *viewCache) databaseTime(local time.Time) time.Time {
	return local.Add(time.Duration(c.clockOffset.Load())).UTC()
}

// samples reports the cache for the metrics store.
func (c *viewCache) samples() []telemetrystore.Sample {
	return []telemetrystore.Sample{
		{Name: "ownsolid_node_view_hits_total", Value: float64(c.hits.Load())},
		{Name: "ownsolid_node_view_misses_total", Value: float64(c.misses.Load())},
		{Name: "ownsolid_node_view_drops_total", Value: float64(c.drops.Load())},
		{Name: "ownsolid_node_view_fallbacks_total", Value: float64(c.fallbacks.Load())},
	}
}
