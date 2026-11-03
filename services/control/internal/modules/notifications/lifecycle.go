package notifications

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"time"
)

func (s *Service) Start(ctx context.Context) error {
	if ctx == nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	if _, e := s.repository(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return failure("NOTIFICATION_ALREADY_STARTED")
	}
	if s.policy().Validate() != nil {
		return failure("NOTIFICATION_UNAVAILABLE")
	}
	operation, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	_, e := s.Cleanup(operation)
	cancel()
	if e != nil {
		return e
	}
	s.lifecycle, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	s.closing = false
	s.maintenanceFailed = false
	done := s.done
	worker := s.lifecycle
	go func() {
		defer close(done)
		ticker := time.NewTicker(config.Seconds(s.policy().NotificationMaintenanceSeconds))
		defer ticker.Stop()
		for {
			select {
			case <-worker.Done():
				return
			case <-ticker.C:
				operation, cancel := context.WithTimeout(worker, config.Seconds(s.policy().CommitSeconds))
				_, e := s.Cleanup(operation)
				cancel()
				s.mu.Lock()
				s.maintenanceFailed = e != nil && worker.Err() == nil
				s.mu.Unlock()
			}
		}
	}()
	return nil
}
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return nil
	}
	if !s.closing {
		s.closing = true
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return failure("NOTIFICATION_SHUTDOWN_TIMEOUT")
	case <-done:
	}
	s.mu.Lock()
	if s.done == done {
		s.cancel = nil
		s.lifecycle = nil
		s.closing = false
	}
	s.mu.Unlock()
	return nil
}
