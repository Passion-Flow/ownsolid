package distribution

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"sort"
	"strings"
	"time"
)

func distributionError(e error) error {
	if e == nil {
		return nil
	}
	var rate *identity.RateError
	if errors.As(e, &rate) {
		return e
	}
	if errors.Is(e, context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	c := e.Error()
	if len(c) > 120 {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, r := range c {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	for _, p := range []string{"STORAGE_", "AUTH_", "LICENSE_", "GATEWAY_", "API_KEY_", "MODEL_", "ALIAS_", "TRANSIENT_"} {
		if strings.HasPrefix(c, p) {
			return fail(c)
		}
	}
	switch c {
	case "ACCESS_DENIED", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "RESOURCE_NOT_FOUND", "RESOURCE_LIMIT_REACHED", "FORBIDDEN", "VERSION_CONFLICT", "INVALID_CURSOR", "CONFIG_MACHINE_TRANSPORT_REQUIRED":
		return fail(c)
	}
	return fail("STORAGE_UNAVAILABLE")
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, fail("GATEWAY_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("GATEWAY_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func distributionSQL(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func distributionStatement(q transaction.Executor, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func (s *Service) executor(ctx context.Context, q transaction.Executor) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if ctx == nil || q == nil || store.Dialect() != q.Dialect() {
		return fail("GATEWAY_STORAGE_MISMATCH")
	}
	if _, pool := q.(transaction.Store); pool {
		return fail("GATEWAY_TRANSACTION_REQUIRED")
	}
	return distributionError(ctx.Err())
}
func (s *Service) storageTransaction(ctx context.Context, fn func(context.Context, transaction.Executor) error) error {
	store, e := s.repository()
	if e != nil {
		return distributionError(e)
	}
	if s.Identity.Config.Policy().Validate() != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if ctx == nil || fn == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	operation, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return distributionError(store.Within(operation, false, fn))
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, active bool, additionalUsers []string, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if !wire.ValidID(workspace) {
		return fail("VALIDATION_FAILED")
	}
	return s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		users := append(append([]string{}, additionalUsers...), a.Me.User.ID)
		sort.Strings(users)
		previous := ""
		for _, user := range users {
			if !wire.ValidID(user) {
				return fail("ACCESS_DENIED")
			}
			if user == previous {
				continue
			}
			previous = user
			var locked string
			if e := q.QueryRow(ctx, distributionNativeSQL(q, `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, "SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", user)).Scan(&locked); e != nil {
				return e
			}
		}
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if active && w.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
}
func (s *Service) gatewayComponents() error {
	if s.Nodes == nil || s.Nodes.Identity != s.Identity || s.Nodes.Workspaces != s.Workspaces || s.Deployments == nil || s.Deployments.Nodes != s.Nodes || s.Deployments.Models == nil || s.Deployments.Models.Identity != s.Identity || s.Deployments.Models.Workspaces != s.Workspaces {
		return fail("GATEWAY_STORAGE_MISMATCH")
	}
	return nil
}

const mysqlGatewayFields = `id,workspace_id,node_id,name,client_origin,created_by,CASE WHEN authority_invalidated_at IS NOT NULL THEN 'invalidated' WHEN revoked_at IS NOT NULL THEN 'revoked' ELSE 'active' END,created_at,revoked_at`

const mysqlAliasFields = `id,workspace_id,gateway_id,node_id,name,deployment_id,deployment_revision,revision,disabled_at,created_at`

const mysqlKeyFields = `k.id,k.workspace_id,k.alias_id,a.name,k.label,k.created_by,CASE WHEN k.revoked_at IS NOT NULL THEN 'revoked' WHEN k.expires_at<=UTC_TIMESTAMP(6) THEN 'expired' WHEN k.requested_epoch<>u.auth_epoch OR u.status<>'active' OR NOT EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.tenant_id=k.tenant_id AND m.workspace_id=k.workspace_id AND m.user_id=k.created_by AND m.authority_id=k.membership_authority) THEN 'invalidated' WHEN w.status<>'active' THEN 'unavailable' ELSE 'active' END,k.created_at,k.expires_at,k.last_used_at,k.revoked_at`

const mysqlRequestFields = `r.id,r.workspace_id,r.gateway_id,r.node_id,r.api_key_id,r.actor_user_id,r.alias_id,a.name,r.deployment_id,r.deployment_revision,r.state,r.capacity_held,r.stream,r.admitted_at,r.deadline_at,r.reported_at,r.reconciled_at,r.active_recovery_id,r.outcome`

const mysqlRecoveryFields = `id,request_id,workspace_id,CASE WHEN confirmed_at IS NOT NULL THEN 'completed' WHEN invalidated_at IS NOT NULL THEN 'invalidated' WHEN superseded_at IS NOT NULL THEN 'superseded' WHEN expires_at<=UTC_TIMESTAMP(6) THEN 'expired' WHEN fence_accepted_at IS NOT NULL THEN 'fenced' ELSE 'pending_fence' END,created_at,expires_at,fence_accepted_at,confirmed_at,fence_receipt`

const mysqlTargetFields = `id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256,created_at,source_revision`

func distributionUTCPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	stamp := value.UTC()
	return &stamp
}
