package agentadmission

import (
	"context"
	"errors"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// seatPool hands out the seats of one control process. A call takes a seat at once when
// one is free, its priority is under its cap and no call of its priority is already
// waiting. Otherwise it queues, and every freed seat goes to the oldest waiter of the
// highest priority that is under its cap. A call that waits too long is refused.
type seatPool struct {
	mu      sync.Mutex
	total   int
	used    int
	classes [len(priorityOrder)]seatClass
}

// priorityOrder fixes the array index of each priority, highest first.
var priorityOrder = [...]wire.Priority{wire.PriorityQuarantine, wire.PriorityHeartbeat, wire.PriorityTelemetry, wire.PriorityBulk}

type seatClass struct {
	max, used, queueLength int
	wait                   time.Duration
	queue                  []*seatWaiter
}

type seatWaiter struct {
	ready   chan struct{}
	granted bool
}

// errNoSeat is returned when a call found no seat within its wait or its queue was full.
type errNoSeat struct{}

func (errNoSeat) Error() string { return "AGENT_OVERLOADED" }

func (s *seatPool) class(p wire.Priority) *seatClass { return &s.classes[p] }

func (s *seatPool) fits(c *seatClass) bool { return s.used < s.total && c.used < c.max }

// enter takes a seat of priority p, waiting up to the priority's wait. It returns the
// function that frees the seat.
func (s *seatPool) enter(ctx context.Context, p wire.Priority) (func(), error) {
	s.mu.Lock()
	c := s.class(p)
	if len(c.queue) == 0 && s.fits(c) {
		s.used++
		c.used++
		s.mu.Unlock()
		return s.releaser(p), nil
	}
	if len(c.queue) >= c.queueLength || c.wait <= 0 {
		s.mu.Unlock()
		return nil, errNoSeat{}
	}
	w := &seatWaiter{ready: make(chan struct{})}
	c.queue = append(c.queue, w)
	s.mu.Unlock()

	timer := time.NewTimer(c.wait)
	defer timer.Stop()
	select {
	case <-w.ready:
		return s.releaser(p), nil
	case <-timer.C:
	case <-ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.granted {
		// The seat arrived as the wait ended. A caller that gave up returns it.
		if ctx.Err() != nil {
			s.free(p)
			return nil, ctx.Err()
		}
		return s.releaser(p), nil
	}
	for i, queued := range c.queue {
		if queued == w {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			break
		}
	}
	// A request deadline that ends the wait is a wait that found no seat; a caller that
	// went away gets its own error.
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	return nil, errNoSeat{}
}

// releaser returns a function that frees one seat of p exactly once.
func (s *seatPool) releaser(p wire.Priority) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.free(p)
			s.mu.Unlock()
		})
	}
}

// free returns a seat of p and hands seats to waiters, highest priority first. The
// caller holds mu.
func (s *seatPool) free(p wire.Priority) {
	s.used--
	s.class(p).used--
	for i := range s.classes {
		c := &s.classes[i]
		for len(c.queue) > 0 && s.fits(c) {
			w := c.queue[0]
			c.queue = c.queue[1:]
			s.used++
			c.used++
			w.granted = true
			close(w.ready)
		}
	}
}

// load reports the seats in use and the queued calls of p.
func (s *seatPool) load(p wire.Priority) (used, queued int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.class(p)
	return c.used, len(c.queue)
}
