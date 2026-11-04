package telemetrystore

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Writer retry and shutdown bounds.
const (
	writeAttempts    = 3
	retryBaseDelay   = 200 * time.Millisecond
	shutdownDrainFor = 5 * time.Second
)

// inserter is the part of Client the writer needs.
type inserter interface {
	Insert(context.Context, Record) error
}

// Writer delivers records in the background through a bounded queue.
type Writer struct {
	store   inserter
	queue   chan Record
	logger  *slog.Logger
	dropped atomic.Uint64
	failed  atomic.Uint64
	done    chan struct{}
	delay   time.Duration
}

// NewWriter returns a writer that holds at most capacity waiting records.
func NewWriter(store inserter, capacity int, logger *slog.Logger) *Writer {
	return &Writer{store: store, queue: make(chan Record, capacity), logger: logger, done: make(chan struct{}), delay: retryBaseDelay}
}

// Enqueue never blocks, so it is safe inside a SQL transaction. A record queued in a
// transaction that later rolls back is still written, so readers must confirm a record
// against the system of record before showing it.
func (w *Writer) Enqueue(r Record) {
	select {
	case w.queue <- r:
	default:
		if dropped := w.dropped.Add(1); dropped == 1 || dropped%1000 == 0 {
			w.logger.Warn("telemetry_queue_full", "dropped_total", dropped, "capacity", cap(w.queue))
		}
	}
}

// Dropped counts records refused by a full queue.
func (w *Writer) Dropped() uint64 { return w.dropped.Load() }

// Failed counts records given up on after every attempt failed.
func (w *Writer) Failed() uint64 { return w.failed.Load() }

// Run delivers records until ctx ends, then spends a bounded time on what is queued.
func (w *Writer) Run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case r := <-w.queue:
			// Shutdown must not fail a record it happens to pick up: each attempt is
			// bounded by the client timeout and the attempts are bounded too.
			w.deliver(context.WithoutCancel(ctx), r)
		case <-ctx.Done():
			w.drain()
			return
		}
	}
}

// Wait returns when Run has finished.
func (w *Writer) Wait() { <-w.done }

func (w *Writer) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownDrainFor)
	defer cancel()
	for {
		select {
		case r := <-w.queue:
			w.deliver(ctx, r)
		default:
			return
		}
		if ctx.Err() != nil {
			if left := len(w.queue); left > 0 {
				w.logger.Warn("telemetry_shutdown_dropped", "records", left)
			}
			return
		}
	}
}

func (w *Writer) deliver(ctx context.Context, r Record) {
	var e error
	for attempt := 0; attempt < writeAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(w.delay << (attempt - 1)):
			case <-ctx.Done():
			}
		}
		if e = w.store.Insert(ctx, r); e == nil {
			return
		}
		if ctx.Err() != nil {
			break
		}
	}
	w.failed.Add(1)
	w.logger.Warn("telemetry_write_failed", "component", r.Component, "node_id", r.NodeID, "error", e.Error())
}
