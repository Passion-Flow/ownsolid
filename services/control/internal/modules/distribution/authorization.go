package distribution

import (
	"context"
	"database/sql"
	"errors"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type machineScope struct{ gateway, node, tenant, workspace string }
type keyAuthority struct {
	id, owner, alias    string
	membershipAuthority string
	epoch               int64
	revoked, expired    bool
	operation, used     bool
	hash                []byte
}

func (s *Service) gatewayAuthority(ctx context.Context, q transaction.Executor, id, credential string, revokedAllowed, lock bool) (machineScope, error) {
	var out machineScope
	parsed, token, e := parseCredential(gatewayCredentialPrefix, credential)
	if e != nil || id != parsed {
		return out, fail("GATEWAY_UNAUTHORIZED")
	}
	suffix, mysqlSuffix := "", ""
	if lock {
		suffix = " FOR UPDATE OF g"
		mysqlSuffix = " FOR UPDATE"
	}
	var hash []byte
	var revoked, invalidated bool
	e = q.QueryRow(ctx, distributionNativeSQL(q, `SELECT G.ID,G.NODE_ID,G.TENANT_ID,G.WORKSPACE_ID,G.CREDENTIAL_HASH,CASE WHEN G.REVOKED_AT IS NOT NULL THEN 1 ELSE 0 END,CASE WHEN G.AUTHORITY_INVALIDATED_AT IS NOT NULL THEN 1 ELSE 0 END FROM GATEWAY_INSTANCES G JOIN INSTALLATION_STATE I ON I.TENANT_ID=G.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND G.ID=?`+mysqlSuffix, `SELECT g.id::text,g.node_id::text,g.tenant_id::text,g.workspace_id::text,g.credential_hash,g.revoked_at IS NOT NULL,g.authority_invalidated_at IS NOT NULL FROM gateway_instances g JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND g.id=$1`+suffix, `SELECT g.id,g.node_id,g.tenant_id,g.workspace_id,g.credential_hash,g.revoked_at IS NOT NULL,g.authority_invalidated_at IS NOT NULL FROM gateway_instances g JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND g.id=?`+mysqlSuffix, id)).Scan(&out.gateway, &out.node, &out.tenant, &out.workspace, &hash, &revoked, &invalidated)
	if errors.Is(e, sql.ErrNoRows) {
		return machineScope{}, fail("GATEWAY_UNAUTHORIZED")
	}
	if e != nil {
		return machineScope{}, e
	}
	if invalidated || (!revokedAllowed && revoked) || !same(hash, s.digest("gateway-credential", id, token)) {
		return machineScope{}, fail("GATEWAY_UNAUTHORIZED")
	}
	return out, nil
}
func (s *Service) keyAuthority(ctx context.Context, q transaction.Executor, scope machineScope, raw string, lock bool) (keyAuthority, error) {
	var out keyAuthority
	id, token, e := parseCredential(apiKeyPrefix, raw)
	if e != nil {
		return out, fail("API_KEY_INVALID")
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	e = q.QueryRow(ctx, distributionNativeSQL(q, `SELECT ID,CREATED_BY,ALIAS_ID,REQUESTED_EPOCH,CASE WHEN REVOKED_AT IS NOT NULL THEN 1 ELSE 0 END,CASE WHEN EXPIRES_AT<=CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END,SECRET_HASH,MEMBERSHIP_AUTHORITY,CASE WHEN LAST_USED_AT IS NOT NULL THEN 1 ELSE 0 END FROM API_KEYS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`+suffix, "SELECT id::text,created_by::text,alias_id::text,requested_epoch,revoked_at IS NOT NULL,expires_at<=clock_timestamp(),secret_hash,membership_authority::text,last_used_at IS NOT NULL FROM api_keys WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3"+suffix, "SELECT id,created_by,alias_id,requested_epoch,revoked_at IS NOT NULL,expires_at<=UTC_TIMESTAMP(6),secret_hash,membership_authority,last_used_at IS NOT NULL FROM api_keys WHERE tenant_id=? AND workspace_id=? AND id=?"+suffix, scope.tenant, scope.workspace, id)).Scan(&out.id, &out.owner, &out.alias, &out.epoch, &out.revoked, &out.expired, &out.hash, &out.membershipAuthority, &out.used)
	if errors.Is(e, sql.ErrNoRows) {
		return keyAuthority{}, fail("API_KEY_INVALID")
	}
	if e != nil {
		return keyAuthority{}, e
	}
	ordinary := same(out.hash, s.digest(ordinaryKeyPurpose, id, token))
	out.operation = same(out.hash, s.digest(operationKeyPurpose, id, token))
	if out.revoked || !ordinary && !out.operation {
		return keyAuthority{}, fail("API_KEY_INVALID")
	}
	if out.expired {
		return keyAuthority{}, fail("API_KEY_EXPIRED")
	}
	return out, nil
}

func (s *Service) authorized(ctx context.Context, gateway, credential, rawKey string, licenseRequired bool, fn func(context.Context, transaction.Executor, machineScope, keyAuthority, Alias) error) error {
	var scope machineScope
	var candidate keyAuthority
	e := s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var e error
		scope, e = s.gatewayAuthority(ctx, q, gateway, credential, false, false)
		if e != nil {
			return e
		}
		candidate, e = s.keyAuthority(ctx, q, scope, rawKey, false)
		return e
	})
	if e != nil {
		return e
	}
	// Both credentials are genuine before observing the license. Commit that
	// independent observation before acquiring any business transaction/lock.
	// The original transaction below rechecks all current authority unchanged.
	if licenseRequired {
		if candidate.operation {
			e = s.Identity.RequireBusinessIntegrity(ctx)
		} else {
			e = s.Identity.RequireNewBusiness(ctx)
		}
		if e != nil {
			return e
		}
	}
	return s.storageTransaction(ctx, func(ctx context.Context, tx transaction.Executor) error {
		var epoch int64
		var status string
		if e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT AUTH_EPOCH,STATUS FROM USERS WHERE ID=? FOR UPDATE`, []any{candidate.owner}, "SELECT auth_epoch,status FROM users WHERE id=$1 FOR UPDATE", `SELECT auth_epoch,status FROM users WHERE id=? FOR UPDATE`, []any{candidate.owner}, []any{candidate.owner})).Scan(&epoch, &status); e != nil {
			return e
		}
		var active, member bool
		e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CASE WHEN W.STATUS='active' AND T.STATUS='active' THEN 1 ELSE 0 END FROM WORKSPACES W JOIN TENANTS T ON T.ID=W.TENANT_ID JOIN INSTALLATION_STATE I ON I.TENANT_ID=W.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND W.TENANT_ID=? AND W.ID=? FOR UPDATE`, []any{scope.tenant, scope.workspace}, "SELECT w.status='active' AND t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR NO KEY UPDATE OF w", `SELECT w.status='active' AND t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, []any{scope.tenant, scope.workspace}, []any{scope.tenant, scope.workspace})).Scan(&active)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("FORBIDDEN")
		}
		if e != nil {
			return e
		}
		if !active {
			return fail("FORBIDDEN")
		}
		if e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM WORKSPACE_MEMBERSHIPS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND USER_ID=?) THEN 1 ELSE 0 END FROM DUAL`, []any{scope.tenant, scope.workspace, candidate.owner}, "SELECT EXISTS(SELECT 1 FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3)", `SELECT EXISTS(SELECT 1 FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?)`, []any{scope.tenant, scope.workspace, candidate.owner}, []any{scope.tenant, scope.workspace, candidate.owner})).Scan(&member); e != nil {
			return e
		}
		if !member {
			return fail("FORBIDDEN")
		}
		membershipAuthority, e := s.Workspaces.MembershipAuthorityWithin(ctx, tx, scope.tenant, scope.workspace, candidate.owner)
		if e != nil {
			return e
		}
		var nodeUsable bool
		e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CASE WHEN STATUS='active' AND INFERENCE_FENCED_AT IS NULL AND LAST_HEARTBEAT_AT>TIMESTAMPADD(SECOND,-?,CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM MANAGED_NODES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE`, []any{s.Identity.Config.Policy().Nodes.OnlineSeconds, scope.tenant, scope.workspace, scope.node}, "SELECT status='active' AND inference_fenced_at IS NULL AND last_heartbeat_at>clock_timestamp()-make_interval(secs=>$4) FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT status='active' AND inference_fenced_at IS NULL AND last_heartbeat_at>TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{scope.tenant, scope.workspace, scope.node, s.Identity.Config.Policy().Nodes.OnlineSeconds}, []any{s.Identity.Config.Policy().Nodes.OnlineSeconds, scope.tenant, scope.workspace, scope.node})).Scan(&nodeUsable)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("MODEL_ALIAS_UNAVAILABLE")
		}
		if e != nil {
			return e
		}
		if !nodeUsable {
			return fail("MODEL_ALIAS_UNAVAILABLE")
		}
		fresh, e := s.gatewayAuthority(ctx, tx, gateway, credential, false, true)
		if e != nil {
			return e
		}
		if fresh != scope {
			return fail("GATEWAY_UNAUTHORIZED")
		}
		key, e := s.keyAuthority(ctx, tx, scope, rawKey, true)
		if e != nil {
			return e
		}
		if key.owner != candidate.owner || key.operation != candidate.operation || key.epoch != epoch || status != "active" || key.membershipAuthority != membershipAuthority {
			return fail("API_KEY_INVALID")
		}
		alias, e := scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND GATEWAY_ID=? FOR UPDATE", []any{scope.tenant, scope.workspace, key.alias, scope.gateway}, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND gateway_id=$4 FOR UPDATE", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? AND gateway_id=? FOR UPDATE`, []any{scope.tenant, scope.workspace, key.alias, scope.gateway}, []any{scope.tenant, scope.workspace, key.alias, scope.gateway})))
		if e != nil {
			return e
		}
		w := workspaces.Workspace{ID: scope.workspace, TenantID: scope.tenant, Status: "active"}
		if e = s.aliasReadiness(ctx, tx, w, &alias); e != nil {
			return e
		}
		if !alias.Available {
			return fail("MODEL_ALIAS_UNAVAILABLE")
		}
		if e = fn(ctx, tx, scope, key, alias); e != nil {
			return e
		}
		return nil
	})
}

