package workflows

import (
	"context"
	"errors"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, failure("WORKFLOW_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces == nil || s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, failure("WORKFLOW_STORAGE_MISMATCH")
	}
	for _, same := range []bool{s.Knowledge == nil || s.Knowledge.Identity == s.Identity, s.Distribution == nil || s.Distribution.Identity == s.Identity, s.Plugins == nil || s.Plugins.Identity == s.Identity} {
		if !same {
			return nil, failure("WORKFLOW_STORAGE_MISMATCH")
		}
	}
	return s.Identity.Repository()
}
func workflowRepositoryError(e error) error {
	if e == nil {
		return nil
	}
	// An uncertain COMMIT must survive a simultaneous cancellation or shutdown.
	// Check the bounded graph before errors.Is/As can follow malformed wrappers.
	if kind, ok := transaction.CommitOutcome(e); ok {
		if direct, ok := e.(*transaction.Error); ok && direct.Kind == kind {
			return direct
		}
		return &transaction.Error{Kind: kind}
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.Canceled) {
			return &transaction.Error{Kind: transaction.Cancelled}
		}
		return &transaction.Error{Kind: transaction.Timeout}
	}
	var safe *transaction.Error
	if errors.As(e, &safe) && safe.Kind != transaction.Cancelled && safe.Kind != transaction.Timeout {
		return safe
	}
	code := e.Error()
	if len(code) == 0 || len(code) > 120 {
		return failure("STORAGE_UNAVAILABLE")
	}
	for _, letter := range code {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '_') {
			return failure("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"AUTH_", "WORKFLOW_", "KNOWLEDGE_", "MODEL_", "PLUGIN_", "SSO_", "STORAGE_", "SESSION_", "CURSOR_", "PAGINATION_"} {
		if strings.HasPrefix(code, prefix) {
			return failure(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "AUTHENTICATION_REQUIRED", "VALIDATION_FAILED", "RESOURCE_NOT_FOUND", "IDEMPOTENCY_CONFLICT", "INVALID_CURSOR":
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}
func (s *Service) scopeWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace string, write bool) (workspaces.Workspace, error) {
	if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return workspaces.Workspace{}, e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
	if e != nil {
		return workspaces.Workspace{}, e
	}
	if w.Status != "active" || write && !editable(a, w) {
		return workspaces.Workspace{}, failure("ACCESS_DENIED")
	}
	return w, nil
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !wire.ValidID(workspace) || a.Me.Audience != "portal" && !admin(a) {
		return failure("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return workflowRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.scopeWithin(ctx, q, a, workspace, write)
		if e != nil {
			return e
		}
		return fn(ctx, q, w)
	})
	return workflowRepositoryError(e)
}

// Name collisions are classified from the exact owned key while holding the
// Workspace lock, never from a vendor message or an unrelated unique constraint.
func workflowNameAvailable(ctx context.Context, q transaction.Executor, w workspaces.Workspace, key, id string) error {
	var exists bool
	if e := q.QueryRow(ctx, workflowStatement(q, workflowNameExists, w.TenantID, w.ID, key, id)).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return failure("WORKFLOW_NAME_EXISTS")
	}
	return nil
}

func (s *Service) maintenanceWithin(ctx context.Context, fn func(context.Context, transaction.Executor) error) error {
	store, e := s.repository()
	if e != nil {
		return workflowRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	return workflowRepositoryError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error { return fn(ctx, q) }))
}

func workflowExecOne(ctx context.Context, q transaction.Executor, statement transaction.Statement) error {
	n, e := q.Exec(ctx, statement)
	if e != nil {
		return e
	}
	if n != 1 {
		return failure("STORAGE_UNAVAILABLE")
	}
	return nil
}
