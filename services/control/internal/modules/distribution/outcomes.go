package distribution

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"time"
)

// Report accepts only metadata for this credential's already committed admission, including after revocation.
func (s *Service) Report(ctx context.Context, gateway, credential string, outcome wire.Outcome, request string) (wire.Acknowledgment, error) {
	if outcome.Validate() != nil {
		return wire.Acknowledgment{}, fail("VALIDATION_FAILED")
	}
	digest, e := outcome.Digest()
	if e != nil {
		return wire.Acknowledgment{}, e
	}
	canonical, e := outcome.Canonical()
	if e != nil {
		return wire.Acknowledgment{}, e
	}
	var scope machineScope
	e = s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var e error
		scope, e = s.gatewayAuthority(ctx, q, gateway, credential, true, false)
		return e
	})
	if e != nil {
		return wire.Acknowledgment{}, e
	}
	e = s.storageTransaction(ctx, func(ctx context.Context, tx transaction.Executor) error {
		var workspace string
		e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT W.ID FROM WORKSPACES W JOIN INSTALLATION_STATE I ON I.TENANT_ID=W.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND W.TENANT_ID=? AND W.ID=? FOR UPDATE`, []any{scope.tenant, scope.workspace}, "SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR UPDATE OF w", `SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, []any{scope.tenant, scope.workspace}, []any{scope.tenant, scope.workspace})).Scan(&workspace)
		if e != nil {
			return e
		}
		fresh, e := s.gatewayAuthority(ctx, tx, gateway, credential, true, true)
		if e != nil {
			return e
		}
		if fresh != scope {
			return fail("GATEWAY_UNAUTHORIZED")
		}
		var previousHash *string
		var previous []byte
		var maximumOutput, maximumContext int
		var capacityHeld bool
		var reconciledAt *time.Time
		e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT OUTCOME_SHA256,OUTCOME,MAXIMUM_OUTPUT_TOKENS,MAXIMUM_CONTEXT_TOKENS,CAPACITY_HELD,RECONCILED_AT FROM GATEWAY_REQUESTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND GATEWAY_ID=? AND ID=? FOR UPDATE`, []any{scope.tenant, scope.workspace, scope.gateway, outcome.RequestID}, "SELECT outcome_sha256,outcome,maximum_output_tokens,maximum_context_tokens,capacity_held,reconciled_at FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND gateway_id=$3 AND id=$4 FOR UPDATE", `SELECT outcome_sha256,outcome,maximum_output_tokens,maximum_context_tokens,capacity_held,reconciled_at FROM gateway_requests WHERE tenant_id=? AND workspace_id=? AND gateway_id=? AND id=? FOR UPDATE`, []any{scope.tenant, scope.workspace, scope.gateway, outcome.RequestID}, []any{scope.tenant, scope.workspace, scope.gateway, outcome.RequestID})).Scan(&previousHash, &previous, &maximumOutput, &maximumContext, &capacityHeld, &reconciledAt)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("GATEWAY_REQUEST_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if usage := outcome.Usage; usage != nil && (usage.CompletionTokens > uint32(maximumOutput) || usage.TotalTokens > uint32(maximumContext)) {
			return fail("GATEWAY_USAGE_INVALID")
		}
		if previousHash != nil {
			saved, e := wire.DecodeOutcome(previous)
			if e != nil {
				return fail("STORAGE_UNAVAILABLE")
			}
			savedDigest, e := saved.Digest()
			if e != nil || subtle.ConstantTimeCompare([]byte(savedDigest), []byte(*previousHash)) != 1 {
				return fail("STORAGE_UNAVAILABLE")
			}
			if subtle.ConstantTimeCompare([]byte(*previousHash), []byte(digest)) != 1 {
				return fail("GATEWAY_OUTCOME_CONFLICT")
			}
		} else {
			uncertain := outcome.Status == "incomplete" || outcome.Status == "usage_storage_failed" || (outcome.ErrorCode != nil && *outcome.ErrorCode == "gateway_interrupted")
			capacityHeld = capacityHeld && uncertain
			if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE GATEWAY_REQUESTS SET STATE='reported',CAPACITY_HELD=?,OUTCOME=?,OUTCOME_SHA256=?,REPORTED_AT=CURRENT_TIMESTAMP(6),RESOLVED_AT=COALESCE(RESOLVED_AT,CURRENT_TIMESTAMP(6)) WHERE ID=?`, []any{capacityHeld, string(canonical), digest, outcome.RequestID}, "UPDATE gateway_requests SET state='reported',capacity_held=$2,outcome=$3,outcome_sha256=$4,reported_at=clock_timestamp(),resolved_at=COALESCE(resolved_at,clock_timestamp()) WHERE id=$1", `UPDATE gateway_requests SET state='reported',capacity_held=?,outcome=CAST(? AS JSON),outcome_sha256=?,reported_at=UTC_TIMESTAMP(6),resolved_at=COALESCE(resolved_at,UTC_TIMESTAMP(6)) WHERE id=?`, []any{outcome.RequestID, capacityHeld, canonical, digest}, []any{capacityHeld, canonical, digest, outcome.RequestID})); e != nil {
				return e
			}
			action := "gateway.request_reported"
			if reconciledAt != nil {
				action = "gateway.request_reported_after_reconciliation"
			}
			// No user FK is inserted while holding only Workspace locks: account revocation uses user-before-Workspace order.
			if e = s.machineAudit(ctx, tx, scope, nil, action, outcome.RequestID, request); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return wire.Acknowledgment{}, e
	}
	return wire.Acknowledgment{RequestID: outcome.RequestID, CanonicalSHA256: digest}, nil
}