func binding(alias Alias) wire.AliasBinding {
	// Only the approved backend model is exposed to Rust; no caller chooses an endpoint or path.
	return wire.AliasBinding{ID: alias.ID, Name: alias.Name, Created: uint64(alias.CreatedAt.Unix()), DeploymentID: alias.DeploymentID, DeploymentRevision: uint64(alias.DeploymentRevision), BackendModel: agentwire.InferenceModelName}
}

func (s *Service) AuthorizeModels(ctx context.Context, gateway, credential, rawKey string) (wire.ModelsAuthorization, error) {
	var out wire.ModelsAuthorization
	e := s.authorized(ctx, gateway, credential, rawKey, false, func(_ context.Context, _ transaction.Executor, scope machineScope, _ keyAuthority, alias Alias) error {
		out = wire.ModelsAuthorization{GatewayID: scope.gateway, NodeID: scope.node, WorkspaceID: scope.workspace, Models: []wire.AliasBinding{binding(alias)}}
		return nil
	})
	if e != nil {
		return wire.ModelsAuthorization{}, e
	}
	return out, nil
}

func (s *Service) Admit(ctx context.Context, gateway, credential string, input wire.AdmissionRequest, request string) (wire.Admission, error) {
	if input.Validate() != nil {
		return wire.Admission{}, fail("VALIDATION_FAILED")
	}
	var out wire.Admission
	e := s.authorized(ctx, gateway, credential, input.APIKey, true, func(ctx context.Context, tx transaction.Executor, scope machineScope, key keyAuthority, alias Alias) error {
		// The original key lock serializes concurrent attempts. Consume only in
		// the same commit as the request, capacity hold and audit below.
		if key.operation && key.used {
			return fail("API_KEY_INVALID")
		}
		if input.Model != alias.Name {
			return fail("MODEL_ALIAS_NOT_FOUND")
		}
		policy := s.Identity.Config.Policy().Distribution
		if input.MaximumOutputTokens > uint32(policy.OutputTokens) {
			return fail("VALIDATION_FAILED")
		}
		var duplicate bool
		if e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM GATEWAY_REQUESTS WHERE ID=?) THEN 1 ELSE 0 END FROM DUAL`, []any{input.RequestID}, "SELECT EXISTS(SELECT 1 FROM gateway_requests WHERE id=$1)", `SELECT EXISTS(SELECT 1 FROM gateway_requests WHERE id=?)`, []any{input.RequestID}, []any{input.RequestID})).Scan(&duplicate); e != nil {
			return e
		}
		if duplicate {
			return fail("GATEWAY_REQUEST_EXISTS")
		}
		var held int
		if e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT COUNT(*) FROM GATEWAY_REQUESTS WHERE DEPLOYMENT_ID=? AND CAPACITY_HELD=1`, []any{alias.DeploymentID}, "SELECT count(*) FROM gateway_requests WHERE deployment_id=$1 AND capacity_held", `SELECT count(*) FROM gateway_requests WHERE deployment_id=? AND capacity_held`, []any{alias.DeploymentID}, []any{alias.DeploymentID})).Scan(&held); e != nil {
			return e
		}
		if held >= policy.MaxConcurrentRequests {
			return &identity.RateError{RetryAfter: policy.RequestSeconds}
		}
		if _, e := s.Identity.AdmitWithin(ctx, tx, identity.Limit{Purpose: "gateway.key.admission", Identifier: key.id, Count: policy.RequestsPerWindow, Window: config.Seconds(policy.RateWindowSeconds)}); e != nil {
			return e
		}
		_, e := distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO GATEWAY_REQUESTS(ID,TENANT_ID,WORKSPACE_ID,NODE_ID,GATEWAY_ID,API_KEY_ID,ACTOR_USER_ID,ACTOR_EPOCH,ALIAS_ID,ALIAS_REVISION,DEPLOYMENT_ID,DEPLOYMENT_REVISION,MAXIMUM_OUTPUT_TOKENS,MAXIMUM_CONTEXT_TOKENS,MAXIMUM_REQUEST_SECONDS,STREAM,STATE,ADMITTED_AT,DEADLINE_AT) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'admitted',CURRENT_TIMESTAMP(6),TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)))`, []any{input.RequestID, scope.tenant, scope.workspace, scope.node, scope.gateway, key.id, key.owner, key.epoch, alias.ID, alias.Revision, alias.DeploymentID, alias.DeploymentRevision, input.MaximumOutputTokens, policy.ContextTokens, policy.RequestSeconds, input.Stream, policy.RequestSeconds}, "INSERT INTO gateway_requests(id,tenant_id,workspace_id,node_id,gateway_id,api_key_id,actor_user_id,actor_epoch,alias_id,alias_revision,deployment_id,deployment_revision,maximum_output_tokens,maximum_context_tokens,maximum_request_seconds,stream,state,admitted_at,deadline_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'admitted',clock_timestamp(),clock_timestamp()+make_interval(secs=>$15::integer))", `INSERT INTO gateway_requests(id,tenant_id,workspace_id,node_id,gateway_id,api_key_id,actor_user_id,actor_epoch,alias_id,alias_revision,deployment_id,deployment_revision,maximum_output_tokens,maximum_context_tokens,maximum_request_seconds,stream,state,admitted_at,deadline_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'admitted',UTC_TIMESTAMP(6),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))`, []any{input.RequestID, scope.tenant, scope.workspace, scope.node, scope.gateway, key.id, key.owner, key.epoch, alias.ID, alias.Revision, alias.DeploymentID, alias.DeploymentRevision, input.MaximumOutputTokens, policy.ContextTokens, policy.RequestSeconds, input.Stream}, []any{input.RequestID, scope.tenant, scope.workspace, scope.node, scope.gateway, key.id, key.owner, key.epoch, alias.ID, alias.Revision, alias.DeploymentID, alias.DeploymentRevision, input.MaximumOutputTokens, policy.ContextTokens, policy.RequestSeconds, input.Stream, policy.RequestSeconds}))
		if e != nil {
			return e
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE API_KEYS SET LAST_USED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, []any{key.id}, "UPDATE api_keys SET last_used_at=clock_timestamp() WHERE id=$1", `UPDATE api_keys SET last_used_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{key.id}, []any{key.id})); e != nil {
			return e
		}
		if e = s.machineAudit(ctx, tx, scope, &key.owner, "gateway.request_admitted", input.RequestID, request); e != nil {
			return e
		}
		out = wire.Admission{RequestID: input.RequestID, GatewayID: scope.gateway, NodeID: scope.node, WorkspaceID: scope.workspace, Binding: binding(alias), MaximumContextTokens: uint32(policy.ContextTokens), MaximumRequestSeconds: uint64(policy.RequestSeconds)}
		return nil
	})
	if e != nil {
		return wire.Admission{}, e
	}
	return out, nil
}

func (s *Service) machineAudit(ctx context.Context, tx transaction.Executor, scope machineScope, actor *string, action, id, request string) error {
	_, e := distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,TENANT_ID,WORKSPACE_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REQUEST_ID) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,'system',?,'success','gateway_request',?,?)`, []any{uuid.NewString(), actor, scope.tenant, scope.workspace, action, id, request}, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'system',$5,'success','gateway_request',$6,$7)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'system',?,'success','gateway_request',?,?)`, []any{uuid.NewString(), actor, scope.tenant, scope.workspace, action, id, request}, []any{uuid.NewString(), actor, scope.tenant, scope.workspace, action, id, request}))
	return e
}
