package datasets

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type candidate struct {
	id, tenant, workspace, dataset, draft, actor, audience string
	epoch                                                  int64
	membership                                             *string
}

func (s *Service) nextCandidate(ctx context.Context) (candidate, error) {
	repo, e := s.repository()
	if e != nil {
		return candidate{}, e
	}

	var c candidate
	e = repo.QueryRow(ctx, datasetSQL(repo, `SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.dataset_id::text,j.draft_id::text,j.requested_by::text,j.requested_audience,j.requested_epoch,j.membership_authority::text FROM dataset_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND ((j.state='pending' AND j.not_before<=clock_timestamp()) OR (j.state='running' AND j.lease_expires_at<=clock_timestamp())) ORDER BY j.created_at,j.id LIMIT 1`, `SELECT j.id,j.tenant_id,j.workspace_id,j.dataset_id,j.draft_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority FROM dataset_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND ((j.state='pending' AND j.not_before<=CURRENT_TIMESTAMP(6)) OR (j.state='running' AND j.lease_expires_at<=CURRENT_TIMESTAMP(6))) ORDER BY j.created_at,j.id LIMIT 1`, nil, nil, transaction.SQL(transaction.Dameng, dmWorkerCandidate))).Scan(&c.id, &c.tenant, &c.workspace, &c.dataset, &c.draft, &c.actor, &c.audience, &c.epoch, &c.membership)
	return c, e
}

// workerTransaction follows user -> Workspace -> dataset -> draft -> job locks. Revoked delegation may only record failure/cancellation, never commit prepared content.
func (s *Service) workerTransaction(ctx context.Context, c candidate, fn func(context.Context, transaction.Executor, identity.Auth, workspaces.Workspace, Dataset, Draft, Job, bool) error) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var epoch int64
		var status string
		var platformAdmin bool
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=$1 FOR UPDATE", `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE`, []any{c.actor}, []any{c.actor}, transaction.SQL(transaction.Dameng, dmWorkerActorLock, c.actor))).Scan(&epoch, &status, &platformAdmin); e != nil {
			return e
		}
		a := identity.Auth{Me: identity.Me{User: identity.User{ID: c.actor, IsPlatformAdmin: platformAdmin}, Audience: c.audience}, Epoch: epoch}
		w := workspaces.Workspace{ID: c.workspace, TenantID: c.tenant}
		var tenantActive bool
		if e = tx.QueryRow(ctx, datasetSQL(tx, `SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR UPDATE OF w`, `SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, []any{c.tenant, c.workspace}, []any{c.tenant, c.workspace}, transaction.SQL(transaction.Dameng, dmWorkerWorkspaceLock, c.tenant, c.workspace))).Scan(&w.Status, &tenantActive); e != nil {
			return e
		}
		valid := status == "active" && epoch == c.epoch && tenantActive && w.Status == "active"
		if c.audience == "admin" {
			valid = valid && platformAdmin
		} else if c.audience == "portal" {
			var role, authority string
			e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT role,authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", `SELECT role,authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`, []any{c.tenant, c.workspace, c.actor}, []any{c.tenant, c.workspace, c.actor}, transaction.SQL(transaction.Dameng, dmWorkerMembership, c.tenant, c.workspace, c.actor))).Scan(&role, &authority)
			if errors.Is(e, pgx.ErrNoRows) {
				valid = false
			} else if e != nil {
				return e
			} else {
				w.Role = &role
				valid = valid && c.membership != nil && authority == *c.membership && role != "viewer"
			}
		} else {
			valid = false
		}
		parent, e := lockedDataset(ctx, tx, w, c.dataset)
		if e != nil {
			return e
		}
		draft, e := lockedDraft(ctx, tx, w, c.dataset, c.draft)
		if e != nil {
			return e
		}
		job, e := lockedJob(ctx, tx, w, c.dataset, c.id)
		if e != nil {
			return e
		}
		if job.RequestedBy != c.actor {
			return failure("DATASET_ATTEMPT_STALE")
		}
		if job.Kind == "publish" {
			valid = valid && isOwner(a, w)
		} else {
			valid = valid && (isOwner(a, w) || draft.CreatedBy == c.actor)
		}
		valid = valid && parent.RetiredAt == nil && draft.CurrentJobID != nil && *draft.CurrentJobID == job.ID && draft.Revision == job.ExpectedRevision
		if e = fn(ctx, tx, a, w, parent, draft, job, valid); e != nil {
			return e
		}
		return nil
	})
	return datasetRuntimeError(e)
}
