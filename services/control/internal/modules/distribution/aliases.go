package distribution

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

const maximumRevision int64 = 9007199254740991
const aliasFields = `id::text,workspace_id::text,gateway_id::text,node_id::text,name,deployment_id::text,deployment_revision,revision,disabled_at,created_at`

func scanAlias(row transaction.Row) (Alias, error) {
	var out Alias
	e := row.Scan(&out.ID, &out.WorkspaceID, &out.GatewayID, &out.NodeID, &out.Name, &out.DeploymentID, &out.DeploymentRevision, &out.Revision, &out.DisabledAt, &out.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Alias{}, fail("MODEL_ALIAS_NOT_FOUND")
	}
	if e != nil {
		return Alias{}, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.DisabledAt = distributionUTCPointer(out.DisabledAt)
	return out, nil
}
func (s *Service) aliasAvailability(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, out *Alias) error {
	if e := s.gatewayComponents(); e != nil {
		return e
	}
	unavailable := "MODEL_ALIAS_UNAVAILABLE"
	if e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT COUNT(*) FROM GATEWAY_REQUESTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ALIAS_ID=? AND CAPACITY_HELD=1`, []any{w.TenantID, w.ID, out.ID}, "SELECT count(*) FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND alias_id=$3 AND capacity_held", `SELECT count(*) FROM gateway_requests WHERE tenant_id=? AND workspace_id=? AND alias_id=? AND capacity_held`, []any{w.TenantID, w.ID, out.ID}, []any{w.TenantID, w.ID, out.ID})).Scan(&out.HeldRequests); e != nil {
		return e
	}
	out.Available = false
	out.UnavailableCode = &unavailable
	var active bool
	if e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CASE WHEN REVOKED_AT IS NULL AND AUTHORITY_INVALIDATED_AT IS NULL AND CREDENTIAL_HASH IS NOT NULL THEN 1 ELSE 0 END,CLIENT_ORIGIN FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`, []any{w.TenantID, w.ID, out.GatewayID}, "SELECT revoked_at IS NULL AND authority_invalidated_at IS NULL AND credential_hash IS NOT NULL,COALESCE(client_origin,'') FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", `SELECT revoked_at IS NULL AND authority_invalidated_at IS NULL AND credential_hash IS NOT NULL,client_origin FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND id=?`, []any{w.TenantID, w.ID, out.GatewayID}, []any{w.TenantID, w.ID, out.GatewayID})).Scan(&active, &out.ClientOrigin); e != nil {
		return e
	}
	if !active || out.DisabledAt != nil {
		return nil
	}
	_, e := s.Deployments.ReadyGatewayTargetWithin(ctx, tx, w.TenantID, w.ID, out.NodeID, out.DeploymentID, out.DeploymentRevision)
	if e != nil {
		switch e.Error() {
		case "MODEL_ALIAS_UNAVAILABLE", "RESOURCE_NOT_FOUND", "MODEL_FILE_INVALID":
			return nil
		default:
			return e
		}
	}
	out.Available = true
	out.UnavailableCode = nil
	return nil
}

