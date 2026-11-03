package plugins

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func pluginError(e error) error {
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
	if len(code) > 120 {
		return failure("STORAGE_UNAVAILABLE")
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return failure("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"PLUGIN_", "AUTH_", "MODEL_", "STORAGE_"} {
		if strings.HasPrefix(code, prefix) {
			return failure(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "INVALID_CURSOR", "RESOURCE_NOT_FOUND":
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, failure("PLUGIN_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, failure("PLUGIN_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func pluginSQL(q transaction.Executor, pg, my string, native pluginNativeQuery, args ...any) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return native.statement(args...)
	}
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func pluginStatement(q transaction.Executor, pg, my string, native pluginNativeQuery, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return native.statement(myArgs...)
	}
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func (s *Service) executor(ctx context.Context, q transaction.Executor) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if ctx == nil || q == nil || store.Dialect() != q.Dialect() {
		return failure("PLUGIN_STORAGE_MISMATCH")
	}
	if _, pool := q.(transaction.Store); pool {
		return failure("PLUGIN_TRANSACTION_REQUIRED")
	}
	if e = ctx.Err(); e != nil {
		return pluginError(e)
	}
	if s.policy().Validate() != nil {
		return failure("PLUGIN_UNAVAILABLE")
	}
	return nil
}
func (s *Service) storageTransaction(ctx context.Context, fn func(context.Context, transaction.Executor) error) error {
	store, e := s.repository()
	if e != nil {
		return pluginError(e)
	}
	if ctx == nil || fn == nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	if s.policy().Validate() != nil {
		return failure("PLUGIN_UNAVAILABLE")
	}
	operation, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	return pluginError(store.Within(operation, false, fn))
}
func (s *Service) adminTransaction(ctx context.Context, a identity.Auth, enabled bool, fn func(context.Context, transaction.Executor, string) error) error {
	if !admin(a) {
		return failure("ACCESS_DENIED")
	}
	if _, e := s.repository(); e != nil {
		return pluginError(e)
	}
	if enabled && !s.Identity.Config.Plugins.Enabled {
		return failure("PLUGIN_UNAVAILABLE")
	}
	return s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		var tenant string
		e := q.QueryRow(ctx, pluginSQL(q, `SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR UPDATE OF i`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR UPDATE`, nativePluginInstallation)).Scan(&tenant)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("ACCESS_DENIED")
		}
		if e != nil {
			return e
		}
		return fn(ctx, q, tenant)
	})
}

const mysqlVersionFields = `id,tenant_id,plugin_id,release_version,abi,executor,capsule_sha256,manifest_sha256,key_id,state,revision,imported_by,created_at,installed_at,uninstalled_at,capsule,manifest`
const mysqlGrantFields = `g.id,g.workspace_id,g.plugin_version_id,g.enabled,g.revision,g.granted_by,g.created_at,g.updated_at,g.authority_id`

func pluginExecOne(ctx context.Context, q transaction.Executor, statement transaction.Statement) error {
	n, e := q.Exec(ctx, statement)
	if e != nil {
		return e
	}
	if n != 1 {
		return failure("STORAGE_UNAVAILABLE")
	}
	return nil
}
