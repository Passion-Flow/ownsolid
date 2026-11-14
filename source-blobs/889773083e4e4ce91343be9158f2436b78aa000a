package main

import (
	"context"
	"errors"
	"sync"
)

// The zero value owns admission without allocating a waiter per request/join.
// Sealing and registration share the mutex; a completed join cannot miss an Add.
type agentGatewayRequests struct {
	mu       sync.Mutex
	active   int
	stopping bool
	joined   chan struct{}
}

func (o *agentGatewayRequests) begin() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopping {
		return false
	}
	o.active++
	return true
}

func (o *agentGatewayRequests) end() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.active--
	if o.stopping && o.active == 0 {
		close(o.joined)
	}
}

func (o *agentGatewayRequests) stop() <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.stopping {
		o.joined = make(chan struct{})
		o.stopping = true
		if o.active == 0 {
			close(o.joined)
		}
	}
	return o.joined
}

func (o *agentGatewayRequests) wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("AGENT_GATEWAY_CONTEXT_REQUIRED")
	}
	joined := o.stop()
	select {
	case <-joined:
		return nil
	default:
	}
	select {
	case <-joined:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
