package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestRuntimeCancellationRetainsActualUnknownOutcome(t *testing.T) {
	for _, outcome := range []error{&transaction.Error{Kind: transaction.CommitUnknown}, errors.New(string(transaction.CommitRolledBack)), errors.New("private vendor detail"), errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown}), errors.Join(context.DeadlineExceeded, errors.New("private vendor detail")), errors.Join(&transaction.Error{Kind: transaction.Cancelled}, &transaction.Error{Kind: transaction.CommitUnknown})} {
		g := newRuntimeLifecycle(t.Context())
		started := make(chan struct{})
		release := make(chan struct{})
		g.Go("TEST_WORKER", func(ctx context.Context) error { close(started); <-release; return outcome })
		<-started
		g.cancel(context.Canceled)
		close(release)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		e := g.Close(ctx)
		cancel()
		if e == nil {
			t.Fatal("actual error lost after cancellation")
		}
		if strings.Contains(e.Error(), "private vendor") {
			t.Fatal("raw worker detail leaked")
		}
		if strings.HasPrefix(outcome.Error(), "STORAGE_COMMIT_") && !strings.Contains(e.Error(), outcome.Error()) {
			t.Fatal("commit outcome lost", e)
		}
		if runtimeContainsOutcome(outcome, transaction.CommitUnknown) && !strings.Contains(e.Error(), string(transaction.CommitUnknown)) {
			t.Fatal("joined unknown commit swallowed", e)
		}
	}
}

func TestRuntimeContextOnlyRequiresEveryErrorLeaf(t *testing.T) {
	if !transaction.IsCancellationOnly(errors.Join(context.Canceled, context.DeadlineExceeded, &transaction.Error{Kind: transaction.Cancelled})) {
		t.Fatal("normal joined contexts refused")
	}
	for _, e := range []error{errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown}), errors.Join(context.DeadlineExceeded, errors.New("TEST_FAILURE")), errors.Join(errors.Join(context.Canceled, context.DeadlineExceeded), &transaction.Error{Kind: transaction.CommitRolledBack})} {
		if transaction.IsCancellationOnly(e) {
			t.Fatal("mixed actual failure treated as cancellation")
		}
	}
}

func TestRuntimeNormalCancellationAndReverseOwnerJoin(t *testing.T) {
	g := newRuntimeLifecycle(t.Context())
	events := make(chan string, 4)
	for _, name := range []string{"first", "second"} {
		if e := g.Start(func(context.Context) error { return nil }, func(context.Context) error { events <- name; return nil }); e != nil {
			t.Fatal(e)
		}
	}
	g.Loop("ORDINARY", func(ctx context.Context) { <-ctx.Done(); events <- "loop" })
	g.closers = append(g.closers, func() error { events <- "close"; return nil })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if e := g.Close(ctx); e != nil {
		t.Fatal(e)
	}
	var order []string
	for len(events) > 0 {
		order = append(order, <-events)
	}
	positions := map[string]int{}
	for i, v := range order {
		positions[v] = i
	}
	if len(order) != 4 || positions["second"] >= positions["first"] || positions["close"] != 3 {
		t.Fatal("not reverse stop/join/close", order)
	}
	select {
	case <-g.joined:
	default:
		t.Fatal("actual join not recorded")
	}
}

func TestRuntimeTimeoutRetainsLeaseUntilActualJoin(t *testing.T) {
	g := newRuntimeLifecycle(t.Context())
	release := make(chan struct{})
	started := make(chan struct{})
	var closed atomic.Bool
	g.Loop("SLOW", func(ctx context.Context) { close(started); <-ctx.Done(); <-release })
	<-started
	g.closers = append(g.closers, func() error { closed.Store(true); return nil })
	short, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	if e := g.Close(short); e == nil || e.Error() != "CONTROL_SHUTDOWN_TIMEOUT" {
		t.Fatal("slow join reported complete", e)
	}
	cancel()
	d := &selectedDatabase{workers: g.joined, cleanup: 1}
	if e := d.Close(); e == nil || e.Error() != "CONTROL_WORKERS_NOT_JOINED" || closed.Load() {
		t.Fatal("lease/store released before join", e)
	}
	close(release)
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if e := g.Close(ctx); e != nil || !closed.Load() {
		t.Fatal("completed join unavailable", e)
	}
	if e := d.Close(); e != nil {
		t.Fatal("confirmed owner retained unnecessarily", e)
	}
}

func TestRuntimeUnconfirmedOwnerStopNeverClaimsJoin(t *testing.T) {
	g := newRuntimeLifecycle(t.Context())
	var closed atomic.Bool
	g.stops = append(g.stops, func(context.Context) error { return errors.New("TEST_OWNER_STOP_FAILED") })
	g.closers = append(g.closers, func() error { closed.Store(true); return nil })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if e := g.Close(ctx); e == nil || closed.Load() {
		t.Fatal("failed stop reported cleanup", e)
	}
	select {
	case <-g.joined:
		t.Fatal("failed stop reported joined")
	default:
	}
}
