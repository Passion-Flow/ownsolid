package distribution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

type recoveryScope struct {
	gateway, node, deployment, origin string
	held                              bool
	active                            *string
	gatewayState                      string
}

func recoveryRequest(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string) (recoveryScope, error) {
	var out recoveryScope
	e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT R.GATEWAY_ID,R.NODE_ID,R.DEPLOYMENT_ID,R.CAPACITY_HELD,R.ACTIVE_RECOVERY_ID,G.CONTROL_ORIGIN_SHA256,CASE WHEN G.AUTHORITY_INVALIDATED_AT IS NOT NULL THEN 'invalidated' WHEN G.REVOKED_AT IS NOT NULL THEN 'revoked' ELSE 'active' END FROM GATEWAY_REQUESTS R JOIN GATEWAY_INSTANCES G ON G.TENANT_ID=R.TENANT_ID AND G.WORKSPACE_ID=R.WORKSPACE_ID AND G.ID=R.GATEWAY_ID WHERE R.TENANT_ID=? AND R.WORKSPACE_ID=? AND R.ID=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, `SELECT r.gateway_id::text,r.node_id::text,r.deployment_id::text,r.capacity_held,r.active_recovery_id::text,g.control_origin_sha256,CASE WHEN g.authority_invalidated_at IS NOT NULL THEN 'invalidated' WHEN g.revoked_at IS NOT NULL THEN 'revoked' ELSE 'active' END FROM gateway_requests r JOIN gateway_instances g ON g.tenant_id=r.tenant_id AND g.workspace_id=r.workspace_id AND g.id=r.gateway_id WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.id=$3 FOR UPDATE OF r,g`, `SELECT r.gateway_id,r.node_id,r.deployment_id,r.capacity_held,r.active_recovery_id,g.control_origin_sha256,CASE WHEN g.authority_invalidated_at IS NOT NULL THEN 'invalidated' WHEN g.revoked_at IS NOT NULL THEN 'revoked' ELSE 'active' END FROM gateway_requests r JOIN gateway_instances g ON g.tenant_id=r.tenant_id AND g.workspace_id=r.workspace_id AND g.id=r.gateway_id WHERE r.tenant_id=? AND r.workspace_id=? AND r.id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})).Scan(&out.gateway, &out.node, &out.deployment, &out.held, &out.active, &out.origin, &out.gatewayState)
	if errors.Is(e, sql.ErrNoRows) {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	return out, e
}

const recoveryFields = `id::text,request_id::text,workspace_id::text,CASE WHEN confirmed_at IS NOT NULL THEN 'completed' WHEN invalidated_at IS NOT NULL THEN 'invalidated' WHEN superseded_at IS NOT NULL THEN 'superseded' WHEN expires_at<=clock_timestamp() THEN 'expired' WHEN fence_accepted_at IS NOT NULL THEN 'fenced' ELSE 'pending_fence' END,created_at,expires_at,fence_accepted_at,confirmed_at,fence_receipt`

func scanRecovery(row transaction.Row) (RecoveryChallenge, error) {
	var out RecoveryChallenge
	var raw []byte
	e := row.Scan(&out.ID, &out.RequestID, &out.WorkspaceID, &out.State, &out.CreatedAt, &out.ExpiresAt, &out.FenceAcceptedAt, &out.ConfirmedAt, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return RecoveryChallenge{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		if e != nil {
			return RecoveryChallenge{}, e
		}
		return out, nil
	}
	if raw != nil {
		receipt, e := wire.DecodeFenceReceipt(raw)
		if e != nil {
			return RecoveryChallenge{}, fail("STORAGE_UNAVAILABLE")
		}
		out.FenceReceipt = &receipt
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.ExpiresAt = out.ExpiresAt.UTC()
	out.FenceAcceptedAt = distributionUTCPointer(out.FenceAcceptedAt)
	out.ConfirmedAt = distributionUTCPointer(out.ConfirmedAt)
	return out, nil
}
func challengeInScope(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, request, id string) (RecoveryChallenge, error) {
	return scanRecovery(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeRecoveryFields+" FROM GATEWAY_RECOVERIES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND REQUEST_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, request, id}, "SELECT "+recoveryFields+" FROM gateway_recoveries WHERE tenant_id=$1 AND workspace_id=$2 AND request_id=$3 AND id=$4 FOR UPDATE", `SELECT `+mysqlRecoveryFields+` FROM gateway_recoveries WHERE tenant_id=? AND workspace_id=? AND request_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, request, id}, []any{w.TenantID, w.ID, request, id})))
}

