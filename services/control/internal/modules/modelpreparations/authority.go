package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type candidate struct {
	id, tenant, workspace, node, actor, audience string
	epoch                                        int64
	membership                                   *string
}

func (s *Service) nextCandidate(ctx context.Context) (candidate, error) {
	var c candidate
	store, e := s.runtimeRepository()
	if e != nil {
		return c, e
	}
	e = preparationIDRow(preparationQueryRow(ctx, store, preparationSelectedSQL(store, preparationDM(`SELECT "J"."ID","J"."TENANT_ID","J"."WORKSPACE_ID","J"."CURRENT_NODE_ID","J"."REQUESTED_BY","J"."REQUESTED_AUDIENCE","J"."REQUESTED_EPOCH","J"."MEMBERSHIP_AUTHORITY" FROM "MODEL_PREPARATIONS" "J" JOIN "INSTALLATION_STATE" "I" ON "I"."TENANT_ID"="J"."TENANT_ID" WHERE "I"."ID"=1 AND "I"."BOOTSTRAP_COMPLETE"=1 AND "J"."STATE"='queued' AND "J"."NEXT_DISPATCH_AT"<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ORDER BY "J"."NEXT_DISPATCH_AT","J"."CREATED_AT","J"."ID" LIMIT 1`), `SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.current_node_id::text,j.requested_by::text,j.requested_audience,j.requested_epoch,j.membership_authority::text FROM model_preparations j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.state='queued' AND j.next_dispatch_at<=clock_timestamp() ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`, `SELECT j.id,j.tenant_id,j.workspace_id,j.current_node_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority FROM model_preparations j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.state='queued' AND j.next_dispatch_at<=UTC_TIMESTAMP(6) ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`)), 0, 1, 2, 3, 4, 7).Scan(&c.id, &c.tenant, &c.workspace, &c.node, &c.actor, &c.audience, &c.epoch, &c.membership)
	return c, e
}

// delegatedTransaction follows user -> Workspace -> node -> job locks, also for denied authority bookkeeping.
func (s *Service) delegatedTransaction(ctx context.Context, c candidate, fn func(context.Context, transaction.Executor, identity.Auth, workspaces.Workspace, nodes.Node, Preparation, bool) error) error {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	store, e := s.reviewRepository()
	if e != nil {
		return preparationRepositoryError(e)
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := preparationBorrowedExecutor(store, tx); e != nil {
			return e
		}
		var epoch int64
		var status string
		var platformAdmin bool
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "AUTH_EPOCH","STATUS","IS_PLATFORM_ADMIN" FROM "USERS" WHERE "ID"=? FOR UPDATE`), "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=$1", `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE`, c.actor)).Scan(&epoch, &status, &platformAdmin); e != nil {
			return e
		}
		a := identity.Auth{Me: identity.Me{User: identity.User{ID: c.actor, IsPlatformAdmin: platformAdmin}, Audience: c.audience}, Epoch: epoch}
		w := workspaces.Workspace{ID: c.workspace, TenantID: c.tenant}
		// ADR 0020: this runs on every preparation agent call, so on PostgreSQL it locks
		// neither the actor nor the Workspace row, which all of a user's nodes share. The
		// node lock fences the status read; the other chains lock the Workspace first.
		scope := func() (bool, error) {
			var active bool
			e := preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "W"."STATUS",CASE WHEN "T"."STATUS"='active' THEN 1 ELSE 0 END FROM "WORKSPACES" "W" JOIN "TENANTS" "T" ON "T"."ID"="W"."TENANT_ID" JOIN "INSTALLATION_STATE" "I" ON "I"."TENANT_ID"="W"."TENANT_ID" WHERE "I"."ID"=1 AND "I"."BOOTSTRAP_COMPLETE"=1 AND "W"."TENANT_ID"=? AND "W"."ID"=? FOR UPDATE OF "W"."ID"`), `SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2`, `SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, c.tenant, c.workspace)).Scan(&w.Status, &active)
			return active, e
		}
		var tenantActive bool
		if tx.Dialect() != transaction.PostgreSQL {
			if tenantActive, e = scope(); e != nil {
				return e
			}
		}
		authorized := false
		if c.audience == "admin" {
			authorized = platformAdmin
		} else if c.audience == "portal" {
			var role, authority string
			e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "ROLE","AUTHORITY_ID" FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`), "SELECT role,authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", `SELECT role,authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`, c.tenant, c.workspace, c.actor)), 1).Scan(&role, &authority)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e == nil {
				w.Role = &role
				authorized = role == "owner" && c.membership != nil && *c.membership == authority
			}
		}
		n, nodeError := s.Nodes.ModelPreparationTargetWithin(ctx, tx, c.tenant, c.workspace, c.node)
		// Expected node unavailability is recorded by the caller; actual database errors abort.
		if nodeError != nil && nodeError.Error() != "RESOURCE_NOT_FOUND" && nodeError.Error() != "NODE_UNAVAILABLE" && nodeError.Error() != "AGENT_PROFILE_REJECTED" {
			return nodeError
		}
		if tx.Dialect() == transaction.PostgreSQL {
			if tenantActive, e = scope(); e != nil {
				return e
			}
		}
		valid := authorized && status == "active" && epoch == c.epoch && tenantActive && w.Status == "active"
		j, e := lockedPreparationWithin(ctx, tx, w, c.id)
		if e != nil {
			return e
		}
		var actor, audience string
		var requestedEpoch int64
		var membership *string
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "REQUESTED_BY","REQUESTED_AUDIENCE","REQUESTED_EPOCH","MEMBERSHIP_AUTHORITY" FROM "MODEL_PREPARATIONS" WHERE "ID"=?`), "SELECT requested_by::text,requested_audience,requested_epoch,membership_authority::text FROM model_preparations WHERE id=$1", `SELECT requested_by,requested_audience,requested_epoch,membership_authority FROM model_preparations WHERE id=?`, j.ID)), 0, 3).Scan(&actor, &audience, &requestedEpoch, &membership); e != nil {
			return e
		}
		sameMembership := membership == nil && c.membership == nil || membership != nil && c.membership != nil && *membership == *c.membership
		if j.CurrentNodeID != c.node {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		if actor != c.actor || audience != c.audience || requestedEpoch != c.epoch || !sameMembership {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		if nodeError != nil {
			n = nodes.Node{ID: c.node, TenantID: c.tenant, WorkspaceID: c.workspace, Status: "unavailable"}
		}
		if e = fn(ctx, tx, a, w, n, j, valid); e != nil {
			return e
		}
		return nil
	})
	return preparationRepositoryError(e)
}
