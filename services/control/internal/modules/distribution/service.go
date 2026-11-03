package distribution

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/url"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strconv"
	"strings"
	"unicode/utf8"
)

func fail(code string) error     { return errors.New(code) }
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func reason(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 500
}
func fingerprint(actor string, input any) []byte {
	b, _ := json.Marshal(struct {
		Actor string
		Input any
	}{actor, input})
	h := sha256.Sum256(b)
	return h[:]
}
func same(first, second []byte) bool {
	return len(first) == sha256.Size && len(second) == sha256.Size && subtle.ConstantTimeCompare(first, second) == 1
}
func (s *Service) audit(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, id, explanation, request string) error {
	_, e := distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,TENANT_ID,WORKSPACE_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REASON,REQUEST_ID) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,?,?,'success',?,?,?,?)`, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, explanation, request}, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success',$7,$8,$9,$10)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success',?,?,?,?)`, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, explanation, request}, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, explanation, request}))
	return e
}
func originDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (s *Service) digest(purpose, id, token string) []byte {
	return security.MAC(s.Identity.HMACKey, "m07-"+purpose, "v1", id, token)
}

const gatewayFields = `id::text,workspace_id::text,node_id::text,name,client_origin,created_by::text,CASE WHEN authority_invalidated_at IS NOT NULL THEN 'invalidated' WHEN revoked_at IS NOT NULL THEN 'revoked' ELSE 'active' END,created_at,revoked_at`

func scanGateway(row transaction.Row) (Gateway, error) {
	var g Gateway
	e := row.Scan(&g.ID, &g.WorkspaceID, &g.NodeID, &g.Name, &g.ClientOrigin, &g.CreatedBy, &g.State, &g.CreatedAt, &g.RevokedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Gateway{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Gateway{}, e
	}
	g.CreatedAt = g.CreatedAt.UTC()
	g.RevokedAt = distributionUTCPointer(g.RevokedAt)
	return g, nil
}

func (s *Service) Provision(ctx context.Context, a identity.Auth, input ProvisionRequest, key, request string) (Provisioned, error) {
	if !admin(a) {
		return Provisioned{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(input.NodeID) || !wire.ValidID(key) || !reason(input.Reason) || !validClientOrigin(input.ClientOrigin) {
		return Provisioned{}, fail("VALIDATION_FAILED")
	}
	name, e := security.Name(input.Name)
	if e != nil {
		return Provisioned{}, e
	}
	input.Name = name
	var out Provisioned
	e = s.transaction(ctx, a, input.WorkspaceID, true, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		expected := fingerprint(a.Me.User.ID, input)
		var existing string
		var stored []byte
		e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT ID,FINGERPRINT FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND CREATED_BY=? AND IDEMPOTENCY_KEY=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, "SELECT id::text,fingerprint FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND idempotency_key=$4", `SELECT id,fingerprint FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND created_by=? AND idempotency_key=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, a.Me.User.ID, key})).Scan(&existing, &stored)
		if e == nil {
			if !same(expected, stored) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out.Gateway, e = scanGateway(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE ID=?", []any{existing}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE id=$1", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE id=?`, []any{existing}, []any{existing})))
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var usable bool
		e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CASE WHEN STATUS='active' AND EXECUTION_PROFILE IN (?,?,?) AND INFERENCE_FENCED_AT IS NULL THEN 1 ELSE 0 END FROM MANAGED_NODES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE`, []any{agentwire.DockerInferenceProfile, agentwire.DockerAIProfile, agentwire.DockerModelLifecycleProfile, w.TenantID, w.ID, input.NodeID}, "SELECT status='active' AND execution_profile IN ($4,$5,$6) AND inference_fenced_at IS NULL FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT status='active' AND execution_profile IN (?,?,?) AND inference_fenced_at IS NULL FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, input.NodeID, agentwire.DockerInferenceProfile, agentwire.DockerAIProfile, agentwire.DockerModelLifecycleProfile}, []any{agentwire.DockerInferenceProfile, agentwire.DockerAIProfile, agentwire.DockerModelLifecycleProfile, w.TenantID, w.ID, input.NodeID})).Scan(&usable)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if !usable {
			return fail("GATEWAY_NODE_UNAVAILABLE")
		}
		var exists bool
		if e = tx.QueryRow(ctx, distributionNativeSQL(tx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND NODE_ID=? AND REVOKED_AT IS NULL AND AUTHORITY_INVALIDATED_AT IS NULL) THEN 1 ELSE 0 END FROM DUAL`, "SELECT EXISTS(SELECT 1 FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND revoked_at IS NULL AND authority_invalidated_at IS NULL)", "SELECT EXISTS(SELECT 1 FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND node_id=? AND revoked_at IS NULL AND authority_invalidated_at IS NULL)", w.TenantID, w.ID, input.NodeID)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fail("GATEWAY_INSTANCE_EXISTS")
		}
		// Existing idempotency replays above retain their historical binding.
		// Select transport only for a new identity, before generating its secret.
		machineOrigin, e := s.Identity.Config.MachineOrigin()
		if e != nil {
			return e
		}
		controlOriginHash := originDigest(machineOrigin)
		id := uuid.NewString()
		token := security.Token()
		raw := gatewayCredentialPrefix + "." + id + "." + token
		_, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO GATEWAY_INSTANCES(ID,TENANT_ID,WORKSPACE_ID,NODE_ID,NAME,CREDENTIAL_HASH,CREATED_BY,REQUESTED_EPOCH,IDEMPOTENCY_KEY,FINGERPRINT,CONTROL_ORIGIN_SHA256,CLIENT_ORIGIN) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, []any{id, w.TenantID, w.ID, input.NodeID, input.Name, s.digest("gateway-credential", id, token), a.Me.User.ID, a.Epoch, key, expected, controlOriginHash, input.ClientOrigin}, "INSERT INTO gateway_instances(id,tenant_id,workspace_id,node_id,name,credential_hash,created_by,requested_epoch,idempotency_key,fingerprint,control_origin_sha256,client_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)", `INSERT INTO gateway_instances(id,tenant_id,workspace_id,node_id,name,credential_hash,created_by,requested_epoch,idempotency_key,fingerprint,control_origin_sha256,client_origin) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, []any{id, w.TenantID, w.ID, input.NodeID, input.Name, s.digest("gateway-credential", id, token), a.Me.User.ID, a.Epoch, key, expected, controlOriginHash, input.ClientOrigin}, []any{id, w.TenantID, w.ID, input.NodeID, input.Name, s.digest("gateway-credential", id, token), a.Me.User.ID, a.Epoch, key, expected, controlOriginHash, input.ClientOrigin}))
		if e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.instance_created", "gateway_instance", id, input.Reason, request); e != nil {
			return e
		}
		out.Gateway, e = scanGateway(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE ID=?", []any{id}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE id=$1", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE id=?`, []any{id}, []any{id})))
		if e != nil {
			return e
		}
		out.Secret = &raw
		out.SecretAvailable = true
		out.Created = true
		return nil
	})
	if e != nil {
		return Provisioned{}, e
	}
	return out, nil
}

