package taskbus

import (
	"context"
	"errors"
	"time"
)

// Reconnect reconstructs real connections without replaying a publication. The
// application retains all uncertain SQL intent and separately resolves readback.
// Persistent stream and consumer creation identity must survive broker restart.
func (b *JetStream) Reconnect(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("TASK_BUS_CLOSED")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.publisher == nil || b.subscriber == nil {
		return errors.New("TASK_BUS_CLOSED")
	}
	if b.publisher.IsConnected() && b.subscriber.IsConnected() {
		bounded, cancel := context.WithTimeout(ctx, time.Duration(b.configuration.RequestSeconds)*time.Second)
		defer cancel()
		return b.verify(bounded)
	}
	b.publisher.Close()
	b.subscriber.Close()
	// Four connection attempts include the two explicit negative authentication
	// probes; the remaining stage is bounded broker profile admission.
	bounded, cancel := context.WithTimeout(ctx, time.Duration(4*b.configuration.ConnectSeconds+b.configuration.RequestSeconds)*time.Second)
	defer cancel()
	fresh, err := NewJetStream(bounded, b.configuration, b.tenant)
	if err != nil {
		return err
	}
	if fresh.identity != b.identity || fresh.publisherKey != b.publisherKey || fresh.consumerKey != b.consumerKey {
		_ = fresh.Close()
		return errors.New("TASK_BUS_IDENTITY_CHANGED")
	}
	b.publisher, b.subscriber = fresh.publisher, fresh.subscriber
	b.api, b.stream, b.consumer = fresh.api, fresh.stream, fresh.consumer
	return nil
}
