package training

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// loadDelegatedWithin follows user -> Workspace -> node -> job, including denied
// authority bookkeeping. It grants no session and never commits a caller's work.
func (s *Service) loadDelegatedWithin(ctx context.Context, q transaction.Executor, c candidate) (identity.Auth, workspaces.Workspace, nodes.Node, Job, bool, error) {
	zero := func(e error) (identity.Auth, workspaces.Workspace, nodes.Node, Job, bool, error) {
		return identity.Auth{}, workspaces.Workspace{}, nodes.Node{}, Job{}, false, e
	}
	store, e := s.trainingRepository()
	if e != nil {
		return zero(e)
	}
	if ctx == nil || q == nil || store.Dialect() != q.Dialect() || s.Nodes == nil || s.Nodes.Identity != s.Identity {
		return zero(fail("TRAINING_STORAGE_MISMATCH"))
	}
	for _, id := range []string{c.id, c.tenant, c.workspace, c.node, c.actor} {
		if !datawire.ValidID(id) {
			return zero(fail("TRAINING_ATTEMPT_STALE"))
		}
	}
	var epoch int64
	var status string
	var platformAdmin bool
	if e = q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=$1 FOR UPDATE", "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE", c.actor)).Scan(&epoch, &status, &platformAdmin); e != nil {
		return zero(e)
	}
	a := identity.Auth{Me: identity.Me{User: identity.User{ID: c.actor, IsPlatformAdmin: platformAdmin}, Audience: c.audience}, Epoch: epoch}
	w := workspaces.Workspace{ID: c.workspace, TenantID: c.tenant}
	var tenantActive bool
	if e = q.QueryRow(ctx, acceptedAdapterSQL(q,
		`SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR UPDATE OF w`,
		`SELECT w.status,EXISTS(SELECT 1 FROM tenants t WHERE t.id=w.tenant_id AND t.status='active') FROM workspaces w WHERE w.tenant_id=? AND w.id=? AND EXISTS(SELECT 1 FROM installation_state i WHERE i.id=1 AND i.bootstrap_complete AND i.tenant_id=w.tenant_id) FOR UPDATE`, c.tenant, c.workspace)).Scan(&w.Status, &tenantActive); e != nil {
		return zero(e)
	}
	valid := status == "active" && epoch == c.epoch && tenantActive && w.Status == "active"
	switch c.audience {
	case "admin":
		valid = valid && platformAdmin
	case "portal":
		var role, authority string
		e = q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT role,authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3 FOR SHARE", "SELECT role,authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=? FOR SHARE", c.tenant, c.workspace, c.actor)).Scan(&role, &authority)
		if errors.Is(e, sql.ErrNoRows) {
			valid = false
		} else if e != nil {
			return zero(e)
		} else {
			w.Role = &role
			valid = valid && role == "owner" && c.membership != nil && *c.membership == authority
		}
	default:
		valid = false
	}
	n, nodeError := s.Nodes.TrainingTargetWithin(ctx, q, c.tenant, c.workspace, c.node)
	if nodeError != nil && nodeError.Error() != "RESOURCE_NOT_FOUND" && nodeError.Error() != "NODE_UNAVAILABLE" && nodeError.Error() != "AGENT_PROFILE_REJECTED" {
		return zero(nodeError)
	}
	j, e := lockedJobWithin(ctx, q, w, c.id)
	if e != nil {
		return zero(e)
	}
	var actor, audience string
	var requestedEpoch int64
	var membership *string
	if e = q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT requested_by::text,requested_audience,requested_epoch,membership_authority::text FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT requested_by,requested_audience,requested_epoch,membership_authority FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND id=?", w.TenantID, w.ID, j.ID)).Scan(&actor, &audience, &requestedEpoch, &membership); e != nil {
		return zero(e)
	}
	sameMembership := membership == nil && c.membership == nil || membership != nil && c.membership != nil && *membership == *c.membership
	if actor != c.actor || audience != c.audience || requestedEpoch != c.epoch || !sameMembership {
		return zero(fail("TRAINING_ATTEMPT_STALE"))
	}
	if nodeError != nil {
		n = nodes.Node{ID: c.node, TenantID: c.tenant, WorkspaceID: c.workspace, Status: "unavailable"}
	}
	return a, w, n, j, valid, nil
}

func (s *Service) delegatedRepositoryTransaction(ctx context.Context, c candidate, fn func(context.Context, transaction.Executor, identity.Auth, workspaces.Workspace, nodes.Node, Job, bool) error) error {
	if ctx == nil || fn == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	store, e := s.trainingRepository()
	if e != nil {
		return trainingRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		a, w, n, j, valid, e := s.loadDelegatedWithin(ctx, q, c)
		if e != nil {
			return e
		}
		return fn(ctx, q, a, w, n, j, valid)
	})
	return trainingRepositoryError(e)
}
