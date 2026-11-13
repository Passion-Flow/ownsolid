package models

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func modelSQL(q transaction.Executor, postgres, mysql string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, mysql, args...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, postgres, args...)
	}
	return transaction.SQL(q.Dialect(), "")
}

func modelReadError(e error) error {
	if e == nil {
		return nil
	}
	switch e.Error() {
	case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED",
		"INVALID_CURSOR", "MODEL_HISTORY_UNAVAILABLE", "MODEL_HISTORY_INVALID", "MODEL_FAMILY_INVALID", "MODEL_FAMILY_CONFLICT",
		"MODEL_REFERENCE_CHECK_UNAVAILABLE", "MODEL_VERSION_REFERENCED", "MODEL_PREPARATION_UNAVAILABLE",
		"MODEL_FILE_INVALID", "MODEL_DERIVED_CATALOG_INVALID", "STORAGE_UNAVAILABLE",
		string(transaction.Cancelled), string(transaction.Timeout), string(transaction.LockTimeout),
		string(transaction.Deadlock), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(e.Error())
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}

func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, fail("MODEL_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("MODEL_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}

// The DTO remains private until the real transaction and its authority fences
// complete. An unknown commit outcome must not publish a projection.
func (s *Service) readActor(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || a.Me.Audience != "portal" && !admin(a) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		return fn(ctx, q)
	})
	return modelReadError(e)
}
