package workflows

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Kept for the original PG authority fixture that supplies a real pgx callback.
// Production workers call withExecutionWithin. MySQL cannot enter this wrapper.
func (s *Service) withExecution(ctx context.Context, execution *runExecution, pending bool, fn func(pgx.Tx, workspaces.Workspace, workflowRecord, runRecord) error) error {
	store, e := s.repository()
	if e != nil {
		return workflowRepositoryError(e)
	}
	if store.Dialect() != transaction.PostgreSQL || s.Identity.DB == nil {
		return failure("WORKFLOW_STORAGE_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	tx, e := s.Identity.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	q := transaction.PostgreSQLTransaction(tx)
	w, e := s.scopeWithin(ctx, q, execution.actor, execution.workspace, false)
	if e != nil {
		return workflowRepositoryError(e)
	}
	e = s.executionAuthorityWithin(ctx, q, w, execution, pending, func(ctx context.Context, _ transaction.Executor, w workspaces.Workspace, b workflowRecord, r runRecord) error {
		return fn(tx, w, b, r)
	})
	if e != nil {
		return workflowRepositoryError(e)
	}
	if e = tx.Commit(ctx); errors.Is(e, pgx.ErrTxCommitRollback) {
		return &transaction.Error{Kind: transaction.CommitRolledBack}
	} else if e != nil {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}
