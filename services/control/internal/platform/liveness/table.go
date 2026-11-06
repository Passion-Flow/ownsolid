// Package liveness keeps what a gateway knows about the nodes that talk to it in memory,
// so that a heartbeat costs no database write (ADR 0022). Every operation is bounded by
// the fleet and does no IO; the owner of the table decides when and how to persist what
// Drain and Transitions return.
package liveness

import (
	"errors"
	"hash/maphash"
	"sync"
	"time"
)

// Connectivity is a node's state as derived from when it was last seen.
type Connectivity string

const (
	Online  Connectivity = "online"
	Unknown Connectivity = "unknown"
	Offline Connectivity = "offline"
)

// rank orders states from healthiest to worst.
func (c Connectivity) rank() int {
	switch c {
	case Online:
		return 0
	case Unknown:
		return 1
	default:
		return 2
	}
}

// Thresholds are the ages at which a silent node stops being online and becomes offline.
type Thresholds struct {
	Online  time.Duration
	Offline time.Duration
}

// at derives the state of a node last seen at seen. A boundary age keeps the better state.
func (t Thresholds) at(seen, now time.Time) Connectivity {
	age := now.Sub(seen)
	switch {
	case age <= t.Online:
		return Online
	case age <= t.Offline:
		return Unknown
	default:
		return Offline
	}
}

// Sighting says a node was heard at a time. HasReport says Report is the latest report it
// sent since the last drain, received at ReportAt; a later contact that carried no report
// can move At past ReportAt, and a report is only ever as fresh as ReportAt.
type Sighting[R any] struct {
	Node      string
	At        time.Time
	HasReport bool
	Report    R
	ReportAt  time.Time
}

// Transition says a node's connectivity differs from the one last recorded.
type Transition struct {
	Node     string
	To       Connectivity
	LastSeen time.Time
}

const shardCount = 64

type entry[R any] struct {
	seen      time.Time
	dirty     bool
	hasReport bool
	report    R
	reportAt  time.Time
	recorded  Connectivity
	digest    [32]byte
	written   time.Time
}

// takeReport holds report as the latest received at at.
func (e *entry[R]) takeReport(report R, at time.Time) {
	e.hasReport, e.report, e.reportAt = true, report, at
}

// dropReport forgets the pending report, because a state at least as recent as at was
// written durably and an older report must not overwrite it.
func (e *entry[R]) dropReport(at time.Time) {
	var none R
	e.hasReport, e.report = false, none
	if at.After(e.reportAt) {
		e.reportAt = at
	}
}

type shard[R any] struct {
	mu      sync.Mutex
	entries map[string]*entry[R]
}

// Table is safe for concurrent use. R is the report a heartbeat carries.
type Table[R any] struct {
	thresholds Thresholds
	started    time.Time
	seed       maphash.Seed
	shards     [shardCount]shard[R]
}

// New returns an empty table. started is when this process began to listen: a node is not
// declared worse than it was recorded until the thresholds have passed since then, so a
// control restart does not read as a fleet outage.
func New[R any](thresholds Thresholds, started time.Time) (*Table[R], error) {
	if thresholds.Online <= 0 || thresholds.Offline <= thresholds.Online {
		return nil, errors.New("LIVENESS_THRESHOLDS_INVALID")
	}
	t := &Table[R]{thresholds: thresholds, started: started, seed: maphash.MakeSeed()}
	for i := range t.shards {
		t.shards[i].entries = map[string]*entry[R]{}
	}
	return t, nil
}

func (t *Table[R]) shard(node string) *shard[R] {
	return &t.shards[maphash.String(t.seed, node)%shardCount]
}

// locked runs fn on the entry of node, creating it as a newly online node when create is
// set. It reports whether an entry exists.
func (t *Table[R]) locked(node string, create bool, fn func(*entry[R])) bool {
	s := t.shard(node)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[node]
	if !ok {
		if !create {
			return false
		}
		e = &entry[R]{recorded: Online}
		s.entries[node] = e
	}
	fn(e)
	return true
}

// Observe records that node was heard at at, carrying report. Time only moves forward: an
// older sighting neither moves the last-seen time back nor replaces a newer report.
func (t *Table[R]) Observe(node string, at time.Time, report R) {
	t.locked(node, true, func(e *entry[R]) {
		if at.Before(e.seen) {
			return
		}
		e.seen, e.dirty = at, true
		e.takeReport(report, at)
	})
}

// Touch records a contact that carried no report, keeping any report still pending.
func (t *Table[R]) Touch(node string, at time.Time) {
	t.locked(node, true, func(e *entry[R]) {
		if !at.Before(e.seen) {
			e.seen, e.dirty = at, true
		}
	})
}

