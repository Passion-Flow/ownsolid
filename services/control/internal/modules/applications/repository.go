package applications

import (
	"context"
	"errors"
	"strings"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, failure("APPLICATION_IDENTITY_REQUIRED")
	}
	if s.Workflows == nil {
		return nil, failure("WORKFLOW_UNAVAILABLE")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces == nil || s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB || s.Workflows.Identity != s.Identity || s.Workflows.Workspaces != s.Workspaces || s.Workflows.DB != nil && s.Workflows.DB != s.Identity.DB {
		return nil, failure("APPLICATION_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func applicationError(e error) error {
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
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return failure("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"APPLICATION_", "WORKFLOW_", "WORKSPACE_", "AUTH_", "IDENTITY_", "SESSION_", "SSO_", "STORAGE_", "CURSOR_", "PAGINATION_"} {
		if strings.HasPrefix(code, prefix) {
			return failure(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "AUTHENTICATION_REQUIRED", "VALIDATION_FAILED", "RESOURCE_NOT_FOUND", "INVALID_CURSOR":
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || a.Me.Audience != "portal" || !strict.ValidID(workspace) {
		return failure("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return applicationError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Portal.CommitSeconds))
	defer cancel()
	return applicationError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" || w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	}))
}
