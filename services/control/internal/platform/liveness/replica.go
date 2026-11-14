package liveness

import (
	"context"
	"time"
)

// Seen is one node's last-seen time as gateways share it.
type Seen struct {
	Node string
	At   time.Time
}

// Replica is the small replicated store gateways share sightings through. A last-seen time
// only moves forward, so concurrent writers need no ordering: readers keep the largest.
type Replica interface {
	// Publish stores sightings. A failure must not stop the gateway: the database holds
	// what it needs, and the next pass publishes newer values.
	Publish(context.Context, []Seen) error
	// Watch calls fn with what is stored now and then with every change, until ctx ends or
	// the store fails. The caller decides whether to watch again.
	Watch(ctx context.Context, fn func(Seen)) error
}
