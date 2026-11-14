package taskbus

import (
	"context"
	"errors"
	"time"
)

type Inspection struct {
	Identity                 string
	Messages, Bytes, Pending uint64
	AckPending               int
}

// Inspect is exact bound-resource readback for operators and actual fixtures. It
// neither lists unrelated streams nor provisions, drains, deletes or ACKs data.
func (b *JetStream) Inspect(ctx context.Context) (Inspection, error) {
	if b == nil || ctx == nil {
		return Inspection{}, errors.New("TASK_BUS_CLOSED")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, time.Duration(b.configuration.RequestSeconds)*time.Second)
	defer cancel()
	if err := b.verify(bounded); err != nil {
		return Inspection{}, err
	}
	stream, err := b.stream.Info(bounded)
	if err != nil {
		return Inspection{}, safeTransport(err)
	}
	consumer, err := b.consumer.Info(bounded)
	if err != nil {
		return Inspection{}, safeTransport(err)
	}
	identity, err := admittedProfile(b.configuration, b.tenant, stream, consumer)
	if err != nil {
		return Inspection{}, err
	}
	if identity != b.identity || consumer.NumAckPending < 0 {
		return Inspection{}, errors.New("TASK_BUS_IDENTITY_CHANGED")
	}
	if bounded.Err() != nil {
		return Inspection{}, safeTransport(bounded.Err())
	}
	return Inspection{Identity: identity, Messages: stream.State.Msgs, Bytes: stream.State.Bytes, Pending: consumer.NumPending, AckPending: consumer.NumAckPending}, nil
}