func (s *Service) Publish(ctx context.Context, a identity.Auth, input PublishRequest, key, request string) (Alias, error) {
	if !admin(a) {
		return Alias{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(input.GatewayID) || !wire.ValidID(input.DeploymentID) || !wire.ValidID(key) || !wire.ValidAlias(input.Name) || input.DeploymentRevision < 1 || input.DeploymentRevision >= maximumRevision || !reason(input.Reason) {
		return Alias{}, fail("VALIDATION_FAILED")
	}
	var out Alias
	e := s.transaction(ctx, a, input.WorkspaceID, true, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		expected := fingerprint(a.Me.User.ID, input)
		var existing string
		var stored []byte
		e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT ID,FINGERPRINT FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND CREATED_BY=? AND IDEMPOTENCY_KEY=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, "SELECT id::text,fingerprint FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND idempotency_key=$4", `SELECT id,fingerprint FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND created_by=? AND idempotency_key=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, a.Me.User.ID, key})).Scan(&existing, &stored)
		if e == nil {
			if !same(stored, expected) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out, e = scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE ID=?", []any{existing}, "SELECT "+aliasFields+" FROM model_aliases WHERE id=$1", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE id=?`, []any{existing}, []any{existing})))
			if e != nil {
				return e
			}
			return s.aliasAvailability(ctx, tx, w, &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		gateway, e := scanGateway(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, input.GatewayID}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, input.GatewayID}, []any{w.TenantID, w.ID, input.GatewayID})))
		if e != nil {
			return e
		}
		if gateway.State != "active" {
			return fail("GATEWAY_INSTANCE_REVOKED")
		}
		// A gateway the agent runs has no address until it is running.
		if gateway.Delivery == "agent" && (gateway.DeliveryState == nil || *gateway.DeliveryState != "running") {
			return fail("GATEWAY_NOT_STARTED")
		}
		target, e := s.Deployments.ReadyGatewayTargetWithin(ctx, tx, w.TenantID, w.ID, gateway.NodeID, input.DeploymentID, input.DeploymentRevision)
		if e != nil {
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT COUNT(*) FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND DISABLED_AT IS NULL`, []any{w.TenantID, w.ID}, "SELECT count(*) FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND disabled_at IS NULL", `SELECT count(*) FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND disabled_at IS NULL`, []any{w.TenantID, w.ID}, []any{w.TenantID, w.ID})).Scan(&count); e != nil {
			return e
		}
		if count >= s.Identity.Config.Policy().Distribution.AliasMaxActive {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		var exists bool
		if e = tx.QueryRow(ctx, distributionNativeSQL(tx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND NAME=?) THEN 1 ELSE 0 END FROM DUAL`, "SELECT EXISTS(SELECT 1 FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND name=$3)", "SELECT EXISTS(SELECT 1 FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND name=?)", w.TenantID, w.ID, input.Name)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fail("MODEL_ALIAS_EXISTS")
		}
		id := uuid.NewString()
		_, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO MODEL_ALIASES(ID,TENANT_ID,WORKSPACE_ID,NODE_ID,GATEWAY_ID,NAME,DEPLOYMENT_ID,DEPLOYMENT_REVISION,CREATED_BY,IDEMPOTENCY_KEY,FINGERPRINT) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, []any{id, w.TenantID, w.ID, gateway.NodeID, gateway.ID, input.Name, target.ID, target.Revision, a.Me.User.ID, key, expected}, "INSERT INTO model_aliases(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,created_by,idempotency_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)", `INSERT INTO model_aliases(id,tenant_id,workspace_id,node_id,gateway_id,name,deployment_id,deployment_revision,created_by,idempotency_key,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, []any{id, w.TenantID, w.ID, gateway.NodeID, gateway.ID, input.Name, target.ID, target.Revision, a.Me.User.ID, key, expected}, []any{id, w.TenantID, w.ID, gateway.NodeID, gateway.ID, input.Name, target.ID, target.Revision, a.Me.User.ID, key, expected}))
		if e != nil {
			return e
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO MODEL_ALIAS_HISTORY(TENANT_ID,WORKSPACE_ID,ALIAS_ID,REVISION,ACTION,ACTOR_USER_ID,REASON) VALUES(?,?,?,1,'published',?,?)`, []any{w.TenantID, w.ID, id, a.Me.User.ID, input.Reason}, "INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES($1,$2,$3,1,'published',$4,$5)", `INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES(?,?,?,1,'published',?,?)`, []any{w.TenantID, w.ID, id, a.Me.User.ID, input.Reason}, []any{w.TenantID, w.ID, id, a.Me.User.ID, input.Reason})); e != nil {
			return e
		}
		if e = s.recordAliasTarget(ctx, tx, w, id, 1, "published", nil); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "model.alias_published", "model_alias", id, input.Reason, request); e != nil {
			return e
		}
		out, e = scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE ID=?", []any{id}, "SELECT "+aliasFields+" FROM model_aliases WHERE id=$1", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE id=?`, []any{id}, []any{id})))
		if e != nil {
			return e
		}
		return s.aliasAvailability(ctx, tx, w, &out)
	})
	if e != nil {
		return Alias{}, e
	}
	return out, nil
}