func (s *Service) Gateways(ctx context.Context, a identity.Auth, workspace string) ([]Gateway, error) {
	if !admin(a) {
		return nil, fail("ACCESS_DENIED")
	}
	out := []Gateway{}
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		rows, e := tx.Query(ctx, distributionPageStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? ORDER BY CREATED_AT DESC,ID DESC", []any{w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE tenant_id=? AND workspace_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, []any{w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize}, []any{w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize}))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			g, e := scanGateway(rows)
			if e != nil {
				return e
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	if e != nil {
		return []Gateway{}, e
	}
	return out, nil
}

func (s *Service) RevokeGateway(ctx context.Context, a identity.Auth, workspace, id, explanation, request string) (Gateway, error) {
	if !admin(a) {
		return Gateway{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(id) || !reason(explanation) {
		return Gateway{}, fail("VALIDATION_FAILED")
	}
	var out Gateway
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		current, e := scanGateway(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, id}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
		if e != nil {
			return e
		}
		if current.State != "active" {
			out = current
			return nil
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE GATEWAY_INSTANCES SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON=? WHERE ID=?`, []any{explanation, id}, "UPDATE gateway_instances SET revoked_at=clock_timestamp(),revoke_reason=$2 WHERE id=$1", `UPDATE gateway_instances SET revoked_at=UTC_TIMESTAMP(6),revoke_reason=? WHERE id=?`, []any{id, explanation}, []any{explanation, id})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.instance_revoked", "gateway_instance", id, explanation, request); e != nil {
			return e
		}
		out, e = scanGateway(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE ID=?", []any{id}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE id=$1", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE id=?`, []any{id}, []any{id})))
		return e
	})
	if e != nil {
		return Gateway{}, e
	}
	return out, nil
}

// validClientOrigin validates advertised metadata only; it never authorizes outbound requests.
func validClientOrigin(value string) bool {
	if len(value) > 2048 || strings.TrimSpace(value) != value {
		return false
	}
	parsed, e := url.Parse(value)
	if e != nil {
		return false
	}
	if port := parsed.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return e == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == "" && parsed.Path == "" && parsed.RawPath == "" && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && parsed.String() == value && !strings.ContainsAny(value, "\\ \t\r\n")
}
