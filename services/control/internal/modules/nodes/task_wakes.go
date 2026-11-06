package nodes

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/outbox"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// Task wakes through the outbox (ARC-011, ADR 0028). Writing a task appends an event in the same
// transaction, and every gateway reads the outbox and wakes the claims it holds for those nodes. A
// wake is a hint: a claim looks at the task table and also returns at the end of its wait, so a late
// or lost wake costs that wait. MySQL and Dameng wake on this process only until CMP-201 and CMP-202.

const (
	// topicTaskWritten says a task for the node named by the key was written.
	topicTaskWritten = "node.task_written"
	// wakeBatch bounds the events of one read.
	wakeBatch = 1000
)

type wakeCounters struct {
	events, failures atomic.Uint64
	lagNanoseconds   atomic.Int64
}

// taskWrittenWithin announces a task in its transaction. Without a running reader, as on other
// chains, it also wakes claims on this process at once, which is before the commit.
func (s *Service) taskWrittenWithin(ctx context.Context, q transaction.Executor, node string) error {
	if q.Dialect() == transaction.PostgreSQL {
		if e := outbox.AppendWithin(ctx, q, topicTaskWritten, node); e != nil {
			return e
		}
	}
	if !s.delivering.Load() {
		s.wake.wake(node)
	}
	return nil
}

// wakeSettle is how long a woken claim waits before it looks again.
func (s *Service) wakeSettle() time.Duration {
	if s.delivering.Load() {
		return 0
	}
	return claimSettle
}

// RunWakes reads the outbox and wakes claims on this process until ctx ends. The server runs it on
// PostgreSQL only. A read that fails keeps its position, and claims meanwhile end at their wait.
func (s *Service) RunWakes(ctx context.Context) {
	store, e := s.repository()
	if e != nil {
		slog.Error("node_task_wakes_unavailable", "code", e.Error())
		return
	}
	policy := s.Identity.Config.Policy().Nodes
	reader, e := outbox.NewReader(store, wakeBatch)
	if e != nil {
		slog.Error("node_task_wakes_unavailable", "code", e.Error())
		return
	}
	timeout := config.Seconds(policy.ReconcileTimeoutSeconds)
	ticker := time.NewTicker(time.Duration(policy.OutboxPollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	defer s.delivering.Store(false)
	for {
		bounded, cancel := context.WithTimeout(ctx, timeout)
		e := reader.Start(bounded)
		if e == nil {
			s.delivering.Store(true)
			e = s.deliverWakes(bounded, reader)
		}
		cancel()
		if e != nil && ctx.Err() == nil {
			s.wakeCounters.failures.Add(1)
			slog.Warn("node_task_wakes_read_failed", "code", "STORAGE_UNAVAILABLE")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// deliverWakes reads until the outbox has nothing more for this pass.
func (s *Service) deliverWakes(ctx context.Context, reader *outbox.Reader) error {
	for {
		events, e := reader.Next(ctx)
		if e != nil {
			return e
		}
		for _, v := range events {
			if v.Topic == topicTaskWritten {
				s.wake.wake(v.Key)
			}
		}
		s.wakeCounters.events.Add(uint64(len(events)))
		s.wakeCounters.lagNanoseconds.Store(int64(reader.Lag()))
		if len(events) < wakeBatch {
			return nil
		}
	}
}

// trimWakes removes events old enough that no gateway still needs them.
func (s *Service) trimWakes(ctx context.Context) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return nil
	}
	retention := config.Seconds(s.Identity.Config.Policy().Nodes.OutboxRetentionSeconds)
	return store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := outbox.Trim(ctx, q, retention)
		return e
	})
}

// WakeSamples reports the outbox reader for the metrics store.
func (s *Service) WakeSamples() []telemetrystore.Sample {
	c := &s.wakeCounters
	return []telemetrystore.Sample{
		{Name: "ownsolid_node_task_wake_events_total", Value: float64(c.events.Load())},
		{Name: "ownsolid_node_task_wake_read_failures_total", Value: float64(c.failures.Load())},
		{Name: "ownsolid_node_task_wake_lag_seconds", Value: time.Duration(c.lagNanoseconds.Load()).Seconds()},
	}
}
