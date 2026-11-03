package nodes

import (
	"context"
	"errors"
	"log/slog"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const taskFields = `id::text,tenant_id::text,workspace_id::text,node_id::text,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at`

func scanTask(row pgx.Row) (Task, error) {
	var v Task
	e := row.Scan(&v.ID, &v.TenantID, &v.WorkspaceID, &v.NodeID, &v.Operation, &v.State, &v.Attempt, &v.ErrorCode, &v.CreatedAt, &v.UpdatedAt, &v.DeadlineAt, &v.CompletedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Task{}, e
	}
	if e == nil {
		v.CreatedAt, v.UpdatedAt, v.DeadlineAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC(), v.DeadlineAt.UTC()
		if v.CompletedAt != nil {
			stamp := v.CompletedAt.UTC()
			v.CompletedAt = &stamp
		}
	}
	return v, e
}
func (s *Service) expireTasks(ctx context.Context, tx pgx.Tx, n Node, request string) error {
	return s.expireTasksWithin(ctx, transaction.PostgreSQLTransaction(tx), n, request)
}
func (s *Service) CreateTask(ctx context.Context, a identity.Auth, id, operation, key, request string) (Task, error) {
	return s.createTaskSelected(ctx, a, id, operation, key, request)
}
func (s *Service) Claim(ctx context.Context, id, credential, request string) (wire.Claim, error) {
	return s.claimSelected(ctx, id, credential, request)
}
func (s *Service) Complete(ctx context.Context, id, credential, taskID string, input wire.Result, request string) (string, error) {
	return s.completeSelected(ctx, id, credential, taskID, input, request)
}
func (s *Service) Sweep(ctx context.Context) error { return s.sweepSelected(ctx) }
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(config.Seconds(s.Identity.Config.Policy().Nodes.ReconcileSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Nodes.ReconcileTimeoutSeconds))
			if s.Sweep(bounded) != nil {
				slog.Warn("node_task_sweep_failed", "code", "STORAGE_UNAVAILABLE")
			}
			cancel()
		}
	}
}

// InvalidateAuthorities participates in the operator's single restore transaction.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}