func (s *Service) CreateRecoveryChallenge(ctx context.Context, a identity.Auth, workspace, requestID, explanation, intent, request string) (RecoveryChallenge, error) {
	if !admin(a) {
		return RecoveryChallenge{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(requestID) || !wire.ValidID(intent) || !reason(explanation) {
		return RecoveryChallenge{}, fail("VALIDATION_FAILED")
	}
	var out RecoveryChallenge
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		scope, e := recoveryRequest(ctx, tx, w, requestID)
		if e != nil {
			return e
		}
		expected := fingerprint(a.Me.User.ID, struct{ RequestID, Reason string }{requestID, explanation})
		var existing string
		var stored []byte
		e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT ID,FINGERPRINT FROM GATEWAY_RECOVERIES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND REQUEST_ID=? AND REQUESTED_BY=? AND IDEMPOTENCY_KEY=?`, []any{w.TenantID, w.ID, requestID, a.Me.User.ID, intent}, "SELECT id::text,fingerprint FROM gateway_recoveries WHERE tenant_id=$1 AND workspace_id=$2 AND request_id=$3 AND requested_by=$4 AND idempotency_key=$5", `SELECT id,fingerprint FROM gateway_recoveries WHERE tenant_id=? AND workspace_id=? AND request_id=? AND requested_by=? AND idempotency_key=?`, []any{w.TenantID, w.ID, requestID, a.Me.User.ID, intent}, []any{w.TenantID, w.ID, requestID, a.Me.User.ID, intent})).Scan(&existing, &stored)
		if e == nil {
			if !same(expected, stored) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out, e = challengeInScope(ctx, tx, w, requestID, existing)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if !scope.held {
			return fail("GATEWAY_RECOVERY_NOT_REQUIRED")
		}
		if scope.gatewayState == "active" {
			return fail("GATEWAY_RECOVERY_REQUIRES_REVOCATION")
		}
		if scope.active != nil {
			if _, e = tx.Exec(ctx, distributionNativeStatement(tx, `UPDATE GATEWAY_RECOVERIES SET SUPERSEDED_AT=COALESCE(SUPERSEDED_AT,CURRENT_TIMESTAMP(6)) WHERE ID=? AND CONFIRMED_AT IS NULL`, []any{*scope.active}, "UPDATE gateway_recoveries SET superseded_at=COALESCE(superseded_at,clock_timestamp()) WHERE id=$1 AND confirmed_at IS NULL", `UPDATE gateway_recoveries SET superseded_at=COALESCE(superseded_at,UTC_TIMESTAMP(6)) WHERE id=? AND confirmed_at IS NULL`, []any{*scope.active}, []any{*scope.active})); e != nil {
				return e
			}
		}
		id := uuid.NewString()
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO GATEWAY_RECOVERIES(ID,TENANT_ID,WORKSPACE_ID,REQUEST_ID,REQUESTED_BY,REASON,IDEMPOTENCY_KEY,FINGERPRINT,EXPIRES_AT) VALUES(?,?,?,?,?,?,?,?,TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)))`, []any{id, w.TenantID, w.ID, requestID, a.Me.User.ID, explanation, intent, expected, s.Identity.Config.Policy().Distribution.RecoverySeconds}, "INSERT INTO gateway_recoveries(id,tenant_id,workspace_id,request_id,requested_by,reason,idempotency_key,fingerprint,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()+make_interval(secs=>$9))", `INSERT INTO gateway_recoveries(id,tenant_id,workspace_id,request_id,requested_by,reason,idempotency_key,fingerprint,expires_at) VALUES(?,?,?,?,?,?,?,?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))`, []any{id, w.TenantID, w.ID, requestID, a.Me.User.ID, explanation, intent, expected, s.Identity.Config.Policy().Distribution.RecoverySeconds}, []any{id, w.TenantID, w.ID, requestID, a.Me.User.ID, explanation, intent, expected, s.Identity.Config.Policy().Distribution.RecoverySeconds})); e != nil {
			return e
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE GATEWAY_REQUESTS SET ACTIVE_RECOVERY_ID=? WHERE ID=?`, []any{id, requestID}, "UPDATE gateway_requests SET active_recovery_id=$2 WHERE id=$1", `UPDATE gateway_requests SET active_recovery_id=? WHERE id=?`, []any{requestID, id}, []any{id, requestID})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.recovery_challenge_created", "gateway_recovery", id, explanation, request); e != nil {
			return e
		}
		out, e = challengeInScope(ctx, tx, w, requestID, id)
		return e
	})
	if e != nil {
		return RecoveryChallenge{}, e
	}
	return out, nil
}

func (s *Service) RecoveryChallenge(ctx context.Context, a identity.Auth, workspace, requestID, id string) (RecoveryChallenge, error) {
	if !admin(a) {
		return RecoveryChallenge{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(requestID) || !wire.ValidID(id) {
		return RecoveryChallenge{}, fail("VALIDATION_FAILED")
	}
	var out RecoveryChallenge
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = challengeInScope(ctx, tx, w, requestID, id)
		return e
	})
	if e != nil {
		return RecoveryChallenge{}, e
	}
	return out, nil
}

func (s *Service) AcceptRecoveryFence(ctx context.Context, a identity.Auth, workspace, requestID, id string, receipt wire.FenceReceipt, explanation, request string) (RecoveryChallenge, error) {
	if !admin(a) {
		return RecoveryChallenge{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(requestID) || !wire.ValidID(id) || receipt.Validate() != nil || receipt.ChallengeID != id || !reason(explanation) {
		return RecoveryChallenge{}, fail("VALIDATION_FAILED")
	}
	var out RecoveryChallenge
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		scope, e := recoveryRequest(ctx, tx, w, requestID)
		if e != nil {
			return e
		}
		if scope.active == nil || *scope.active != id {
			return fail("GATEWAY_RECOVERY_STATE_CONFLICT")
		}
		current, e := challengeInScope(ctx, tx, w, requestID, id)
		if e != nil {
			return e
		}
		if current.FenceReceipt != nil {
			before, _ := current.FenceReceipt.Digest()
			after, _ := receipt.Digest()
			if before != after {
				return fail("GATEWAY_FENCE_RECEIPT_CONFLICT")
			}
			out = current
			return nil
		}
		if !scope.held {
			return fail("GATEWAY_RECOVERY_NOT_REQUIRED")
		}
		if scope.gatewayState == "active" {
			return fail("GATEWAY_RECOVERY_REQUIRES_REVOCATION")
		}
		if current.State != "pending_fence" {
			return fail("GATEWAY_RECOVERY_STATE_CONFLICT")
		}
		if receipt.GatewayID != scope.gateway || receipt.NodeID != scope.node || receipt.ControlOriginSHA256 != scope.origin {
			return fail("GATEWAY_FENCE_SCOPE_CONFLICT")
		}
		raw, _ := json.Marshal(receipt)
		digest, _ := receipt.Digest()
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE GATEWAY_RECOVERIES SET FENCE_RECEIPT=?,FENCE_RECEIPT_SHA256=?,FENCE_ACCEPTED_AT=CURRENT_TIMESTAMP(6),FENCE_ACCEPTED_BY=? WHERE ID=?`, []any{string(raw), digest, a.Me.User.ID, id}, "UPDATE gateway_recoveries SET fence_receipt=$2,fence_receipt_sha256=$3,fence_accepted_at=clock_timestamp(),fence_accepted_by=$4 WHERE id=$1", `UPDATE gateway_recoveries SET fence_receipt=CAST(? AS JSON),fence_receipt_sha256=?,fence_accepted_at=UTC_TIMESTAMP(6),fence_accepted_by=? WHERE id=?`, []any{id, raw, digest, a.Me.User.ID}, []any{raw, digest, a.Me.User.ID, id})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.recovery_fence_accepted", "gateway_recovery", id, explanation, request); e != nil {
			return e
		}
		out, e = challengeInScope(ctx, tx, w, requestID, id)
		return e
	})
	if e != nil {
		return RecoveryChallenge{}, e
	}
	return out, nil
}

