package nodes

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func joinWriteError(e error) error {
	if e == nil {
		return nil
	}
	switch e.Error() {
	case "RESOURCE_LIMIT_REACHED", "NODE_JOIN_STATE_CONFLICT":
		return fail(e.Error())
	default:
		return nodeRepositoryError(e)
	}
}
func (s *Service) withJoinWriter(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || !isAdmin(a) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		return fn(ctx, q)
	})
	return joinWriteError(e)
}

// Installation serializes token capacity after the Workspace fence,matching
// legacy enrollment. Tenant uses a separate shared read,never a lock upgrade.
func lockedJoinTenant(ctx context.Context, q transaction.Executor) (string, error) {
	var tenant, active string
	e := q.QueryRow(ctx, nodeSQL(q, `SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR UPDATE`, `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR UPDATE`, `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR UPDATE`)).Scan(&tenant)
	if errors.Is(e, sql.ErrNoRows) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return "", e
	}
	e = q.QueryRow(ctx, nodeSQL(q, `SELECT id::text FROM tenants WHERE id=$1 AND status='active' FOR SHARE`, `SELECT id FROM tenants WHERE id=? AND status='active' FOR SHARE`, `SELECT id FROM tenants WHERE id=? AND status='active' FOR UPDATE`, tenant)).Scan(&active)
	if errors.Is(e, sql.ErrNoRows) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return "", e
	}
	return tenant, nil
}
func joinWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string, lock bool) (JoinToken, error) {
	pg := `SELECT ` + joinFields + ` FROM node_join_tokens j WHERE j.tenant_id=$1 AND j.workspace_id=$2 AND j.id=$3`
	my := `SELECT ` + mysqlJoinFields + ` FROM node_join_tokens j WHERE j.tenant_id=? AND j.workspace_id=? AND j.id=?`
	dm := `SELECT ` + damengJoinFields + ` FROM node_join_tokens j WHERE j.tenant_id=? AND j.workspace_id=? AND j.id=?`
	if lock {
		pg += " FOR UPDATE OF j"
		my += " FOR UPDATE"
		dm += " FOR UPDATE"
	}
	return scanJoin(q.QueryRow(ctx, nodeSQL(q, pg, my, dm, tenant, workspace, id)))
}
func joinAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, id, reason, request string) error {
	_, e := q.Exec(ctx, nodeSQL(q,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin',$5,'success','node_join_token',$6,NULLIF($7,''),$8)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin',?,'success','node_join_token',?,NULLIF(?,''),?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,'admin',?,'success','node_join_token',?,NULLIF(?,''),?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, action, id, reason, request))
	return e
}
func (s *Service) Mint(ctx context.Context, a identity.Auth, workspace, name, request string) (MintedToken, error) {
	return s.MintForProfile(ctx, a, workspace, name, wire.ReadOnlyProfile, request)
}
func (s *Service) MintForProfile(ctx context.Context, a identity.Auth, workspace, name, profile, request string) (MintedToken, error) {
	if !isAdmin(a) {
		return MintedToken{}, fail("ACCESS_DENIED")
	}
	if !nodeID(workspace) || !nodeID(request) {
		return MintedToken{}, fail("VALIDATION_FAILED")
	}
	if profile == "" {
		profile = wire.ReadOnlyProfile
	}
	if !wire.KnownProfile(profile) {
		return MintedToken{}, fail("VALIDATION_FAILED")
	}
	name, e := security.Name(name)
	if e != nil {
		return MintedToken{}, e
	}
	var out MintedToken
	e = s.withJoinWriter(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.readScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		// Kubernetes cells are stored only on PostgreSQL (ADR 0051).
		if profile == wire.KubernetesNodeProfile && q.Dialect() != transaction.PostgreSQL {
			return fail("AGENT_PROFILE_REJECTED")
		}
		tenant, e := lockedJoinTenant(ctx, q)
		if e != nil {
			return e
		}
		if tenant != w.TenantID {
			return fail("RESOURCE_NOT_FOUND")
		}
		var count int
		e = q.QueryRow(ctx, nodeSQL(q, `SELECT count(*) FROM node_join_tokens WHERE tenant_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()`, `SELECT count(*) FROM node_join_tokens WHERE tenant_id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, `SELECT count(*) FROM node_join_tokens WHERE tenant_id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))`, tenant)).Scan(&count)
		if e != nil {
			return e
		}
		policy := s.Identity.Config.Policy().Nodes
		if count >= policy.MaximumLiveJoinTokens {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		raw, id := security.Token(), uuid.NewString()
		_, e = q.Exec(ctx, nodeSQL(q,
			`INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at,execution_profile) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+make_interval(secs=>$7),$8)`,
			`INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at,execution_profile) VALUES(?,?,?,?,?,?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),?)`, `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at,execution_profile) VALUES(?,?,?,?,?,?,DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),?)`, id, tenant, w.ID, name, digest("node-join", raw), a.Me.User.ID, policy.JoinTTLSeconds, profile))
		if e != nil {
			return e
		}
		if e = joinAuditWithin(ctx, q, a, w, "node.join_token_created", id, "", request); e != nil {
			return e
		}
		value, e := joinWithin(ctx, q, tenant, w.ID, id, false)
		if e != nil {
			return e
		}
		out = MintedToken{JoinToken: value, Token: raw}
		return nil
	})
	if e != nil {
		return MintedToken{}, e
	}
	return out, nil
}
func (s *Service) RevokeToken(ctx context.Context, a identity.Auth, id, reason, request string) (JoinToken, error) {
	if !isAdmin(a) {
		return JoinToken{}, fail("ACCESS_DENIED")
	}
	if !nodeID(id) || !nodeID(request) || !workspaces.ValidReason(reason) {
		return JoinToken{}, fail("VALIDATION_FAILED")
	}
	var out JoinToken
	e := s.withJoinWriter(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var workspace string
		e := q.QueryRow(ctx, nodeSQL(q, `SELECT j.workspace_id::text FROM node_join_tokens j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.id=$1`, `SELECT j.workspace_id FROM node_join_tokens j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND j.id=?`, `SELECT j.workspace_id FROM node_join_tokens j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND j.id=?`, id)).Scan(&workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		w, e := s.readScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		tenant, e := lockedJoinTenant(ctx, q)
		if e != nil {
			return e
		}
		if tenant != w.TenantID {
			return fail("RESOURCE_NOT_FOUND")
		}
		value, e := joinWithin(ctx, q, tenant, w.ID, id, true)
		if e != nil {
			return e
		}
		if value.Status != "pending" {
			return fail("NODE_JOIN_STATE_CONFLICT")
		}
		result, e := q.Exec(ctx, nodeSQL(q, `UPDATE node_join_tokens SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()`, `UPDATE node_join_tokens SET revoked_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, `UPDATE node_join_tokens SET revoked_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))`, tenant, w.ID, id))
		if e != nil {
			return e
		}
		if result != 1 {
			return fail("NODE_JOIN_STATE_CONFLICT")
		}
		if e = joinAuditWithin(ctx, q, a, w, "node.join_token_revoked", id, reason, request); e != nil {
			return e
		}
		out, e = joinWithin(ctx, q, tenant, w.ID, id, false)
		return e
	})
	if e != nil {
		return JoinToken{}, e
	}
	return out, nil
}