func (s *Service) DisableAlias(ctx context.Context, a identity.Auth, workspace, id string, revision int64, explanation, request string) (Alias, error) {
	if !admin(a) {
		return Alias{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(id) || revision < 1 || revision >= maximumRevision || !reason(explanation) {
		return Alias{}, fail("VALIDATION_FAILED")
	}
	var out Alias
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		current, e := scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, id}, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
		if e != nil {
			return e
		}
		if current.Revision != revision {
			return fail("VERSION_CONFLICT")
		}
		if current.DisabledAt != nil {
			out = current
			return s.aliasAvailability(ctx, tx, w, &out)
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE MODEL_ALIASES SET DISABLED_AT=CURRENT_TIMESTAMP(6),REVISION=REVISION+1 WHERE ID=?`, []any{id}, "UPDATE model_aliases SET disabled_at=clock_timestamp(),revision=revision+1 WHERE id=$1", `UPDATE model_aliases SET disabled_at=UTC_TIMESTAMP(6),revision=revision+1 WHERE id=?`, []any{id}, []any{id})); e != nil {
			return e
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO MODEL_ALIAS_HISTORY(TENANT_ID,WORKSPACE_ID,ALIAS_ID,REVISION,ACTION,ACTOR_USER_ID,REASON) VALUES(?,?,?,?,'disabled',?,?)`, []any{w.TenantID, w.ID, id, revision + 1, a.Me.User.ID, explanation}, "INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES($1,$2,$3,$4,'disabled',$5,$6)", `INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES(?,?,?,?,'disabled',?,?)`, []any{w.TenantID, w.ID, id, revision + 1, a.Me.User.ID, explanation}, []any{w.TenantID, w.ID, id, revision + 1, a.Me.User.ID, explanation})); e != nil {
			return e
		}
		if e = s.recordAliasTarget(ctx, tx, w, id, revision+1, "disabled", nil); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "model.alias_disabled", "model_alias", id, explanation, request); e != nil {
			return e
		}
		out, e = scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE ID=?", []any{id}, "SELECT "+aliasFields+" FROM model_aliases WHERE id=$1", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE id=?`, []any{id}, []any{id})))
		if e != nil {
			return e
		}
		return s.aliasAvailability(ctx, tx, w, &out)
	})
	if e != nil {
		return Alias{}, e
	}
	return out, nil
}

