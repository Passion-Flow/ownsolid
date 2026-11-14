package nodes

import (
	"context"

	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// TrainingAuthority preserves current human delegation before a machine acquires or renews training work.
type TrainingAuthority interface {
	ClaimTraining(context.Context, string, string, string) (wire.Claim, bool, error)
	RenewTraining(context.Context, string, string, string, string) (wire.RenewedLease, bool, error)
	FenceTrainingNode(context.Context, transaction.Executor, Node) error
}

// CredentialInTransaction requires the caller's current Workspace lock before the node lock.
func (s *Service) CredentialInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id, credential string) (Node, error) {
	return s.CredentialWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id, credential)
}

func (s *Service) trainingTask(ctx context.Context, tx pgx.Tx, n Node, id string) (Task, training.Command, error) {
	return s.readTrainingTask(ctx, tx, n, id, true)
}
func (s *Service) readTrainingTask(ctx context.Context, tx pgx.Tx, n Node, id string, requireActive bool) (Task, training.Command, error) {
	var command training.Command
	if requireActive && n.Status != "active" || !wire.IsTrainingProfile(n.ExecutionProfile) || n.ProtocolVersion != wire.ProfileVersion(n.ExecutionProfile) || !slices.Contains(n.Capabilities, wire.TrainingRunOperation) {
		return Task{}, command, fail("AGENT_PROFILE_REJECTED")
	}
	t, e := scanTask(tx.QueryRow(ctx, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE", n.TenantID, n.WorkspaceID, n.ID, id))
	if e != nil {
		return t, command, e
	}
	if t.Operation != wire.TrainingRunOperation {
		return t, command, fail("AGENT_PROFILE_REJECTED")
	}
	var raw []byte
	if e = tx.QueryRow(ctx, "SELECT payload FROM node_tasks WHERE id=$1", id).Scan(&raw); e != nil {
		return t, command, e
	}
	command, e = training.DecodeCommand(raw)
	if e != nil {
		return t, command, e
	}
	if command.Scope.TenantID != n.TenantID || command.Scope.WorkspaceID != n.WorkspaceID || command.ModelCatalogSHA256 != wire.ModelCatalogDigest() {
		return t, command, fail("TRAINING_COMMAND_INVALID")
	}
	return t, command, nil
}

// TrainingStopTaskInTransaction resolves only immutable stop scope; it does not grant execution or output authority.
// The caller must authenticate the node and independently verify the attempt's retained delivered stop-token hash.
func (s *Service) TrainingStopTaskInTransaction(ctx context.Context, tx pgx.Tx, n Node, id string) (Task, training.Command, error) {
	return s.TrainingStopTaskWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id)
}

// TrainingHistoryInTransaction is server-side inventory scope for cleanup, including disabled/removed historical nodes.
// It does not authenticate a machine or authorize execution.
func (s *Service) TrainingHistoryInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id string) (Node, error) {
	return s.TrainingHistoryWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id)
}

// ClaimTrainingInTransaction never reissues a delivered attempt after lease loss.
func (s *Service) ClaimTrainingInTransaction(ctx context.Context, tx pgx.Tx, n Node, id, request string) (wire.Claim, error) {
	return s.ClaimTrainingWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id, request)
}

// LeasedTrainingInTransaction binds the exact live machine lease without taking a second authority transaction.
func (s *Service) LeasedTrainingInTransaction(ctx context.Context, tx pgx.Tx, n Node, id, token string) (Task, training.Command, error) {
	return s.LeasedTrainingWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id, token)
}

func (s *Service) RenewTrainingInTransaction(ctx context.Context, tx pgx.Tx, n Node, id, token string) (wire.RenewedLease, training.Command, error) {
	return s.RenewTrainingWithin(ctx, transaction.PostgreSQLTransaction(tx), n, id, token)
}

// TrainingTargetInTransaction belongs to node inventory; callers already hold current Workspace authority.
func (s *Service) TrainingTargetInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id string) (Node, error) {
	return s.TrainingTargetWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id)
}

// QueueTrainingInTransaction commits a typed immutable task inside the caller's authority/resource transaction.
func (s *Service) QueueTrainingInTransaction(ctx context.Context, tx pgx.Tx, n Node, command training.Command, actor string, deadline time.Time) (Task, error) {
	return s.QueueTrainingWithin(ctx, transaction.PostgreSQLTransaction(tx), n, command, actor, deadline)
}
