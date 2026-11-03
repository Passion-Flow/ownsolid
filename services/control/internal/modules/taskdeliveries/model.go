// Package taskdeliveries persists transport events and current human visibility.
// Node tasks and their existing physical claim/lease remain authoritative.
package taskdeliveries

import (
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

type Service struct {
	Identity      *identity.Service
	Workspaces    *workspaces.Service
	Nodes         *nodes.Service
	Configuration taskbus.Configuration
	Transport     *taskbus.JetStream
	lifecycle     *lifecycle
}

type Delivery struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	NodeID          string    `json:"node_id"`
	TaskID          string    `json:"task_id"`
	Operation       string    `json:"operation"`
	TaskState       string    `json:"task_state"`
	State           string    `json:"state"`
	OutcomeUnknown  bool      `json:"outcome_unknown"`
	CanRetry        bool      `json:"can_retry"`
	Generation      int       `json:"generation"`
	PublishAttempts int       `json:"publish_attempts"`
	ConsumeAttempts int64     `json:"consume_attempts"`
	ErrorCode       *string   `json:"error_code"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type RetryReceipt struct {
	EventID     string    `json:"event_id"`
	DeliveryID  string    `json:"delivery_id"`
	Generation  int       `json:"generation"`
	RequestedAt time.Time `json:"requested_at"`
}

type storedDelivery struct {
	Delivery
	Event                          taskbus.Event
	Tenant, Namespace, DeliveryID  string
	ConsumerName                   string
	LogicalSHA256                  string
	ClaimID                        *string
	ClaimExpiresAt                 *time.Time
	BrokerIdentity, BrokerSequence *string
	NotBefore                      time.Time
}