func (s *Service) Aliases(ctx context.Context, a identity.Auth, workspace string) ([]Alias, error) {
	out := []Alias{}
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		query := "SELECT " + aliasFields + " FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2"
		mysqlQuery := "SELECT " + mysqlAliasFields + " FROM model_aliases WHERE tenant_id=? AND workspace_id=?"
		nativeQuery := "SELECT " + nativeAliasFields + " FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=?"
		if !admin(a) {
			query += " AND disabled_at IS NULL"
			mysqlQuery += " AND disabled_at IS NULL"
			nativeQuery += " AND DISABLED_AT IS NULL"
		}
		rows, e := tx.Query(ctx, distributionPageSQL(tx, nativeQuery+" ORDER BY CREATED_AT DESC,ID DESC", query+" ORDER BY created_at DESC,id DESC LIMIT $3", mysqlQuery+" ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize))
		if e != nil {
			return e
		}
		for rows.Next() {
			value, e := scanAlias(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out = append(out, value)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for i := range out {
			if e = s.aliasAvailability(ctx, tx, w, &out[i]); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return []Alias{}, e
	}
	return out, nil
}

// HasModelReferences composes module-owned references without allowing model retirement to bypass active aliases.
func (s *Service) HasModelReferences(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
	if s == nil || s.Deployments == nil || ctx == nil || q == nil {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng {
		return false, fail("MODEL_REFERENCE_CHECK_UNAVAILABLE")
	}
	referenced, e := s.Deployments.HasModelReferences(ctx, q, tenant, workspace, model)
	if e != nil || referenced {
		return referenced, e
	}
	history := "SELECT EXISTS(SELECT 1 FROM model_alias_target_versions WHERE tenant_id=$1 AND workspace_id=$2 AND model_id=$3)"
	live := "SELECT EXISTS(SELECT 1 FROM model_aliases a JOIN deployments d ON d.id=a.deployment_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND d.model_id=$3 AND a.disabled_at IS NULL)"
	if q.Dialect() == transaction.MySQL {
		history = "SELECT EXISTS(SELECT 1 FROM model_alias_target_versions WHERE tenant_id=? AND workspace_id=? AND model_id=?)"
		live = "SELECT EXISTS(SELECT 1 FROM model_aliases a JOIN deployments d ON d.id=a.deployment_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id WHERE a.tenant_id=? AND a.workspace_id=? AND d.model_id=? AND a.disabled_at IS NULL)"
	}
	if e = q.QueryRow(ctx, transaction.SQL(q.Dialect(), history, tenant, workspace, model)).Scan(&referenced); e != nil || referenced {
		return referenced, e
	}
	e = q.QueryRow(ctx, transaction.SQL(q.Dialect(), live, tenant, workspace, model)).Scan(&referenced)
	return referenced, e
}

// RecheckAnswerAliasWithin fences a server-owned model answer against
// alias retargeting/disable, gateway revocation and current deployment health.
// It uses the caller's transaction so the final result/audit cannot race a
// committed authority mutation. No model key or client-provided proof is used.
func (s *Service) RecheckAnswerAliasWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace string, expected Alias) error {
	current, e := s.CurrentAnswerAliasWithin(ctx, tx, a, workspace, expected.ID)
	if e != nil {
		return e
	}
	if current.Revision != expected.Revision || current.DeploymentID != expected.DeploymentID || current.DeploymentRevision != expected.DeploymentRevision || current.GatewayID != expected.GatewayID || current.Name != expected.Name || current.ClientOrigin != expected.ClientOrigin {
		return fail("MODEL_ALIAS_UNAVAILABLE")
	}
	return nil
}

// CurrentAnswerAliasWithin shares current model-reference validation
// with trusted workflow publication/execution in the same caller transaction.
func (s *Service) CurrentAnswerAliasWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, id string) (Alias, error) {
	var zero Alias
	if e := s.executor(ctx, tx); e != nil {
		return zero, e
	}
	if !wire.ValidID(workspace) || !wire.ValidID(id) {
		return zero, fail("VALIDATION_FAILED")
	}
	var lockedID string
	if e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, []any{a.Me.User.ID}, "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE", `SELECT id FROM users WHERE id=? FOR UPDATE`, []any{a.Me.User.ID}, []any{a.Me.User.ID})).Scan(&lockedID); e != nil {
		return zero, e
	}
	if lockedID != a.Me.User.ID {
		return zero, fail("ACCESS_DENIED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return zero, e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return zero, e
	}
	if w.Status != "active" || w.Role == nil {
		return zero, fail("ACCESS_DENIED")
	}
	current, e := scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, id}, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
	if e != nil {
		return zero, e
	}
	if e = s.aliasAvailability(ctx, tx, w, &current); e != nil {
		return zero, e
	}
	if !current.Available {
		return zero, fail("MODEL_ALIAS_UNAVAILABLE")
	}
	return current, nil
}

// CheckHistoricalAliasAccessWithin checks current access to caller-owned
// history. Retargeting, unavailable compute and rotated transport credentials
// do not grant a new model call and do not erase historical customer content.
func (s *Service) CheckHistoricalAliasAccessWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace string, snapshot Alias) error {
	if e := s.executor(ctx, tx); e != nil {
		return e
	}
	if !wire.ValidID(workspace) || !wire.ValidID(snapshot.ID) || snapshot.WorkspaceID != workspace {
		return fail("MODEL_ALIAS_UNAVAILABLE")
	}
	var lockedID string
	if e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, []any{a.Me.User.ID}, "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE", `SELECT id FROM users WHERE id=? FOR UPDATE`, []any{a.Me.User.ID}, []any{a.Me.User.ID})).Scan(&lockedID); e != nil {
		return e
	}
	if lockedID != a.Me.User.ID {
		return fail("ACCESS_DENIED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return e
	}
	if w.Status != "active" || w.Role == nil {
		return fail("ACCESS_DENIED")
	}
	current, e := scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, snapshot.ID}, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? FOR SHARE`, []any{w.TenantID, w.ID, snapshot.ID}, []any{w.TenantID, w.ID, snapshot.ID})))
	if e != nil {
		return e
	}
	if current.DisabledAt != nil {
		return fail("MODEL_ALIAS_UNAVAILABLE")
	}
	return nil
}
