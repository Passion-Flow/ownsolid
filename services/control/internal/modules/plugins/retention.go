package plugins

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func (s *Service) Start(ctx context.Context) error {
	if s == nil || s.Identity == nil {
		return failure("PLUGIN_IDENTITY_REQUIRED")
	}
	if ctx == nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return pluginError(e)
	}
	if !s.Identity.Config.Plugins.Enabled {
		return nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stop != nil {
		return failure("PLUGIN_ALREADY_STARTED")
	}
	if s.policy().Validate() != nil || s.Identity.Config.Plugins.Validate() != nil {
		return failure("PLUGIN_UNAVAILABLE")
	}
	worker, stop := context.WithCancel(ctx)
	s.stop = stop
	s.lifecycleContext = worker
	s.closing = false
	s.joined = make(chan struct{})
	joined := s.joined
	s.maintenanceError = false
	go func() {
		defer close(joined)
		ticker := time.NewTicker(config.Seconds(s.policy().MaintenanceSeconds))
		defer ticker.Stop()
		for {
			select {
			case <-worker.Done():
				return
			case <-ticker.C:
				operation, cancel := context.WithTimeout(worker, config.Seconds(s.policy().CommitSeconds))
				_, e := s.Cleanup(operation)
				cancel()
				s.lifecycleMu.Lock()
				s.maintenanceError = e != nil && worker.Err() == nil
				s.lifecycleMu.Unlock()
			}
		}
	}()
	return nil
}
func (s *Service) Stop(ctx context.Context) error {
	s.lifecycleMu.Lock()
	if s.stop == nil {
		s.lifecycleMu.Unlock()
		return nil
	}
	stop, joined := s.stop, s.joined
	s.closing = true
	stop()
	s.lifecycleMu.Unlock()
	select {
	case <-ctx.Done():
		return failure("PLUGIN_SHUTDOWN_TIMEOUT")
	case <-joined:
		s.lifecycleMu.Lock()
		if s.joined == joined {
			s.stop = nil
		}
		s.lifecycleMu.Unlock()
		return nil
	}
}
func (s *Service) VerifyStored(ctx context.Context) error {
	if _, e := s.repository(); e != nil {
		return pluginError(e)
	}
	if !s.Identity.Config.Plugins.Enabled {
		return nil
	}
	s.lifecycleMu.Lock()
	unavailable := !s.availableLocked()
	s.lifecycleMu.Unlock()
	if unavailable {
		return failure("PLUGIN_UNAVAILABLE")
	}
	return s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var ready bool
		if e := q.QueryRow(ctx, pluginSQL(q, "SELECT EXISTS(SELECT 1 FROM installation_state WHERE id=1 AND bootstrap_complete)", "SELECT EXISTS(SELECT 1 FROM installation_state WHERE id=1 AND bootstrap_complete=1)", nativePluginBootstrapReady)).Scan(&ready); e != nil {
			return e
		}
		if !ready {
			return failure("PLUGIN_UNAVAILABLE")
		}
		// Presence is checked against actual product relations. Removed trust keys do
		// not make the whole platform unready: every execution verifies current trust.
		var present bool
		return q.QueryRow(ctx, pluginSQL(q, `SELECT EXISTS(SELECT 1 FROM plugin_versions),EXISTS(SELECT 1 FROM plugin_grants),EXISTS(SELECT 1 FROM plugin_mutation_intents),EXISTS(SELECT 1 FROM workflow_plugin_references)`, `SELECT EXISTS(SELECT 1 FROM plugin_versions),EXISTS(SELECT 1 FROM plugin_grants),EXISTS(SELECT 1 FROM plugin_mutation_intents),EXISTS(SELECT 1 FROM workflow_plugin_references)`, nativePluginRelationsPresent)).Scan(&present, &present, &present, &present)
	})
}

func (s *Service) availableLocked() bool {
	return s != nil && s.Identity != nil && s.Identity.Config.Plugins.Enabled && s.stop != nil && !s.closing && s.lifecycleContext != nil && s.lifecycleContext.Err() == nil && !s.maintenanceError
}
func (s *Service) available() bool {
	if s == nil {
		return false
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.availableLocked()
}
