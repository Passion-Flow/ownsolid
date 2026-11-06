package nodes

import (
	"context"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// claimSettle is how long a claim woken inside the writing transaction waits before it looks
// again, because the first look can miss a task that has not committed. A wake from the outbox
// follows the commit and needs no wait (ADR 0028).
const claimSettle = 100 * time.Millisecond

// wakeHub tells claims waiting on this process that a task for their node was written. The zero
// value is ready to use. A missed wake costs at most one claim wait, because the agent claims
// again after its next heartbeat.
type wakeHub struct {
	mu      sync.Mutex
	waiters map[string]chan struct{}
}

// watch returns a channel that is closed at the next wake for node.
func (h *wakeHub) watch(node string) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.waiters == nil {
		h.waiters = map[string]chan struct{}{}
	}
	ch := h.waiters[node]
	if ch == nil {
		ch = make(chan struct{})
		h.waiters[node] = ch
	}
	return ch
}

func (h *wakeHub) wake(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch := h.waiters[node]; ch != nil {
		close(ch)
		delete(h.waiters, node)
	}
}

// release drops an unused watch so idle nodes hold no entry.
func (h *wakeHub) release(node string, ch <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current := h.waiters[node]; current != nil && (<-chan struct{})(current) == ch {
		delete(h.waiters, node)
	}
}

// Seat takes a serving seat for one step of an admitted call and returns its release
// (ADR 0018).
type Seat func(context.Context) (release func(), err error)

// ClaimWaiting leases the next task like Claim. When there is none, it holds the claim
// for up to wait and claims again as soon as a task for the node is written. A claim
// that ends without work returns an empty lease. Each look for work takes a seat, so a
// held claim holds none while it waits.
func (s *Service) ClaimWaiting(ctx context.Context, id, credential, request string, input wire.ClaimRequest, seat Seat) (wire.Claim, error) {
	wait := time.Duration(input.WaitSeconds) * time.Second
	return waitForClaim(ctx, &s.wake, id, wait, s.wakeSettle(), func() (wire.Claim, error) {
		release, e := seat(ctx)
		if e != nil {
			return wire.Claim{}, e
		}
		defer release()
		return s.ClaimFrom(ctx, id, credential, request, input)
	})
}

func waitForClaim(ctx context.Context, hub *wakeHub, node string, wait, settle time.Duration, claim func() (wire.Claim, error)) (wire.Claim, error) {
	if wait <= 0 {
		return claim()
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		// Watch before claiming, so a task written between the two is not missed.
		woken := hub.watch(node)
		out, e := claim()
		if e != nil || out.Lease != nil || out.Resync != nil {
			hub.release(node, woken)
			return out, e
		}
		select {
		case <-woken:
		case <-deadline.C:
			hub.release(node, woken)
			return out, nil
		case <-ctx.Done():
			hub.release(node, woken)
			return out, nil
		}
		if settle <= 0 {
			continue
		}
		pause := time.NewTimer(settle)
		select {
		case <-pause.C:
		case <-ctx.Done():
			pause.Stop()
			return out, nil
		}
	}
}
