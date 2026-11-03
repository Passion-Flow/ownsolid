package recovery

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func operatorReason(reason string) bool {
	return utf8.ValidString(reason) && strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= 500
}
func operatorAudit(ctx context.Context, tx pgx.Tx, tenant, workspace, action, target, id, reason string, metadata any) error {
	return operatorAuditWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, action, target, id, reason, metadata)
}

// FenceInferenceNode runs through the stopped installation's protected local operator command.
// It never stops a host process or frees an accelerator based on database state alone.
func FenceInferenceNode(ctx context.Context, accounts *identity.Service, node, reason string) error {
	return fenceInferenceNode(ctx, accounts, node, reason)
}

// ReconcileInferenceRemoval explicitly accepts a local root operator receipt after credential fencing.
// This is a reviewed operational attestation, never cryptographic/remote GPU qualification.
func ReconcileInferenceRemoval(ctx context.Context, accounts *identity.Service, receipt wire.InferenceRemovalReceipt, reason string) error {
	return reconcileInferenceRemovalSelected(ctx, accounts, receipt, reason)
}

// Signed v1 cleanup receipts have a fixed validity window across operators and validators.
const receiptFutureSkew = 5 * time.Minute
const receiptMaximumAge = 7 * 24 * time.Hour
