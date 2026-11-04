package components_test

import (
	"context"
	"slices"
	"sync"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// memoryLogStore behaves like the real store: it keeps whatever was enqueued, including
// records of settlements that later rolled back, and returns them newest first.
type memoryLogStore struct {
	mu      sync.Mutex
	records []telemetrystore.Record
	failure bool
}

func (m *memoryLogStore) Enqueue(r telemetrystore.Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r)
}

func (m *memoryLogStore) Recent(_ context.Context, s telemetrystore.Scope, limit int) ([]telemetrystore.Record, error) {
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

func (m *memoryLogStore) failing(value bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failure = value
}
