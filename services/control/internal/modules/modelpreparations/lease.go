package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// withMachineLease checks current human, Workspace, node credential, exact live attempt and shared GPU fence.
func (s *Service) withMachineLease(ctx context.Context, node, credential, task, token string, fn func(context.Context, transaction.Executor, workspaces.Workspace, Preparation, wire.Command) error) error {
	c, _, e := s.machineCandidate(ctx, node, task)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AGENT_LEASE_STALE")
	}
	if e != nil {
		return e
	}
	return s.delegatedTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Preparation, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || j.State != "preparing" && j.State != "running" || j.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		_, command, e := s.Nodes.LeasedPreparationWithin(ctx, tx, current, task, token)
		if e != nil {
			return e
		}
		if e = bindAttempt(ctx, tx, j, command, task); e != nil {
			return e
		}
		return fn(ctx, tx, w, j, command)
	})
}
