package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

// fastRenewal shortens the renewal interval for one test.
func fastRenewal(t *testing.T) {
	t.Helper()
	previous := renewalInterval
	renewalInterval = 10 * time.Millisecond
	t.Cleanup(func() { renewalInterval = previous })
}

var shed = &client.Problem{Code: "AGENT_OVERLOADED", Status: 503, RetryAfter: 2 * time.Second}

func TestShedRenewalIsRetriedAtTheNextTick(t *testing.T) {
	fastRenewal(t)
	var calls atomic.Int32
	renew := func(context.Context) (time.Time, error) {
		if calls.Add(1) <= 3 {
			return time.Time{}, shed
		}
		return time.Now().Add(time.Second), nil
	}
	bounded, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := make(chan struct{})
	done, failed := renewWhileRunning(bounded, cancel, renew, "task", time.Now().Add(time.Second), stop)
	for calls.Load() < 6 {
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	<-done
	select {
	case e := <-failed:
		t.Fatalf("shed renewals ended the task: %v", e)
	default:
	}
	if bounded.Err() != nil {
		t.Fatal("the task was cancelled")
	}
}

func TestLeaseExpiresWhenEveryRenewalIsShed(t *testing.T) {
	fastRenewal(t)
	renew := func(context.Context) (time.Time, error) { return time.Time{}, shed }
	bounded, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := time.Now()
	done, failed := renewWhileRunning(bounded, cancel, renew, "task", started.Add(60*time.Millisecond), make(chan struct{}))
	<-done
	if e := <-failed; e == nil || e.Error() != "AGENT_LEASE_STALE" {
		t.Fatalf("expired lease %v", e)
	}
	if bounded.Err() == nil || time.Since(started) < 60*time.Millisecond {
		t.Fatal("the task outlived its lease or ended early")
	}
}

func TestRefusedRenewalWithoutAHintEndsTheTask(t *testing.T) {
	fastRenewal(t)
	lost := errors.New("AGENT_LEASE_STALE")
	renew := func(context.Context) (time.Time, error) { return time.Time{}, &client.Problem{Code: lost.Error(), Status: 409} }
	bounded, cancel := context.WithCancel(t.Context())
	defer cancel()
	done, failed := renewWhileRunning(bounded, cancel, renew, "task", time.Now().Add(time.Minute), make(chan struct{}))
	<-done
	if e := <-failed; e == nil || e.Error() != lost.Error() {
		t.Fatalf("lost lease %v", e)
	}
	if bounded.Err() == nil {
		t.Fatal("the task kept running without its lease")
	}
}

func TestLostBeforeStartReportsTheReason(t *testing.T) {
	reason := errors.New("AGENT_OVERLOADED")
	done, failed := lostBeforeStart(reason)
	<-done
	if e := <-failed; e != reason {
		t.Fatalf("reason %v", e)
	}
}
