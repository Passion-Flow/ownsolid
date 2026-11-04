package nodes

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const waitNode = "0d6f2c52-7f43-4b5e-9a7d-5e9c1f3b2a11"

func TestClaimWithoutWaitClaimsOnce(t *testing.T) {
	var calls atomic.Int32
	out, e := waitForClaim(context.Background(), &wakeHub{}, waitNode, 0, func() (wire.Claim, error) { calls.Add(1); return wire.Claim{}, nil })
	if e != nil || out.Lease != nil || calls.Load() != 1 {
		t.Fatalf("claim %+v %v calls %d", out, e, calls.Load())
	}
}

func TestWaitingClaimReturnsWorkWhenATaskIsWritten(t *testing.T) {
	hub := &wakeHub{}
	var written atomic.Bool
	var calls atomic.Int32
	claim := func() (wire.Claim, error) {
		calls.Add(1)
		if written.Load() {
			return wire.Claim{Lease: &wire.Lease{TaskID: "task"}}, nil
		}
		return wire.Claim{}, nil
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		written.Store(true)
		hub.wake(waitNode)
	}()
	started := time.Now()
	out, e := waitForClaim(context.Background(), hub, waitNode, 5*time.Second, claim)
	if e != nil || out.Lease == nil || out.Lease.TaskID != "task" {
		t.Fatalf("claim %+v %v", out, e)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("woken claim took %s", elapsed)
	}
	if calls.Load() != 2 {
		t.Fatalf("claims %d", calls.Load())
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.waiters) != 0 {
		t.Fatalf("finished claims leave no watch: %v", hub.waiters)
	}
}

func TestWaitingClaimEndsEmptyAtItsWait(t *testing.T) {
	hub := &wakeHub{}
	started := time.Now()
	out, e := waitForClaim(context.Background(), hub, waitNode, 150*time.Millisecond, func() (wire.Claim, error) { return wire.Claim{}, nil })
	if e != nil || out.Lease != nil {
		t.Fatalf("claim %+v %v", out, e)
	}
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("wait lasted %s", elapsed)
	}
	if len(hub.waiters) != 0 {
		t.Fatal("expired claim leaves no watch")
	}
}

func TestWaitingClaimStopsWithItsRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out, e := waitForClaim(ctx, &wakeHub{}, waitNode, 10*time.Second, func() (wire.Claim, error) { return wire.Claim{}, nil })
	if e != nil || out.Lease != nil {
		t.Fatalf("claim %+v %v", out, e)
	}
}

func TestWaitingClaimReturnsClaimErrors(t *testing.T) {
	_, e := waitForClaim(context.Background(), &wakeHub{}, waitNode, time.Second, func() (wire.Claim, error) { return wire.Claim{}, errors.New("AGENT_UNAUTHORIZED") })
	if e == nil || e.Error() != "AGENT_UNAUTHORIZED" {
		t.Fatalf("error %v", e)
	}
}

func TestWakeForAnotherNodeDoesNotWake(t *testing.T) {
	hub := &wakeHub{}
	ch := hub.watch(waitNode)
	hub.wake("other")
	select {
	case <-ch:
		t.Fatal("woken by another node")
	default:
	}
	hub.wake(waitNode)
	select {
	case <-ch:
	default:
		t.Fatal("not woken")
	}
}
