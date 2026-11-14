package agentadmission

import (
	"math"
	"sync"
	"time"
)

// sweepInterval spaces the scans that drop full buckets when the table is full, so a
// caller that keeps the table full cannot make every call scan it.
const sweepInterval = time.Second

// limit is a token bucket: rate tokens per second, at most burst held.
type limit struct {
	rate  float64
	burst float64
}

type bucket struct {
	tokens  float64
	updated time.Time
	limit   limit
}

// refill brings b up to now.
func (b *bucket) refill(now time.Time) {
	if elapsed := now.Sub(b.updated).Seconds(); elapsed > 0 {
		b.tokens = math.Min(b.limit.burst, b.tokens+elapsed*b.limit.rate)
	}
	b.updated = now
}

// buckets holds the token buckets of the address and per-node limits, at most capacity
// of them. A full bucket carries no state, because a new one starts full, so it is the
// one dropped when room is needed.
type buckets struct {
	mu       sync.Mutex
	capacity int
	table    map[string]*bucket
	swept    time.Time
}

func newBuckets(capacity int) *buckets {
	return &buckets{capacity: capacity, table: make(map[string]*bucket)}
}

// take spends one token of key. When none is left it returns the whole seconds until
// one is, at least one. full reports that key is new and the table has no room for it.
func (b *buckets) take(key string, l limit, now time.Time) (retry int, full bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	current := b.table[key]
	if current == nil {
		if len(b.table) >= b.capacity && !b.sweep(now) {
			return 0, true
		}
		current = &bucket{tokens: l.burst, updated: now, limit: l}
		b.table[key] = current
	}
	current.refill(now)
	if current.tokens >= 1 {
		current.tokens--
		return 0, false
	}
	return max(1, int(math.Ceil((1-current.tokens)/l.rate))), false
}

// sweep drops full buckets and reports whether that made room. It runs at most once per
// sweepInterval; a call that finds the table full in between is refused.
func (b *buckets) sweep(now time.Time) bool {
	if now.Sub(b.swept) < sweepInterval {
		return false
	}
	b.swept = now
	for key, current := range b.table {
		current.refill(now)
		if current.tokens >= current.limit.burst {
			delete(b.table, key)
		}
	}
	return len(b.table) < b.capacity
}

// size reports how many buckets are held.
func (b *buckets) size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.table)
}
