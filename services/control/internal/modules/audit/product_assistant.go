package audit

import (
	"context"
	"errors"
	"regexp"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const (
	productAssistantActionPrefix = "product_assistant."
	maxAuditActionBytes          = 64
)

var (
	// ErrProductAssistantEventInvalid means an event does not match the writer's input contract.
	ErrProductAssistantEventInvalid = errors.New("AUDIT_ASSISTANT_EVENT_INVALID")
	// ErrProductAssistantScopeDenied means the subject cannot be recorded in the requested scope.
	ErrProductAssistantScopeDenied = errors.New("AUDIT_ASSISTANT_SCOPE_DENIED")

	productAssistantToolID = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// ProductAssistantEvent holds server-resolved attribution and a registered tool
// ID. It carries no command text, arguments or result data.
type ProductAssistantEvent struct {
	SubjectUserID string
	TenantID      string
	WorkspaceID   string
	Audience      string
	RequestID     string
	ToolID        string
	Outcome       string
}

// WriteProductAssistantWithin records the built-in assistant acting on behalf of
// the authenticated subject. The caller must authorize the operation in this
// transaction, supply its resolved scope and return any append error before
// releasing a result or committing a mutation.
func WriteProductAssistantWithin(ctx context.Context, tx transaction.Executor, event ProductAssistantEvent) error {
	_, pool := tx.(transaction.Store)
	if tx == nil || pool {
		return errors.New("AUDIT_TRANSACTION_REQUIRED")
	}
	if tx.Dialect() != transaction.PostgreSQL {
		return errors.New("AUDIT_DIALECT_UNSUPPORTED")
	}
	for _, value := range [...]string{event.SubjectUserID, event.TenantID, event.WorkspaceID, event.RequestID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ErrProductAssistantEventInvalid
		}
	}
	if event.Audience != "admin" && event.Audience != "portal" {
		return ErrProductAssistantEventInvalid
	}
	switch event.Outcome {
	case "success", "failure", "accepted":
	default:
		return ErrProductAssistantEventInvalid
	}
	if len(event.ToolID) > maxAuditActionBytes-len(productAssistantActionPrefix) ||
		!productAssistantToolID.MatchString(event.ToolID) {
		return ErrProductAssistantEventInvalid
	}

	// The scope guard binds attribution at append time without adding locks to
	// the caller's authorization and audit ordering.
	const insert = `
INSERT INTO audit_events (
    id, occurred_at, subject_user_id, tenant_id, workspace_id, audience,
    action, outcome, request_id, metadata
)
SELECT $1, clock_timestamp(), u.id, w.tenant_id, w.id, $5::text, $6, $7, $8,
    jsonb_build_object('actor_kind', 'product_assistant', 'actor_id', 'ownsolid.product-assistant')
FROM workspaces w
JOIN installation_state i ON i.id = 1 AND i.bootstrap_complete AND i.tenant_id = w.tenant_id
JOIN tenants t ON t.id = w.tenant_id AND t.status = 'active'
JOIN users u ON u.id = $4 AND u.status = 'active'
WHERE w.tenant_id = $2 AND w.id = $3 AND (
    ($5::text = 'admin' AND u.is_platform_admin)
    OR ($5::text = 'portal' AND w.status = 'active' AND EXISTS (
        SELECT 1 FROM workspace_memberships m
        WHERE m.user_id = u.id AND m.tenant_id = w.tenant_id AND m.workspace_id = w.id
    ))
)`
	rows, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, insert, uuid.NewString(),
		event.TenantID, event.WorkspaceID, event.SubjectUserID, event.Audience,
		productAssistantActionPrefix+event.ToolID, event.Outcome, event.RequestID))
	if err != nil {
		return err
	}
	switch rows {
	case 0:
		return ErrProductAssistantScopeDenied
	case 1:
		return nil
	default:
		return errors.New("AUDIT_WRITE_UNCONFIRMED")
	}
}
