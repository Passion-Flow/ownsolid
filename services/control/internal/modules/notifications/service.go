// Package notifications owns recipient-private durable internal task notices.
package notifications

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type Service struct {
	DB                *pgxpool.Pool
	Identity          *identity.Service
	mu                sync.Mutex
	cancel            context.CancelFunc
	lifecycle         context.Context
	done              chan struct{}
	closing           bool
	maintenanceFailed bool
}
type Notification struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	WorkflowID  string     `json:"workflow_id"`
	RunID       string     `json:"run_id"`
	Outcome     string     `json:"outcome"`
	ErrorCode   *string    `json:"error_code"`
	OccurredAt  time.Time  `json:"occurred_at"`
	ReadAt      *time.Time `json:"read_at"`
}
type Page struct {
	Items      []Notification `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}
type Configuration struct {
	Available     bool  `json:"available"`
	UnreadCount   int64 `json:"unread_count"`
	RetentionDays int   `json:"retention_days"`
}

func failure(code string) error                { return errors.New(code) }
func (s *Service) policy() config.PortalPolicy { return s.Identity.Config.Policy().Portal }
func (s *Service) Available() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil && !s.closing && s.lifecycle.Err() == nil && !s.maintenanceFailed
}
func (s *Service) Readiness() error {
	if !s.Available() {
		return failure("NOTIFICATION_UNAVAILABLE")
	}
	return nil
}
