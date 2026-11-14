package metering

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrGatewayReceiptNotFound indicates that the exact scope has no reported request.
	ErrGatewayReceiptNotFound = errors.New("METERING_GATEWAY_RECEIPT_NOT_FOUND")
	// ErrGatewayReceiptInvalid indicates an invalid or mismatched canonical receipt.
	ErrGatewayReceiptInvalid = errors.New("METERING_GATEWAY_RECEIPT_INVALID")
)

const gatewayReceiptProducer = "gateway/receipt-v1"

// AppendGatewayReceiptWithin projects the exact receipt verified by the gateway
// owner. Call it after Report stores or rechecks the outcome, with the authenticated
// scope and acknowledgment, before the owning PostgreSQL transaction commits.
// A false result with no error is an exact ledger replay.
func AppendGatewayReceiptWithin(ctx context.Context, tx transaction.Executor, scope Scope, gatewayID uuid.UUID, receipt wire.Acknowledgment) (bool, error) {
	if ctx == nil || tx == nil || tx.Dialect() != transaction.PostgreSQL {
		return false, ErrUnavailable
	}
	if _, pool := tx.(transaction.Store); pool {
		return false, ErrUnavailable
	}
	if !scope.valid() || gatewayID == uuid.Nil || !wire.ValidID(receipt.RequestID) || len(receipt.CanonicalSHA256) != 64 {
		return false, ErrGatewayReceiptInvalid
	}

	var admittedAt time.Time
	var raw []byte
	var storedDigest string
	var maxOutput, maxContext int64
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT admitted_at, outcome, outcome_sha256, maximum_output_tokens, maximum_context_tokens
 FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND gateway_id=$3 AND id=$4 AND state='reported'
 FOR SHARE`, scope.TenantID, scope.WorkspaceID, gatewayID, receipt.RequestID)).
		Scan(&admittedAt, &raw, &storedDigest, &maxOutput, &maxContext)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrGatewayReceiptNotFound
	}
	if err != nil {
		return false, operationError(ctx)
	}
	if subtle.ConstantTimeCompare([]byte(receipt.CanonicalSHA256), []byte(storedDigest)) != 1 {
		return false, ErrGatewayReceiptInvalid
	}
	outcome, err := wire.DecodeOutcome(raw)
	if err != nil || outcome.RequestID != receipt.RequestID {
		return false, ErrGatewayReceiptInvalid
	}
	digest, err := outcome.Digest()
	if err != nil || subtle.ConstantTimeCompare([]byte(digest), []byte(storedDigest)) != 1 {
		return false, ErrGatewayReceiptInvalid
	}
	usage := Usage{Requests: 1}
	if outcome.Usage == nil {
		usage.UnmeasuredTokenRequests = 1
	} else {
		if int64(outcome.Usage.CompletionTokens) > maxOutput || int64(outcome.Usage.TotalTokens) > maxContext {
			return false, ErrGatewayReceiptInvalid
		}
		usage.Tokens = int64(outcome.Usage.TotalTokens)
	}
	return appendWithin(ctx, tx, scope, Event{
		Producer: gatewayReceiptProducer, ID: receipt.RequestID, OccurredAt: admittedAt, Usage: usage,
	})
}
