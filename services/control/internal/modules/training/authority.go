package training

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

type candidate struct {
	id, tenant, workspace, node, actor, audience string
	epoch                                        int64
	membership                                   *string
}

func (s *Service) nextCandidate(ctx context.Context) (candidate, error) {
	var c candidate
	store, e := s.trainingRepository()
	if e != nil {
		return c, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	e = store.QueryRow(ctx, acceptedAdapterSQL(store,
		`SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.current_node_id::text,j.requested_by::text,j.requested_audience,j.requested_epoch,j.membership_authority::text FROM training_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.site_id=local_site_id() AND j.cell_id=local_cell_id() AND j.state='queued' AND j.next_dispatch_at<=clock_timestamp() ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`,
		`SELECT j.id,j.tenant_id,j.workspace_id,j.current_node_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority FROM training_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.state='queued' AND j.next_dispatch_at<=UTC_TIMESTAMP(6) ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`)).Scan(&c.id, &c.tenant, &c.workspace, &c.node, &c.actor, &c.audience, &c.epoch, &c.membership)
	if e != nil {
		return candidate{}, e
	}
	return c, nil
}

// The retained PostgreSQL callback borrows the same ordered authority loader.
// New owners use delegatedRepositoryTransaction without a second commit owner.
func (s *Service) delegatedTransaction(ctx context.Context, c candidate, fn func(context.Context, pgx.Tx, identity.Auth, workspaces.Workspace, nodes.Node, Job, bool) error) error {
	if s == nil || s.DB == nil || fn == nil {
		return fail("TRAINING_STORAGE_MISMATCH")
	}
	if _, e := s.trainingRepository(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return trainingRepositoryError(e)
	}
	defer tx.Rollback(ctx)
	a, w, n, j, valid, e := s.loadDelegatedWithin(ctx, transaction.PostgreSQLTransaction(tx), c)
	if e != nil {
		return e
	}
	if e = fn(ctx, tx, a, w, n, j, valid); e != nil {
		return e
	}
	return trainingRepositoryError(tx.Commit(ctx))
}