// Merge takes a sighting from another gateway. That gateway persists it, so it is not
// marked for draining here.
func (t *Table[R]) Merge(node string, at time.Time) {
	t.locked(node, true, func(e *entry[R]) {
		if at.After(e.seen) {
			e.seen = at
		}
	})
}

// Load seeds a node from its stored record when the process starts. It never overrides
// what the table has already heard.
func (t *Table[R]) Load(node string, seen time.Time, recorded Connectivity) {
	t.locked(node, true, func(e *entry[R]) {
		if seen.After(e.seen) {
			e.seen = seen
		}
		e.recorded = recorded
	})
}

// Forget drops a node that was removed.
func (t *Table[R]) Forget(node string) {
	s := t.shard(node)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, node)
}

// Seen returns when node was last heard.
func (t *Table[R]) Seen(node string) (time.Time, bool) {
	var seen time.Time
	ok := t.locked(node, false, func(e *entry[R]) { seen = e.seen })
	return seen, ok
}

// Size is the number of nodes the table tracks.
func (t *Table[R]) Size() int {
	total := 0
	for i := range t.shards {
		s := &t.shards[i]
		s.mu.Lock()
		total += len(s.entries)
		s.mu.Unlock()
	}
	return total
}

// Accept records a synchronous write: the node was heard at at, its state with this digest
// is durable, and written is the time Unchanged measures its age from. A pending older
// report is dropped so that it cannot overwrite that state.
func (t *Table[R]) Accept(node string, digest [32]byte, at, written time.Time) {
	t.locked(node, true, func(e *entry[R]) {
		e.digest, e.written = digest, written
		if at.After(e.seen) {
			e.seen = at
		}
		if !at.Before(e.reportAt) {
			e.dropReport(at)
		}
	})
}

// Unchanged reports whether digest is the state last written for node, no longer ago than
// maximumAge. A table that has not heard of the node, as after a restart, reports false.
func (t *Table[R]) Unchanged(node string, digest [32]byte, now time.Time, maximumAge time.Duration) bool {
	same := false
	t.locked(node, false, func(e *entry[R]) {
		same = !e.written.IsZero() && e.digest == digest && now.Sub(e.written) <= maximumAge
	})
	return same
}

// Drain returns up to limit sightings not yet drained and clears their mark. The caller
// returns the ones it could not persist with Restore.
func (t *Table[R]) Drain(limit int) []Sighting[R] {
	var out []Sighting[R]
	var none R
	for i := range t.shards {
		s := &t.shards[i]
		s.mu.Lock()
		for node, e := range s.entries {
			if len(out) >= limit {
				break
			}
			if e.dirty {
				out = append(out, Sighting[R]{Node: node, At: e.seen, HasReport: e.hasReport, Report: e.report, ReportAt: e.reportAt})
				e.dirty, e.hasReport, e.report = false, false, none
			}
		}
		s.mu.Unlock()
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Restore marks sightings for draining again after a failed write. A newer report of the
// same node wins, a report older than a durable write stays dropped, and a node forgotten
// meanwhile stays forgotten.
func (t *Table[R]) Restore(sightings []Sighting[R]) {
	for _, v := range sightings {
		t.locked(v.Node, false, func(e *entry[R]) {
			e.dirty = true
			if v.HasReport && !e.hasReport && !v.ReportAt.Before(e.reportAt) {
				e.takeReport(v.Report, v.ReportAt)
			}
		})
	}
}

// Transitions returns up to limit nodes whose derived connectivity differs from the one
// recorded. A node gets better on its real last-seen time; it gets worse only on the later
// of that time and the process start.
func (t *Table[R]) Transitions(now time.Time, limit int) []Transition {
	var out []Transition
	for i := range t.shards {
		s := &t.shards[i]
		s.mu.Lock()
		for node, e := range s.entries {
			if len(out) >= limit {
				break
			}
			if to, changed := t.next(e, now); changed {
				out = append(out, Transition{Node: node, To: to, LastSeen: e.seen})
			}
		}
		s.mu.Unlock()
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (t *Table[R]) next(e *entry[R], now time.Time) (Connectivity, bool) {
	actual := t.thresholds.at(e.seen, now)
	if actual.rank() < e.recorded.rank() {
		return actual, true
	}
	effective := e.seen
	if t.started.After(effective) {
		effective = t.started
	}
	if worse := t.thresholds.at(effective, now); worse.rank() > e.recorded.rank() {
		return worse, true
	}
	return "", false
}

// Recorded notes that a transition was stored, so it is not returned again.
func (t *Table[R]) Recorded(transitions []Transition) {
	for _, v := range transitions {
		t.locked(v.Node, false, func(e *entry[R]) { e.recorded = v.To })
	}
}
