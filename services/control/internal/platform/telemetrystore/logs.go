package telemetrystore

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Settings size the log store connection. config.TelemetryConfiguration validates them.
type Settings struct {
	LogsAddress    string
	RequestTimeout time.Duration
	QueueCapacity  int
}

// Logs is the log store as the control plane uses it: writes through the queue and
// reads straight from the store.
type Logs struct {
	*Client
	*Writer
}

// Open returns the log store. Run must be started for writes to reach it.
func Open(s Settings, logger *slog.Logger) (*Logs, error) {
	client, e := NewClient(s.LogsAddress, s.RequestTimeout)
	if e != nil {
		return nil, e
	}
	if s.QueueCapacity < 1 {
		return nil, fmt.Errorf("logs queue capacity %d must be positive", s.QueueCapacity)
	}
	return &Logs{Client: client, Writer: NewWriter(client, s.QueueCapacity, logger)}, nil
}

// Run delivers queued records until ctx ends.
func (l *Logs) Run(ctx context.Context) error {
	l.Writer.Run(ctx)
	return nil
}
