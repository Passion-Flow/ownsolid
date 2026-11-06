package preferences

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, failure("PREFERENCE_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB {
		return nil, failure("PREFERENCE_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func preferenceSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func noRows(e error) bool { return errors.Is(e, sql.ErrNoRows) }

func preferenceError(e error) error {
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
	for _, prefix := range []string{"PREFERENCE_", "AUTH_", "IDENTITY_", "SESSION_", "SSO_", "STORAGE_"} {
		if strings.HasPrefix(code, prefix) {
			return failure(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "AUTHENTICATION_REQUIRED", "VALIDATION_FAILED", "RESOURCE_NOT_FOUND", "RESOURCE_LIMIT_REACHED", "SAVED_VIEW_NAME_EXISTS":
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}
func installedTenant(ctx context.Context, q transaction.Executor) (string, error) {
	var tenant string
	e := q.QueryRow(ctx, preferenceSQL(q, `SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR UPDATE OF i.tenant_id,t.id`)).Scan(&tenant)
	if errors.Is(e, sql.ErrNoRows) {
		return "", failure("ACCESS_DENIED")
	}
	return tenant, e
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor, string) error) error {
	if ctx == nil || a.Me.Audience != "portal" && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return failure("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return preferenceError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Portal.CommitSeconds))
	defer cancel()
	return preferenceError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		tenant, e := installedTenant(ctx, q)
		if e != nil {
			return e
		}
		return fn(ctx, q, tenant)
	}))
}
