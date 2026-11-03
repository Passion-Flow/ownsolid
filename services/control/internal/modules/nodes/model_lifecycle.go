package nodes

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type PreparationAuthority interface {
	ClaimPreparation(context.Context, string, string, string) (wire.Claim, bool, error)
	RenewPreparation(context.Context, string, string, string, string) (wire.RenewedLease, bool, error)
	FencePreparationNode(context.Context, transaction.Executor, Node) error
}

// ModelPreparationTargetInTransaction resolves only a currently scoped explicitly enrolled lifecycle node.
// The caller owns current human/Workspace locks; this lookup does not grant execution or reserve a GPU.
func (s *Service) ModelPreparationTargetInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id string) (Node, error) {
	return s.ModelPreparationTargetWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id)
}

// PreparationHistoryInTransaction resolves retained scope only, including removed nodes; no execution authority is granted.
func (s *Service) PreparationHistoryInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id string) (Node, error) {
	return s.PreparationHistoryWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id)
}

func (s *Service) readPreparationTask(ctx context.Context, tx pgx.Tx, n Node, id string, active bool) (Task, lifecycle.Command, error) {
	var command lifecycle.Command
	if active && n.Status != "active" || n.ExecutionProfile != wire.DockerModelLifecycleProfile || n.ProtocolVersion != wire.ModelLifecycleVersion || !slices.Contains(n.Capabilities, wire.ModelPreparationRunOperation) {
		return Task{}, command, fail("AGENT_PROFILE_REJECTED")
	}
	t, e := scanTask(tx.QueryRow(ctx, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE", n.TenantID, n.WorkspaceID, n.ID, id))
	if e != nil {
		return t, command, e
	}
	if t.Operation != wire.ModelPreparationRunOperation {
		return t, command, fail("AGENT_PROFILE_REJECTED")
	}
	var raw []byte
	if e = tx.QueryRow(ctx, "SELECT payload FROM node_tasks WHERE id=$1", id).Scan(&raw); e != nil {
		return t, command, e
	}
	command, e = lifecycle.DecodeCommand(raw)
	if e != nil {
		return t, command, e
	}
	if command.Scope.TenantID != n.TenantID || command.Scope.WorkspaceID != n.WorkspaceID {
		return t, command, fail("MODEL_PREPARATION_COMMAND_INVALID")
	}
	return t, command, nil
}

func (s *Service) PreparationStopTaskInTransaction(ctx context.Context, tx pgx.Tx, n Node, id string) (Task, lifecycle.Command, error) {
	return s.PreparationStopTaskWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id)
}

func (s *Service) QueuePreparationInTransaction(ctx context.Context, tx pgx.Tx, n Node, command lifecycle.Command, actor string, deadline time.Time) (Task, error) {
	return s.QueuePreparationWithin(ctx, transaction.PostgreSQLTransaction(tx), n, command, actor, deadline)
}

func (s *Service) ClaimPreparationInTransaction(ctx context.Context, tx pgx.Tx, n Node, id, request string) (wire.Claim, error) {
	return s.ClaimPreparationWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id, request)
}

func (s *Service) LeasedPreparationInTransaction(ctx context.Context, tx pgx.Tx, n Node, id, token string) (Task, lifecycle.Command, error) {
	return s.LeasedPreparationWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id, token)
}

func (s *Service) RenewPreparationInTransaction(ctx context.Context, tx pgx.Tx, n Node, id, token string) (wire.RenewedLease, lifecycle.Command, error) {
	return s.RenewPreparationWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id, token)
}
