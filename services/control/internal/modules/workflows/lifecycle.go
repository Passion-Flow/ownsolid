package workflows

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

// Start owns bounded workflow work inside this control process. Existing remote
// or unexpired run owners are never adopted or reinvoked after a restart.
func (s *Service) Start(ctx context.Context) error {
	if s == nil || s.Identity == nil {
		return failure("WORKFLOW_IDENTITY_REQUIRED")
	}
	if ctx == nil {
		return failure("WORKFLOW_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return workflowRepositoryError(e)
	}
	if !s.Identity.Config.Workflows.Enabled {
		return nil
	}
	if s.Identity.Config.Knowledge.Enabled {
		if s.Knowledge == nil || !s.Knowledge.Processor.Available() || s.Distribution == nil {
			return failure("WORKFLOW_UNAVAILABLE")
		}
	} else if !s.toolOnly() || s.Plugins == nil {
		return failure("WORKFLOW_UNAVAILABLE")
	}
	if _, e := s.repository(); e != nil {
		return workflowRepositoryError(e)
	}
	if s.toolOnly() {
		// Plugin startup precedes workflow startup. Verify its selected SQL owner
		// and joined lifecycle before taking the workflow lifecycle mutex.
		if e := s.Plugins.VerifyStored(ctx); e != nil {
			return workflowRepositoryError(e)
		}
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started {
		return failure("WORKFLOW_ALREADY_STARTED")
	}
	if s.policy().Validate() != nil {
		return failure("WORKFLOW_UNAVAILABLE")
	}
	// Initial recovery is bounded and contains only terminal reductions. No model
	// call or automatic retry can occur in this maintenance path.
	startup, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	if e := s.RecoverExpired(startup); e != nil {
		return e
	}
	s.lifecycleContext, s.lifecycleCancel = context.WithCancelCause(ctx)
	s.processOwner = uuid.NewString()
	s.active = make(chan struct{}, s.policy().Concurrency)
	s.running = map[string]context.CancelCauseFunc{}
	s.closing = false
	s.maintenanceError = ""
	s.started = true
	s.publishConfigurationLocked()
	s.shutdownDone = nil
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		ticker := time.NewTicker(config.Seconds(s.policy().MaintenanceSeconds))
		defer ticker.Stop()
		for {
			select {
			case <-s.lifecycleContext.Done():
				return
			case <-ticker.C:
				operation, cancel := context.WithTimeout(s.lifecycleContext, config.Seconds(s.policy().CommitSeconds))
				e := s.RecoverExpired(operation)
				if e == nil {
					_, e = s.Cleanup(operation)
				}
				cancel()
				s.lifecycleMu.Lock()
				if e != nil && s.lifecycleContext.Err() == nil {
					s.maintenanceError = "WORKFLOW_MAINTENANCE_FAILED"
				} else if e == nil {
					s.maintenanceError = ""
				}
				s.publishConfigurationLocked()
				s.lifecycleMu.Unlock()
			}
		}
	}()
	return nil
}

// Stop cancels tracked jobs and waits for actual cleanup. A timeout is explicit;
// it never claims that a provider request stopped or that all workers joined.
func (s *Service) Stop(ctx context.Context) error {
	s.lifecycleMu.Lock()
	if !s.started {
		s.lifecycleMu.Unlock()
		return nil
	}
	if !s.closing {
		s.closing = true
		s.publishConfigurationLocked()
		s.lifecycleCancel(failure("WORKFLOW_RUN_INTERRUPTED"))
		for _, cancel := range s.running {
			cancel(failure("WORKFLOW_RUN_INTERRUPTED"))
		}
		done := make(chan struct{})
		s.shutdownDone = done
		go func() {
			s.workers.Wait()
			s.lifecycleMu.Lock()
			s.started = false
			s.publishConfigurationLocked()
			s.running = nil
			s.lifecycleMu.Unlock()
			close(done)
		}()
	}
	done := s.shutdownDone
	s.lifecycleMu.Unlock()
	select {
	case <-ctx.Done():
		return failure("WORKFLOW_SHUTDOWN_TIMEOUT")
	case <-done:
		return nil
	}
}

type expiredRun struct {
	id, tenant, workspace, workflow, owner, audience string
	conversation                                     *string
}

// RecoverExpired reads candidates without locks, then follows ordinary user ->
// Workspace -> workflow -> conversation -> run order and rechecks actual DB time.
// Only expired pending/running rows are reduced; no current owner is guessed.
func (s *Service) RecoverExpired(ctx context.Context) error {
	store, e := s.repository()
	if e != nil {
		return workflowRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	rows, e := store.Query(ctx, workflowStatement(store, workflowExpiredCandidates, s.policy().MaintenanceBatch))
	if e != nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	var candidates []expiredRun
	for rows.Next() {
		var r expiredRun
		if e = rows.Scan(&r.id, &r.tenant, &r.workspace, &r.workflow, &r.owner, &r.audience, &r.conversation); e != nil {
			rows.Close()
			return failure("STORAGE_UNAVAILABLE")
		}
		candidates = append(candidates, r)
	}
	rows.Close()
	if rows.Err() != nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	for _, candidate := range candidates {
		if e = s.interruptExpired(ctx, candidate); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) interruptExpired(ctx context.Context, c expiredRun) error {
	return s.maintenanceWithin(ctx, func(ctx context.Context, tx transaction.Executor) error {
		var lockedActor string

		if e := tx.QueryRow(ctx, workflowStatement(tx, workflowLockActor, c.owner)).Scan(&lockedActor); e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		var id string
		var e error
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLockWorkspace, c.tenant, c.workspace)).Scan(&id); e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLockIdentity, c.tenant, c.workspace, c.workflow)).Scan(&id); e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		if c.conversation != nil {
			if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLockExpiredConversation, c.tenant, c.workspace, c.workflow, c.owner, *c.conversation)).Scan(&id); e != nil {
				return failure("STORAGE_UNAVAILABLE")
			}
		}
		var eligible bool
		e = tx.QueryRow(ctx, workflowStatement(tx, workflowExpiredEligible, c.tenant, c.workspace, c.workflow, c.owner, c.id)).Scan(&eligible)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		if !eligible {
			return nil
		}
		if _, e = tx.Exec(ctx, workflowStatement(tx, workflowTraceInterrupt, c.id)); e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		if _, e = tx.Exec(ctx, workflowStatement(tx, workflowRunInterrupt, c.id)); e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		a := identity.Auth{Me: identity.Me{User: identity.User{ID: c.owner}, Audience: c.audience}}
		if e = s.audit(ctx, tx, a, workspaces.Workspace{ID: c.workspace, TenantID: c.tenant}, "workflow.run_interrupted", "workflow_run", c.id, "Expired durable run requires an explicit new request", uuid.NewString()); e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		return nil
	})
}

// VerifyStored exposes only readiness. Private execution and maintenance details
// remain in the operator-owned evidence and caller-authorized run interfaces.
func (s *Service) VerifyStored(ctx context.Context) error {
	if !s.Identity.Config.Workflows.Enabled {
		return nil
	}
	if e := ctx.Err(); e != nil {
		return failure("WORKFLOW_UNAVAILABLE")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.started || s.closing || s.lifecycleContext.Err() != nil {
		return failure("WORKFLOW_UNAVAILABLE")
	}
	if s.maintenanceError != "" {
		return failure(s.maintenanceError)
	}
	return nil
}
