// Package logstorefixture provides an in-memory log store for tests.
package logstorefixture

import (
	"context"
	"slices"
	"sync"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// Memory behaves like the real store: it keeps whatever was enqueued, including records
// of settlements that later rolled back, and returns them newest first.
type Memory struct {
	mu      sync.Mutex
	records []telemetrystore.Record
	failure bool
}

// Enqueue keeps the record.
func (m *Memory) Enqueue(r telemetrystore.Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r)
}

// Recent returns up to limit records in scope, newest first, or ErrUnavailable while the
// store is set to fail.
func (m *Memory) Recent(_ context.Context, s telemetrystore.Scope, limit int) ([]telemetrystore.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure {
		return nil, telemetrystore.ErrUnavailable
	}
	var out []telemetrystore.Record
	for _, r := range slices.Backward(m.records) {
		if r.Scope == s && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

// SetFailing makes reads fail or succeed, like a store that goes down and returns.
func (m *Memory) SetFailing(value bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failure = value
}
