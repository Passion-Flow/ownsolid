package training

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// transferStatement selects explicit repository-owned SQL and argument order.
// It never rewrites a query or retries an uncertain transaction.
func transferStatement(q transaction.Executor, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
}
func transferAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, id, reason, request string) error {
	n, e := q.Exec(ctx, acceptedAdapterSQL(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','training_job',$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success','training_job',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, id, reason, request))
	if e == nil && n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

// Operator callers can have no Workspace service. The identity-owned selected
// repository is still mandatory; a stale PG pool may not override MySQL.
func (s *Service) trainingStorageTransaction(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	if s == nil || ctx == nil || s.Identity == nil {
		return fail("TRAINING_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB {
		return fail("TRAINING_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return trainingRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	return trainingRepositoryError(store.Within(ctx, read, fn))
}

// acceptedCheckpoint is the original PostgreSQL borrowed compatibility boundary.
func (s *Service) acceptedCheckpoint(ctx context.Context, tx pgx.Tx, w workspaces.Workspace, j Job, command wire.Command, id string) (acceptedCheckpoint, error) {
	return s.acceptedCheckpointWithin(ctx, transaction.PostgreSQLTransaction(tx), w, j, command, id)
}