func (s *Service) ConfirmRecovery(ctx context.Context, a identity.Auth, workspace, requestID, id string, deploymentRevision int64, explanation, request string) (RecoveryChallenge, error) {
	if !admin(a) {
		return RecoveryChallenge{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(requestID) || !wire.ValidID(id) || deploymentRevision < 1 || deploymentRevision >= maximumRevision || !reason(explanation) {
		return RecoveryChallenge{}, fail("VALIDATION_FAILED")
	}
	var out RecoveryChallenge
	expected := fingerprint(a.Me.User.ID, struct {
		RequestID, Challenge string
		DeploymentRevision   int64
		Reason               string
	}{requestID, id, deploymentRevision, explanation})
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		scope, e := recoveryRequest(ctx, tx, w, requestID)
		if e != nil {
			return e
		}
		current, e := challengeInScope(ctx, tx, w, requestID, id)
		if e != nil {
			return e
		}
		if current.ConfirmedAt != nil {
			var stored []byte
			if e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT CONFIRMATION_FINGERPRINT FROM GATEWAY_RECOVERIES WHERE ID=?`, []any{id}, "SELECT confirmation_fingerprint FROM gateway_recoveries WHERE id=$1", `SELECT confirmation_fingerprint FROM gateway_recoveries WHERE id=?`, []any{id}, []any{id})).Scan(&stored); e != nil {
				return e
			}
			if !same(stored, expected) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = current
			return nil
		}
		if scope.active == nil || *scope.active != id || current.State != "fenced" || current.FenceAcceptedAt == nil {
			return fail("GATEWAY_RECOVERY_STATE_CONFLICT")
		}
		if !scope.held {
			return fail("GATEWAY_RECOVERY_NOT_REQUIRED")
		}
		if scope.gatewayState == "active" {
			return fail("GATEWAY_RECOVERY_REQUIRES_REVOCATION")
		}
		if e = s.Deployments.ConfirmGatewayStopWithin(ctx, tx, w.TenantID, w.ID, scope.node, scope.deployment, deploymentRevision, *current.FenceAcceptedAt); e != nil {
			return e
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE GATEWAY_REQUESTS SET CAPACITY_HELD=0,STATE=CASE WHEN OUTCOME IS NULL THEN 'reconciled' ELSE 'reported' END,RESOLVED_AT=COALESCE(RESOLVED_AT,CURRENT_TIMESTAMP(6)),RECONCILED_BY=?,RECONCILED_AT=CURRENT_TIMESTAMP(6),RECONCILIATION_REASON=? WHERE ID=?`, []any{a.Me.User.ID, explanation, requestID}, "UPDATE gateway_requests SET capacity_held=false,state=CASE WHEN outcome IS NULL THEN 'reconciled' ELSE 'reported' END,resolved_at=COALESCE(resolved_at,clock_timestamp()),reconciled_by=$2,reconciled_at=clock_timestamp(),reconciliation_reason=$3 WHERE id=$1", `UPDATE gateway_requests SET capacity_held=false,state=CASE WHEN outcome IS NULL THEN 'reconciled' ELSE 'reported' END,resolved_at=COALESCE(resolved_at,UTC_TIMESTAMP(6)),reconciled_by=?,reconciled_at=UTC_TIMESTAMP(6),reconciliation_reason=? WHERE id=?`, []any{requestID, a.Me.User.ID, explanation}, []any{a.Me.User.ID, explanation, requestID})); e != nil {
			return e
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE GATEWAY_RECOVERIES SET CONFIRMED_AT=CURRENT_TIMESTAMP(6),CONFIRMATION_FINGERPRINT=?,CONFIRMED_DEPLOYMENT_REVISION=? WHERE ID=?`, []any{expected, deploymentRevision, id}, "UPDATE gateway_recoveries SET confirmed_at=clock_timestamp(),confirmation_fingerprint=$2,confirmed_deployment_revision=$3 WHERE id=$1", `UPDATE gateway_recoveries SET confirmed_at=UTC_TIMESTAMP(6),confirmation_fingerprint=?,confirmed_deployment_revision=? WHERE id=?`, []any{id, expected, deploymentRevision}, []any{expected, deploymentRevision, id})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.request_reconciled", "gateway_request", requestID, explanation, request); e != nil {
			return e
		}
		out, e = challengeInScope(ctx, tx, w, requestID, id)
		return e
	})
	if e != nil {
		return RecoveryChallenge{}, e
	}
	return out, nil
}
